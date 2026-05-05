package memory

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const schemaVersion = 1

// Store 是 SQLite 结构化检索层。AST 仍是控制流权威来源；Store 是可重建检索索引。
type Store struct {
	db *sql.DB
	mu sync.Mutex
}

// New 打开（或创建）SQLite 数据库并初始化 schema。
// path 为 ":memory:" 时使用内存数据库（测试用）。
func New(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("memory: open db: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite 单写连接
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("memory: migrate: %w", err)
	}
	return s, nil
}

// Close 关闭数据库连接。
func (s *Store) Close() error {
	return s.db.Close()
}

// migrate 建表（幂等）。
func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER PRIMARY KEY)`,
		`INSERT OR IGNORE INTO schema_version VALUES (` + fmt.Sprintf("%d", schemaVersion) + `)`,

		`CREATE TABLE IF NOT EXISTS nodes (
			id TEXT PRIMARY KEY,
			parent_id TEXT,
			name TEXT,
			type TEXT,
			status TEXT,
			information TEXT,
			depth INTEGER,
			traversal_index INTEGER,
			created_at TEXT,
			updated_at TEXT
		)`,

		`CREATE TABLE IF NOT EXISTS node_handoffs (
			node_id TEXT PRIMARY KEY,
			goal TEXT,
			summary TEXT,
			key_facts_json TEXT,
			decisions_json TEXT,
			assumptions_json TEXT,
			artifact_refs_json TEXT,
			outputs_json TEXT,
			open_questions_json TEXT,
			handoff TEXT,
			confidence TEXT,
			updated_at TEXT
		)`,

		`CREATE TABLE IF NOT EXISTS scoped_variables (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			node_id TEXT,
			scope_node_id TEXT,
			name TEXT,
			value_ref TEXT,
			value_summary TEXT,
			updated_at TEXT
		)`,

		`CREATE TABLE IF NOT EXISTS artifacts (
			id TEXT PRIMARY KEY,
			producer_node_id TEXT,
			kind TEXT,
			title TEXT,
			summary TEXT,
			content_ref TEXT,
			pinned INTEGER DEFAULT 0,
			confidence TEXT,
			created_at TEXT,
			last_used_at TEXT
		)`,

		`CREATE VIRTUAL TABLE IF NOT EXISTS artifact_fts USING fts5(
			artifact_id UNINDEXED,
			title,
			summary,
			content
		)`,

		`CREATE INDEX IF NOT EXISTS idx_nodes_parent ON nodes(parent_id)`,
		`CREATE INDEX IF NOT EXISTS idx_nodes_status ON nodes(status)`,
		`CREATE INDEX IF NOT EXISTS idx_scoped_vars_node ON scoped_variables(node_id)`,
		`CREATE INDEX IF NOT EXISTS idx_artifacts_producer ON artifacts(producer_node_id)`,
		`CREATE INDEX IF NOT EXISTS idx_artifacts_pinned ON artifacts(pinned)`,
	}

	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("exec %q: %w", stmt[:min(40, len(stmt))], err)
		}
	}
	return nil
}

// ---- Node index ----

// UpsertNode 插入或更新节点元信息。
func (s *Store) UpsertNode(id, parentID, name, typ, status, information string, depth, traversalIndex int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		INSERT INTO nodes(id,parent_id,name,type,status,information,depth,traversal_index,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			parent_id=excluded.parent_id,
			name=excluded.name,
			type=excluded.type,
			status=excluded.status,
			information=excluded.information,
			depth=excluded.depth,
			traversal_index=excluded.traversal_index,
			updated_at=excluded.updated_at`,
		id, parentID, name, typ, status, information, depth, traversalIndex, now, now)
	return err
}

// ---- Handoff index ----

// UpsertHandoff 插入或更新节点的结构化 handoff。
func (s *Store) UpsertHandoff(nodeID, goal, summary string,
	keyFacts, decisions, assumptions, artifactRefs, outputs, openQuestions []string,
	handoff, confidence string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	kfJSON, _ := json.Marshal(keyFacts)
	decJSON, _ := json.Marshal(decisions)
	assJSON, _ := json.Marshal(assumptions)
	artJSON, _ := json.Marshal(artifactRefs)
	outJSON, _ := json.Marshal(outputs)
	oqJSON, _ := json.Marshal(openQuestions)
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := s.db.Exec(`
		INSERT INTO node_handoffs(node_id,goal,summary,key_facts_json,decisions_json,assumptions_json,
			artifact_refs_json,outputs_json,open_questions_json,handoff,confidence,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(node_id) DO UPDATE SET
			goal=excluded.goal, summary=excluded.summary,
			key_facts_json=excluded.key_facts_json, decisions_json=excluded.decisions_json,
			assumptions_json=excluded.assumptions_json, artifact_refs_json=excluded.artifact_refs_json,
			outputs_json=excluded.outputs_json, open_questions_json=excluded.open_questions_json,
			handoff=excluded.handoff, confidence=excluded.confidence, updated_at=excluded.updated_at`,
		nodeID, goal, summary,
		string(kfJSON), string(decJSON), string(assJSON),
		string(artJSON), string(outJSON), string(oqJSON),
		handoff, confidence, now)
	return err
}

