package llm

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Steve65535/llmvm/pkg/tasknode"
)

// AcceptanceCriterionDTO 是 create_node payload 中的验收标准。
type AcceptanceCriterionDTO struct {
	ID           string `json:"id,omitempty"`
	Description  string `json:"description"`
	Required     bool   `json:"required"`
	CheckType    string `json:"check_type"`              // testable / manual / llm_judge
	CheckCommand string `json:"check_command,omitempty"` // testable 用
	ExpectedExit int    `json:"expected_exit,omitempty"` // testable 用
}

// AcceptanceResultDTO 是 mark_complete payload 中的验收结果。
type AcceptanceResultDTO struct {
	CriterionID          string   `json:"criterion_id"`
	Passed               bool     `json:"passed"`
	Notes                string   `json:"notes,omitempty"`
	EvidenceArtifactRefs []string `json:"evidence_artifact_refs,omitempty"`
}

// NodeDTO 对应 response.json 中的 node 结构
type NodeDTO struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Type        string                 `json:"type"` // "Normal", "Leaf"
	Information string                 `json:"information"`
	Variables   map[string]interface{} `json:"variables,omitempty"`
	Index       int                    `json:"index,omitempty"`
	IsImportant bool                   `json:"is_important,omitempty"`

	// 🆕 新增：错误处理
	ErrorHandlerID string `json:"error_handler_id,omitempty"`
	MaxRetries     int    `json:"max_retries,omitempty"`

	// === 验收标准（替代 Loop）===
	AcceptanceCriteria []AcceptanceCriterionDTO `json:"acceptance_criteria,omitempty"`
}

// NodeState 对应 request.json 中的 parent_info/current_info
// 比 NodeDTO 多了 Status 字段
type NodeState struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Type        string                 `json:"type"`
	Status      string                 `json:"status"`
	Information string                 `json:"information"`
	Variables   map[string]interface{} `json:"variables,omitempty"`
	Index       int                    `json:"index,omitempty"`
	Result      string                 `json:"result,omitempty"`
	IsImportant bool                   `json:"is_important,omitempty"`
}

// Request 对应 request.json 的根结构
// 用于向 LLM 发送请求
type Request struct {
	TaskPath    []string  `json:"task_path"`
	ParentInfo  NodeState `json:"parent_info"`
	CurrentInfo NodeState `json:"current_info"`
	Request     string    `json:"request"`
}

// Action 对应 response.json 中的 action 结构
type Action struct {
	ActionType  string                 `json:"action_type"`
	Node        NodeDTO                `json:"node"`
	Variables   map[string]interface{} `json:"variables,omitempty"` // For updating current node variables
	Result      string                 `json:"result,omitempty"`    // For setting node result
	Command     string                 `json:"command,omitempty"`   // For execute_command
	IsImportant bool                   `json:"is_important,omitempty"`

	// 🆕 新增：文件追加操作
	FilePath string `json:"file_path,omitempty"` // For append_to_file
	Content  string `json:"content,omitempty"`   // For append_to_file

	// 🆕 新增：错误处理
	ErrorHandlerNode NodeDTO `json:"error_handler_node,omitempty"`

	// Node Report（mark_complete 结构化交接）
	Summary       string   `json:"summary,omitempty"`
	KeyFacts      []string `json:"key_facts,omitempty"`
	ArtifactRefs  []string `json:"artifact_refs,omitempty"`
	Handoff       string   `json:"handoff,omitempty"`

	// 扩展结构化交接字段
	Goal          string   `json:"goal,omitempty"`
	Decisions     []string `json:"decisions,omitempty"`
	Assumptions   []string `json:"assumptions,omitempty"`
	Outputs       []string `json:"outputs,omitempty"`
	OpenQuestions []string `json:"open_questions,omitempty"`
	Confidence    string   `json:"confidence,omitempty"`

	// read_artifact 分片读取
	ArtifactID string `json:"artifact_id,omitempty"`
	StartLine  int    `json:"start_line,omitempty"`
	EndLine    int    `json:"end_line,omitempty"`

	// request_human_input
	Question string   `json:"question,omitempty"`
	Context  string   `json:"context,omitempty"`
	Options  []string `json:"options,omitempty"`
	Blocking bool     `json:"blocking,omitempty"`

	// query_memory
	QueryType string                 `json:"query_type,omitempty"`
	Filters   map[string]interface{} `json:"filters,omitempty"`
	Limit     int                    `json:"limit,omitempty"`

	// === add_artifact / modify_artifact (一等 DSL) ===
	ArtifactName        string                  `json:"artifact_name,omitempty"`
	ArtifactType        string                  `json:"artifact_type,omitempty"` // 复用 type 名："evidence", "design_contract", ...
	ArtifactScope       string                  `json:"scope,omitempty"`         // node / subtree / global
	ArtifactTags        []string                `json:"tags,omitempty"`
	ArtifactGranularity string                  `json:"granularity,omitempty"`
	ArtifactImportance  string                  `json:"importance,omitempty"`
	ArtifactSource      *ArtifactSourceDTO      `json:"source,omitempty"`
	SupersedesArtifact  string                  `json:"supersedes,omitempty"` // modify_artifact 时指向旧 artifact

	// === acceptance_results (mark_complete 时附带) ===
	AcceptanceResults []AcceptanceResultDTO `json:"acceptance_results,omitempty"`

	// === request_context (节点显式请求补充上下文) ===
	Needs []ContextNeedDTO `json:"needs,omitempty"`
}

