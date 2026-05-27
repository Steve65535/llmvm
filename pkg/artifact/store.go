package artifact

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	MaxContentSize   = 8000                      // 单个 artifact 内联上限（字节），超出溢出磁盘
	MaxStoreSize     = 50                        // Store 最多保留多少个 artifact（含墓碑）
	SpillDir         = "test/sandbox/.artifacts" // 大对象溢出目录
	DefaultSliceSize = 50                        // read_artifact 默认读取行数
)

// ArtifactScope 表示 artifact 的作用域：节点级、子树级、全局。
type ArtifactScope string

const (
	ScopeNode    ArtifactScope = "node"
	ScopeSubtree ArtifactScope = "subtree"
	ScopeGlobal  ArtifactScope = "global"
)

// SourceMeta 描述 artifact 来源。模型显式 add_artifact 时填写。
type SourceMeta struct {
	Kind   string `json:"kind,omitempty"`   // file / command / search / handoff / inference
	Path   string `json:"path,omitempty"`   // 文件路径或命令字符串
	Locator string `json:"locator,omitempty"` // 例如 lines 40-120
}

// Artifact 是一个带稳定 ID 的信息对象（一等 DSL 实体）。
//
// 在 RAG/位置工程改造中，Artifact 不再是工具结果的副产品，而是节点显式生产、引用、修改的最小信息单元：
//   - Name / Tags / Granularity / Importance：让模型主动命名和分类
//   - Scope：决定召回范围（node / subtree / global）
//   - Version + SupersededBy：modify_artifact 通过追加新版本实现，不在原 ID 上原地改
type Artifact struct {
	ID          string `json:"id"`
	Type        string `json:"type"`           // file_read, search, dir_list, command, evidence, design_contract, ...
	Source      string `json:"source"`         // 文件路径 / pattern / 命令（保留向后兼容）
	Summary     string `json:"summary"`        // Runtime 生成的 1 行摘要（或模型提供）
	Truncated   bool   `json:"truncated"`
	OriginalLen int    `json:"original_len"`
	TotalLines  int    `json:"total_lines"`
	CreatedBy   string `json:"created_by"` // 节点 ID
	CreatedAt   int64  `json:"created_at"`
	Pinned      bool   `json:"pinned"`

	// 内容存储：二选一。Content 不序列化到 JSON state。
	Content   string `json:"-"`
	SpillPath string `json:"spill_path,omitempty"`

	// 墓碑标记：淘汰后 Content/SpillPath 清空，但元信息保留
	Evicted bool `json:"evicted,omitempty"`

	// === RAG / 一等 DSL 扩展字段（模型 add_artifact / modify_artifact 时填写） ===
	Name         string        `json:"name,omitempty"`
	Scope        ArtifactScope `json:"scope,omitempty"`
	Tags         []string      `json:"tags,omitempty"`
	Granularity  string        `json:"granularity,omitempty"` // case-level / contract-level / fragment-level / decision
	Importance   string        `json:"importance,omitempty"`  // high / medium / low
	TokenCount   int           `json:"token_count,omitempty"`
	SourceMeta   *SourceMeta   `json:"source_meta,omitempty"`
	Version      int           `json:"version,omitempty"`        // 1, 2, 3 ...（同 Name 的递增）
	SupersededBy string        `json:"superseded_by,omitempty"`  // 被新版本 artifact 取代时填入新 artifact ID
	Supersedes   string        `json:"supersedes,omitempty"`     // 当前 artifact 替代了哪个旧 artifact
}

// Store 管理所有 artifact 的生命周期
type Store struct {
	artifacts []*Artifact
	index     map[string]*Artifact
	counter   int
	mu        sync.Mutex
}

// New 创建空 Store
func New() *Store {
	return &Store{
		artifacts: make([]*Artifact, 0),
		index:     make(map[string]*Artifact),
	}
}

// storeJSON 是 Store 的序列化格式
type storeJSON struct {
	Artifacts []*Artifact `json:"artifacts"`
	Counter   int         `json:"counter"`
}

// MarshalJSON 序列化 Store（只保存元信息，Content 不序列化）
func (s *Store) MarshalJSON() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return json.Marshal(storeJSON{
		Artifacts: s.artifacts,
		Counter:   s.counter,
	})
}

// UnmarshalJSON 反序列化 Store，重建 index，检测 spill 文件可用性
func (s *Store) UnmarshalJSON(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var sj storeJSON
	if err := json.Unmarshal(data, &sj); err != nil {
		return err
	}
	s.artifacts = sj.Artifacts
	s.counter = sj.Counter
	s.index = make(map[string]*Artifact)
	for _, art := range s.artifacts {
		s.index[art.ID] = art
		// 检测 spill 文件是否仍然存在，不存在则标记为 evicted
		if art.SpillPath != "" && !art.Evicted {
			if _, err := os.Stat(art.SpillPath); os.IsNotExist(err) {
				art.Evicted = true
				art.SpillPath = ""
				fmt.Printf("  ⚠️ Artifact %s spill file missing, marked as evicted (summary retained: %s)\n", art.ID, art.Summary)
			}
		}
	}
	return nil
}