// ---- Scoped variables ----

// UpsertScopedVariable 插入或更新作用域变量。
func (s *Store) UpsertScopedVariable(nodeID, scopeNodeID, name, valueRef, valueSummary string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		INSERT INTO scoped_variables(node_id,scope_node_id,name,value_ref,value_summary,updated_at)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT DO UPDATE SET
			value_ref=excluded.value_ref,
			value_summary=excluded.value_summary,
			updated_at=excluded.updated_at`,
		nodeID, scopeNodeID, name, valueRef, valueSummary, now)
	return err
}

// ---- Artifact index ----

// UpsertArtifact 插入或更新 artifact 元信息。
func (s *Store) UpsertArtifact(id, producerNodeID, kind, title, summary, contentRef string, pinned bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC().Format(time.RFC3339)
	pinnedInt := 0
	if pinned {
		pinnedInt = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO artifacts(id,producer_node_id,kind,title,summary,content_ref,pinned,created_at,last_used_at)
		VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			pinned=excluded.pinned,
			summary=excluded.summary,
			last_used_at=excluded.last_used_at`,
		id, producerNodeID, kind, title, summary, contentRef, pinnedInt, now, now)
	return err
}

// UpdateArtifactPinned 更新 artifact 的 pinned 状态。
func (s *Store) UpdateArtifactPinned(id string, pinned bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	pinnedInt := 0
	if pinned {
		pinnedInt = 1
	}
	_, err := s.db.Exec(`UPDATE artifacts SET pinned=? WHERE id=?`, pinnedInt, id)
	return err
}