// ArtifactSourceDTO add_artifact payload 中的 source 子结构
type ArtifactSourceDTO struct {
	Kind    string `json:"kind,omitempty"`
	Path    string `json:"path,omitempty"`
	Locator string `json:"locator,omitempty"`
}

// ContextNeedDTO request_context payload 中的单条需求
type ContextNeedDTO struct {
	Kind      string `json:"kind"` // artifact / siblings / ancestors / vector / fts
	Query     string `json:"query"`
	Scope     string `json:"scope,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Rationale string `json:"rationale,omitempty"`
}

// Response 对应 response.json 的根结构
type Response struct {
	Actions []Action `json:"actions"`
}

// ParseResponse 解析 LLM 返回的 JSON 字符串
// 包含基本的 Markdown 清理和错误处理
func ParseResponse(jsonStr string) (*Response, error) {
	// 1. 清理 LLM 可能输出的 Markdown 代码块标记 & 空格
	cleaned := CleanJSONString(jsonStr)

	// 2. 尝试解析
	var resp Response
	err := json.Unmarshal([]byte(cleaned), &resp)
	if err != nil {
		// 捕捉 JSON 格式错误
		return nil, fmt.Errorf("JSON parse error: %w | Input: %s", err, cleaned)
	}

	// 3. 业务逻辑校验 (Try-Catch 逻辑的一部分)
	for i, action := range resp.Actions {
		if action.ActionType == "" {
			return nil, fmt.Errorf("action %d missing action_type", i)
		}

		// 对于 create_node action，验证 Node Type 是否合法
		if action.ActionType == "create_node" {
			switch action.Node.Type {
			case "Normal", "Leaf":
				// valid
			case "":
				return nil, fmt.Errorf("action %d (create_node) missing node type", i)
			case "Loop":
				return nil, fmt.Errorf("action %d (create_node) Loop type has been removed; use acceptance_criteria instead", i)
			default:
				return nil, fmt.Errorf("action %d contains invalid node type: %s", i, action.Node.Type)
			}
		}

		// 对于 append_to_file / write_file action，验证必需字段
		if action.ActionType == "append_to_file" || action.ActionType == "write_file" {
			if action.FilePath == "" {
				return nil, fmt.Errorf("action %d (%s) missing file_path", i, action.ActionType)
			}
			if action.Content == "" {
				return nil, fmt.Errorf("action %d (%s) missing content", i, action.ActionType)
			}
		}
		if action.ActionType == "read_file" || action.ActionType == "list_dir" {
			if action.FilePath == "" {
				return nil, fmt.Errorf("action %d (%s) missing file_path", i, action.ActionType)
			}
		}
		if action.ActionType == "search" {
			if action.FilePath == "" {
				return nil, fmt.Errorf("action %d (search) missing file_path", i)
			}
			if action.Content == "" {
				return nil, fmt.Errorf("action %d (search) missing content (pattern)", i)
			}
		}
		if action.ActionType == "read_artifact" {
			if action.ArtifactID == "" {
				return nil, fmt.Errorf("action %d (read_artifact) missing artifact_id", i)
			}
		}
		if action.ActionType == "request_human_input" {
			if action.Question == "" {
				return nil, fmt.Errorf("action %d (request_human_input) missing question", i)
			}
		}
		if action.ActionType == "append_sibling_node" {
			if action.Node.ID == "" {
				return nil, fmt.Errorf("action %d (append_sibling_node) missing node.id", i)
			}
			switch action.Node.Type {
			case "Normal", "Leaf":
			case "":
				return nil, fmt.Errorf("action %d (append_sibling_node) missing node type", i)
			case "Loop":
				return nil, fmt.Errorf("action %d (append_sibling_node) Loop type has been removed", i)
			default:
				return nil, fmt.Errorf("action %d (append_sibling_node) invalid node type: %s", i, action.Node.Type)
			}
		}
		if action.ActionType == "query_memory" {
			validQueryTypes := map[string]bool{
				"sibling_handoffs": true,
				"ancestor_chain":   true,
				"pinned_artifacts": true,
				"recent_artifacts": true,
				"fts_artifacts":    true,
			}
			if !validQueryTypes[action.QueryType] {
				return nil, fmt.Errorf("action %d (query_memory) invalid query_type: %q", i, action.QueryType)
			}
		}
		if action.ActionType == "add_artifact" {
			if action.ArtifactName == "" {
				return nil, fmt.Errorf("action %d (add_artifact) missing artifact_name", i)
			}
			if action.Content == "" {
				return nil, fmt.Errorf("action %d (add_artifact) missing content", i)
			}
			if s := action.ArtifactScope; s != "" && s != "node" && s != "subtree" && s != "global" {
				return nil, fmt.Errorf("action %d (add_artifact) invalid scope %q (want node/subtree/global)", i, s)
			}
		}
		if action.ActionType == "modify_artifact" {
			if action.SupersedesArtifact == "" {
				return nil, fmt.Errorf("action %d (modify_artifact) missing supersedes (artifact id to replace)", i)
			}
			if action.Content == "" {
				return nil, fmt.Errorf("action %d (modify_artifact) missing content", i)
			}
		}
		if action.ActionType == "request_context" {
			if len(action.Needs) == 0 {
				return nil, fmt.Errorf("action %d (request_context) missing needs", i)
			}
			for j, n := range action.Needs {
				if n.Kind == "" {
					return nil, fmt.Errorf("action %d.needs[%d] missing kind", i, j)
				}
				if n.Query == "" {
					return nil, fmt.Errorf("action %d.needs[%d] missing query", i, j)
				}
			}
		}
		if action.ActionType == "create_node" {
			for j, ac := range action.Node.AcceptanceCriteria {
				if ac.Description == "" {
					return nil, fmt.Errorf("action %d.acceptance_criteria[%d] missing description", i, j)
				}
				switch ac.CheckType {
				case "testable", "manual", "llm_judge":
				case "":
					return nil, fmt.Errorf("action %d.acceptance_criteria[%d] missing check_type (testable/manual/llm_judge)", i, j)
				default:
					return nil, fmt.Errorf("action %d.acceptance_criteria[%d] invalid check_type %q", i, j, ac.CheckType)
				}
				if ac.CheckType == "testable" && ac.CheckCommand == "" {
					return nil, fmt.Errorf("action %d.acceptance_criteria[%d] testable check_type requires check_command", i, j)
				}
			}
		}
		if action.ActionType == "mark_complete" {
			for j, r := range action.AcceptanceResults {
				if r.CriterionID == "" {
					return nil, fmt.Errorf("action %d.acceptance_results[%d] missing criterion_id", i, j)
				}
			}
		}
		// mark_complete action 不需要 node 字段，所以不验证
	}

	return &resp, nil
}

// CleanJSONString 清理 JSON 字符串
func CleanJSONString(str string) string {
	cleaned := strings.TrimSpace(str)
	// 移除可能存在的 Markdown code block
	if strings.HasPrefix(cleaned, "```json") {
		cleaned = strings.TrimPrefix(cleaned, "```json")
		cleaned = strings.TrimSuffix(cleaned, "```")
	} else if strings.HasPrefix(cleaned, "```") {
		cleaned = strings.TrimPrefix(cleaned, "```")
		cleaned = strings.TrimSuffix(cleaned, "```")
	}
	return strings.TrimSpace(cleaned)
}

