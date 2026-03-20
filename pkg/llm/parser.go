package llm

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Steve65535/llmvm/pkg/tasknode"
)

// NodeDTO 对应 response.json 中的 node 结构
type NodeDTO struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Type        string                 `json:"type"` // "Normal", "Loop", "Leaf"
	Information string                 `json:"information"`
	Variables   map[string]interface{} `json:"variables,omitempty"`
	Index       int                    `json:"index,omitempty"`
	IsImportant bool                   `json:"is_important,omitempty"`

	// 🆕 新增：错误处理
	ErrorHandlerID string `json:"error_handler_id,omitempty"`
	MaxRetries     int    `json:"max_retries,omitempty"`
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
	Summary      string   `json:"summary,omitempty"`
	KeyFacts     []string `json:"key_facts,omitempty"`
	ArtifactRefs []string `json:"artifact_refs,omitempty"`
	Handoff      string   `json:"handoff,omitempty"`

	// read_artifact 分片读取
	ArtifactID string `json:"artifact_id,omitempty"`
	StartLine  int    `json:"start_line,omitempty"`
	EndLine    int    `json:"end_line,omitempty"`
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
			case "Normal", "Loop", "Leaf":
				// valid
			case "":
				return nil, fmt.Errorf("action %d (create_node) missing node type", i)
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
	case "Loop":
		typ = tasknode.Loop
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
	return node
}