// IndexArtifactFTS 将 artifact 内容写入 FTS 索引。
func (s *Store) IndexArtifactFTS(artifactID, title, summary, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 先删除旧记录再插入（FTS5 不支持 ON CONFLICT）
	if _, err := s.db.Exec(`DELETE FROM artifact_fts WHERE artifact_id=?`, artifactID); err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT INTO artifact_fts(artifact_id,title,summary,content) VALUES(?,?,?,?)`,
		artifactID, title, summary, content)
	return err
}

// ---- Query API ----

// NodeBrief 节点摘要（用于 activation context）
type NodeBrief struct {
	ID          string
	Name        string
	Type        string
	Status      string
	Information string
	Depth       int
}

// HandoffBrief 结构化 handoff 摘要
type HandoffBrief struct {
	NodeID       string
	Goal         string
	Summary      string
	KeyFacts     []string
	Decisions    []string
	ArtifactRefs []string
	OpenQuestions []string
	Handoff      string
	Confidence   string
}

// ArtifactBrief artifact 摘要
type ArtifactBrief struct {
	ID             string
	ProducerNodeID string
	Kind           string
	Title          string
	Summary        string
	Pinned         bool
}

// ScopedVariable 作用域变量
type ScopedVariable struct {
	NodeID      string
	ScopeNodeID string
	Name        string
	ValueRef    string
	ValueSummary string
}

// QueryAncestorChain 查询节点的祖先链（从根到父节点）。
func (s *Store) QueryAncestorChain(nodeID string) ([]NodeBrief, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var chain []NodeBrief
	current := nodeID
	seen := map[string]bool{}

	for {
		if seen[current] {
			break
		}
		seen[current] = true

		row := s.db.QueryRow(`SELECT id,parent_id,name,type,status,information,depth FROM nodes WHERE id=?`, current)
		var n NodeBrief
		var parentID sql.NullString
		if err := row.Scan(&n.ID, &parentID, &n.Name, &n.Type, &n.Status, &n.Information, &n.Depth); err != nil {
			break
		}
		chain = append([]NodeBrief{n}, chain...) // prepend
		if !parentID.Valid || parentID.String == "" {
			break
		}
		current = parentID.String
	}
	return chain, nil
}

// QuerySiblingHandoffs 查询同父节点下已完成或失败的兄弟节点 handoff（最多 limit 条）。
func (s *Store) QuerySiblingHandoffs(parentID string, excludeNodeID string, statuses []string, limit int) ([]HandoffBrief, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(statuses) == 0 {
		statuses = []string{"Completed", "Failed"}
	}
	placeholders := make([]string, len(statuses))
	args := []interface{}{parentID, excludeNodeID}
	for i, st := range statuses {
		placeholders[i] = "?"
		args = append(args, st)
	}
	args = append(args, limit)

	query := fmt.Sprintf(`
		SELECT n.id, h.goal, h.summary, h.key_facts_json, h.decisions_json,
		       h.artifact_refs_json, h.open_questions_json, h.handoff, h.confidence
		FROM nodes n
		LEFT JOIN node_handoffs h ON n.id = h.node_id
		WHERE n.parent_id=? AND n.id!=? AND n.status IN (%s)
		ORDER BY n.traversal_index DESC
		LIMIT ?`, strings.Join(placeholders, ","))

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []HandoffBrief
	for rows.Next() {
		var b HandoffBrief
		var kfJSON, decJSON, artJSON, oqJSON sql.NullString
		var goal, summary, handoff, confidence sql.NullString
		if err := rows.Scan(&b.NodeID, &goal, &summary, &kfJSON, &decJSON, &artJSON, &oqJSON, &handoff, &confidence); err != nil {
			continue
		}
		b.Goal = goal.String
		b.Summary = summary.String
		b.Handoff = handoff.String
		b.Confidence = confidence.String
		parseJSONSlice(kfJSON.String, &b.KeyFacts)
		parseJSONSlice(decJSON.String, &b.Decisions)
		parseJSONSlice(artJSON.String, &b.ArtifactRefs)
		parseJSONSlice(oqJSON.String, &b.OpenQuestions)
		result = append(result, b)
	}
	return result, rows.Err()
}

// QueryPinnedArtifacts 查询所有 pinned artifacts。
func (s *Store) QueryPinnedArtifacts(limit int) ([]ArtifactBrief, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(`
		SELECT id,producer_node_id,kind,title,summary,pinned
		FROM artifacts WHERE pinned=1
		ORDER BY last_used_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanArtifactBriefs(rows)
}