// ToTaskNode 将 NodeDTO 转换为系统内部的 TaskNode
func (n *NodeDTO) ToTaskNode() *tasknode.TaskNode {
	var typ tasknode.TaskType
	switch n.Type {
	case "Leaf":
		typ = tasknode.Leaf
	default:
		typ = tasknode.Normal
	}

	// 将单一的 information 字符串转换为 []string
	// 如果有需要，这里可以使用更复杂的分割逻辑
	infos := []string{}
	if n.Information != "" {
		infos = append(infos, n.Information)
	}

	node := tasknode.NewTaskNode(n.ID, n.Name, typ, infos)
	if n.Variables != nil {
		node.Variables = n.Variables
	}
	if n.Index != 0 {
		node.Index = n.Index
	}
	node.IsImportant = n.IsImportant

	// 转换验收标准
	for k, ac := range n.AcceptanceCriteria {
		id := ac.ID
		if id == "" {
			id = fmt.Sprintf("%s_ac_%d", n.ID, k+1)
		}
		node.AcceptanceCriteria = append(node.AcceptanceCriteria, tasknode.AcceptanceCriterion{
			ID:           id,
			Description:  ac.Description,
			Required:     ac.Required,
			CheckType:    tasknode.CheckType(ac.CheckType),
			CheckCommand: ac.CheckCommand,
			ExpectedExit: ac.ExpectedExit,
			SourceNodeID: n.ID,
		})
	}
	return node
}