// AddRequest 是 add_artifact action 的结构化输入。
//
// runtime 工具产物（read_file / execute_command / ...）走 Add 兼容入口；
// 模型显式 add_artifact 走 AddStructured。两者最终都进同一个 Store。
type AddRequest struct {
	Type        string
	Source      string
	Content     string
	CreatedBy   string
	Name        string
	Scope       ArtifactScope
	Tags        []string
	Granularity string
	Importance  string
	Summary     string // 模型提供的 summary；为空则 runtime 自动生成
	SourceMeta  *SourceMeta
	Supersedes  string // modify_artifact：要替代的旧 artifact ID
}

// Add 兼容旧 4-arg 入口：runtime 工具产物走这里，元信息由 runtime 推导。
func (s *Store) Add(typ, source, content, createdBy string) *Artifact {
	return s.AddStructured(AddRequest{
		Type:      typ,
		Source:    source,
		Content:   content,
		CreatedBy: createdBy,
		Scope:     ScopeNode,
	})
}

// AddStructured 写入一个一等 artifact。大于 MaxContentSize 的内容自动溢出磁盘。
// 若 Store 已满，淘汰最老的未 pin artifact。
func (s *Store) AddStructured(req AddRequest) *Artifact {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.counter++
	id := fmt.Sprintf("art_%d", s.counter)

	totalLines := strings.Count(req.Content, "\n") + 1
	originalLen := len(req.Content)
	truncated := false

	scope := req.Scope
	if scope == "" {
		scope = ScopeNode
	}

	// 推导版本号：同 CreatedBy + Name 已存在时递增；否则 1。
	version := 1
	if req.Name != "" {
		for _, prev := range s.artifacts {
			if prev.Name == req.Name && !prev.Evicted {
				if prev.Version >= version {
					version = prev.Version + 1
				}
			}
		}
	}

	art := &Artifact{
		ID:          id,
		Type:        req.Type,
		Source:      req.Source,
		OriginalLen: originalLen,
		TotalLines:  totalLines,
		CreatedBy:   req.CreatedBy,
		CreatedAt:   time.Now().Unix(),
		Name:        req.Name,
		Scope:       scope,
		Tags:        req.Tags,
		Granularity: req.Granularity,
		Importance:  req.Importance,
		SourceMeta:  req.SourceMeta,
		Version:     version,
		Supersedes:  req.Supersedes,
		TokenCount:  estimateTokens(req.Content),
	}

	// 摘要：优先用模型给的，否则 runtime 生成
	if req.Summary != "" {
		art.Summary = req.Summary
	} else {
		art.Summary = generateSummary(req.Type, req.Source, req.Content, totalLines, originalLen)
	}

	// 大对象溢出磁盘
	if len(req.Content) > MaxContentSize {
		spillPath := filepath.Join(SpillDir, id+".txt")
		if err := os.MkdirAll(SpillDir, 0755); err == nil {
			if err := os.WriteFile(spillPath, []byte(req.Content), 0644); err == nil {
				art.SpillPath = spillPath
				art.Content = ""
				truncated = false
			} else {
				art.Content = req.Content[:MaxContentSize]
				truncated = true
			}
		} else {
			art.Content = req.Content[:MaxContentSize]
			truncated = true
		}
	} else {
		art.Content = req.Content
	}
	art.Truncated = truncated

	s.artifacts = append(s.artifacts, art)
	s.index[id] = art

	// modify_artifact: 把被替代的旧 artifact 标记 SupersededBy
	if req.Supersedes != "" {
		if old, ok := s.index[req.Supersedes]; ok {
			old.SupersededBy = id
		}
	}

	s.evictIfNeeded()
	return art
}

// estimateTokens 粗估 token 数：英文 ≈ 4 chars/token，CJK ≈ 1.5 chars/token。
func estimateTokens(text string) int {
	cjk := 0
	for _, r := range text {
		if r > 127 {
			cjk++
		}
	}
	en := len(text) - cjk
	return en/4 + cjk*2/3
}

// Get 获取 artifact 元信息
func (s *Store) Get(id string) *Artifact {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.index[id]
}

// Pin 标记 artifact 不被淘汰
func (s *Store) Pin(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if art, ok := s.index[id]; ok {
		art.Pinned = true
	}
}