// QueryRecentArtifacts 查询最近使用的 artifacts（不含 pinned）。
func (s *Store) QueryRecentArtifacts(limit int) ([]ArtifactBrief, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(`
		SELECT id,producer_node_id,kind,title,summary,pinned
		FROM artifacts
		ORDER BY last_used_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanArtifactBriefs(rows)
}

// QueryArtifactsByNode 查询某节点产生的 artifacts。
func (s *Store) QueryArtifactsByNode(nodeID string) ([]ArtifactBrief, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(`
		SELECT id,producer_node_id,kind,title,summary,pinned
		FROM artifacts WHERE producer_node_id=?
		ORDER BY created_at DESC`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanArtifactBriefs(rows)
}

// SearchArtifactsFTS 用 FTS5 全文检索 artifacts，返回匹配的 artifact IDs 和摘要片段。
func (s *Store) SearchArtifactsFTS(query string, limit int) ([]ArtifactBrief, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(`
		SELECT f.artifact_id, a.producer_node_id, a.kind, a.title, a.summary, a.pinned
		FROM artifact_fts f
		JOIN artifacts a ON a.id = f.artifact_id
		WHERE artifact_fts MATCH ?
		ORDER BY rank
		LIMIT ?`, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanArtifactBriefs(rows)
}

// QueryScopedVariables 查询节点路径上的作用域变量（nearest scope wins）。
func (s *Store) QueryScopedVariables(nodeIDs []string) ([]ScopedVariable, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(nodeIDs) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(nodeIDs))
	args := make([]interface{}, len(nodeIDs))
	for i, id := range nodeIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	rows, err := s.db.Query(fmt.Sprintf(`
		SELECT node_id,scope_node_id,name,value_ref,value_summary
		FROM scoped_variables
		WHERE scope_node_id IN (%s)
		ORDER BY updated_at DESC`, strings.Join(placeholders, ",")), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ScopedVariable
	for rows.Next() {
		var v ScopedVariable
		if err := rows.Scan(&v.NodeID, &v.ScopeNodeID, &v.Name, &v.ValueRef, &v.ValueSummary); err != nil {
			continue
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

// ---- Rebuild ----

// RebuildInput 是从 AST + artifact store 重建索引所需的输入结构。
type RebuildInput struct {
	Nodes     []RebuildNode
	Artifacts []RebuildArtifact
}

// RebuildNode 节点重建输入
type RebuildNode struct {
	ID            string
	ParentID      string
	Name          string
	Type          string
	Status        string
	Information   string
	Depth         int
	TraversalIndex int
	// Handoff fields
	Goal          string
	Summary       string
	KeyFacts      []string
	Decisions     []string
	Assumptions   []string
	ArtifactRefs  []string
	Outputs       []string
	OpenQuestions []string
	Handoff       string
	Confidence    string
	HasHandoff    bool
}

// RebuildArtifact artifact 重建输入
type RebuildArtifact struct {
	ID             string
	ProducerNodeID string
	Kind           string
	Title          string
	Summary        string
	ContentRef     string
	Pinned         bool
	Content        string // 用于 FTS 索引
}

// Rebuild 从 AST + artifact store 完整重建 SQLite 索引。
func (s *Store) Rebuild(input RebuildInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 清空所有表
	for _, tbl := range []string{"nodes", "node_handoffs", "scoped_variables", "artifacts", "artifact_fts"} {
		if _, err := tx.Exec("DELETE FROM " + tbl); err != nil {
			return fmt.Errorf("clear %s: %w", tbl, err)
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)

	for _, n := range input.Nodes {
		if _, err := tx.Exec(`
			INSERT INTO nodes(id,parent_id,name,type,status,information,depth,traversal_index,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?)`,
			n.ID, n.ParentID, n.Name, n.Type, n.Status, n.Information, n.Depth, n.TraversalIndex, now, now); err != nil {
			return fmt.Errorf("insert node %s: %w", n.ID, err)
		}

		if n.HasHandoff {
			kfJSON, _ := json.Marshal(n.KeyFacts)
			decJSON, _ := json.Marshal(n.Decisions)
			assJSON, _ := json.Marshal(n.Assumptions)
			artJSON, _ := json.Marshal(n.ArtifactRefs)
			outJSON, _ := json.Marshal(n.Outputs)
			oqJSON, _ := json.Marshal(n.OpenQuestions)
			if _, err := tx.Exec(`
				INSERT INTO node_handoffs(node_id,goal,summary,key_facts_json,decisions_json,assumptions_json,
					artifact_refs_json,outputs_json,open_questions_json,handoff,confidence,updated_at)
				VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
				n.ID, n.Goal, n.Summary,
				string(kfJSON), string(decJSON), string(assJSON),
				string(artJSON), string(outJSON), string(oqJSON),
				n.Handoff, n.Confidence, now); err != nil {
				return fmt.Errorf("insert handoff %s: %w", n.ID, err)
			}
		}
	}

	for _, a := range input.Artifacts {
		pinnedInt := 0
		if a.Pinned {
			pinnedInt = 1
		}
		if _, err := tx.Exec(`
			INSERT INTO artifacts(id,producer_node_id,kind,title,summary,content_ref,pinned,created_at,last_used_at)
			VALUES(?,?,?,?,?,?,?,?,?)`,
			a.ID, a.ProducerNodeID, a.Kind, a.Title, a.Summary, a.ContentRef, pinnedInt, now, now); err != nil {
			return fmt.Errorf("insert artifact %s: %w", a.ID, err)
		}
		if a.Content != "" {
			if _, err := tx.Exec(`INSERT INTO artifact_fts(artifact_id,title,summary,content) VALUES(?,?,?,?)`,
				a.ID, a.Title, a.Summary, a.Content); err != nil {
				return fmt.Errorf("insert fts %s: %w", a.ID, err)
			}
		}
	}

	return tx.Commit()
}

// ---- helpers ----

func scanArtifactBriefs(rows *sql.Rows) ([]ArtifactBrief, error) {
	var result []ArtifactBrief
	for rows.Next() {
		var b ArtifactBrief
		var pinnedInt int
		if err := rows.Scan(&b.ID, &b.ProducerNodeID, &b.Kind, &b.Title, &b.Summary, &pinnedInt); err != nil {
			continue
		}
		b.Pinned = pinnedInt == 1
		result = append(result, b)
	}
	return result, rows.Err()
}

func parseJSONSlice(s string, out *[]string) {
	if s == "" || s == "null" {
		return
	}
	_ = json.Unmarshal([]byte(s), out)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