// ReadSlice 分片读取 artifact 内容。返回 [startLine, endLine) 范围的文本。
// startLine 从 1 开始。endLine=0 表示读到 startLine+DefaultSliceSize。
func (s *Store) ReadSlice(id string, startLine, endLine int) (string, error) {
	s.mu.Lock()
	art, ok := s.index[id]
	s.mu.Unlock()

	if !ok {
		return "", fmt.Errorf("artifact %s not found", id)
	}
	if art.Evicted {
		return "", fmt.Errorf("artifact %s has been evicted (summary: %s)", id, art.Summary)
	}

	content, err := s.loadContent(art)
	if err != nil {
		return "", err
	}

	lines := strings.Split(content, "\n")

	if startLine < 1 {
		startLine = 1
	}
	if endLine <= 0 {
		endLine = startLine + DefaultSliceSize
	}
	if startLine > len(lines) {
		return "", fmt.Errorf("start_line %d exceeds total lines %d", startLine, len(lines))
	}
	if endLine > len(lines)+1 {
		endLine = len(lines) + 1
	}

	slice := lines[startLine-1 : endLine-1]
	return strings.Join(slice, "\n"), nil
}

// ListByNode 返回某个节点产生的所有 artifact
func (s *Store) ListByNode(nodeID string) []*Artifact {
	s.mu.Lock()
	defer s.mu.Unlock()

	var result []*Artifact
	for _, art := range s.artifacts {
		if art.CreatedBy == nodeID {
			result = append(result, art)
		}
	}
	return result
}

// ListAll 返回所有 artifact（按加入顺序，最新在后）
func (s *Store) ListAll() []*Artifact {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]*Artifact, len(s.artifacts))
	copy(result, s.artifacts)
	return result
}

// Index 返回紧凑索引供 prompt 使用（只有 ID + Type + Source + Summary）
// maxEntries 限制条数，maxChars 限制总字符数，优先最近的
func (s *Store) Index(maxEntries int, maxChars int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.artifacts) == 0 {
		return "No artifacts yet.\n"
	}

	var sb strings.Builder
	start := 0
	if len(s.artifacts) > maxEntries {
		start = len(s.artifacts) - maxEntries
	}

	for i := start; i < len(s.artifacts); i++ {
		art := s.artifacts[i]
		evictTag := ""
		if art.Evicted {
			evictTag = " [EVICTED]"
		}
		pinTag := ""
		if art.Pinned {
			pinTag = " [PINNED]"
		}
		line := fmt.Sprintf("- %s (%s) %s%s%s — %s\n",
			art.ID, art.Type, art.Source, pinTag, evictTag, art.Summary)
		if maxChars > 0 && sb.Len()+len(line) > maxChars {
			sb.WriteString(fmt.Sprintf("... (%d more artifacts omitted)\n", len(s.artifacts)-i))
			break
		}
		sb.WriteString(line)
	}
	return sb.String()
}

// loadContent 从内存或磁盘加载 artifact 全文
func (s *Store) loadContent(art *Artifact) (string, error) {
	if art.Content != "" {
		return art.Content, nil
	}
	if art.SpillPath != "" {
		data, err := os.ReadFile(art.SpillPath)
		if err != nil {
			return "", fmt.Errorf("failed to read spill file %s: %w", art.SpillPath, err)
		}
		return string(data), nil
	}
	return "", fmt.Errorf("artifact %s has no content and no spill path", art.ID)
}

// evictIfNeeded 淘汰最老的未 pin artifact（保留 Summary 作为墓碑）
func (s *Store) evictIfNeeded() {
	for s.activeCount() > MaxStoreSize {
		evicted := false
		for _, art := range s.artifacts {
			if !art.Pinned && !art.Evicted {
				art.Content = ""
				if art.SpillPath != "" {
					_ = os.Remove(art.SpillPath)
					art.SpillPath = ""
				}
				art.Evicted = true
				evicted = true
				break
			}
		}
		if !evicted {
			break
		}
	}
}

// activeCount 返回未淘汰的 artifact 数量
func (s *Store) activeCount() int {
	count := 0
	for _, art := range s.artifacts {
		if !art.Evicted {
			count++
		}
	}
	return count
}

// generateSummary 纯 Go 生成摘要，不调 LLM
func generateSummary(typ, source, content string, totalLines, originalLen int) string {
	switch typ {
	case "file_read":
		return fmt.Sprintf("%s (%d bytes, %d lines)", source, originalLen, totalLines)
	case "search":
		matchCount := strings.Count(content, "\n")
		if strings.TrimSpace(content) == "" {
			return fmt.Sprintf("grep %q: no matches", source)
		}
		return fmt.Sprintf("grep in %s: %d matches", source, matchCount)
	case "dir_list":
		entryCount := strings.Count(content, "\n") + 1
		if strings.TrimSpace(content) == "" {
			entryCount = 0
		}
		return fmt.Sprintf("%s: %d entries", source, entryCount)
	case "command":
		firstLine := content
		if idx := strings.Index(content, "\n"); idx > 0 {
			firstLine = content[:idx]
		}
		if len(firstLine) > 80 {
			firstLine = firstLine[:80] + "..."
		}
		return fmt.Sprintf("$ %s → %s", source, firstLine)
	default:
		return fmt.Sprintf("%s: %d bytes", source, originalLen)
	}
}
