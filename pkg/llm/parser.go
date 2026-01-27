package llm

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Steve65535/llmvm/pkg/tasknode"
)

// NodeDTO 对应 response.json 中的 node 结构
type NodeDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"` // "Normal", "Loop", "Leaf"
	Information string `json:"information"`
}

// NodeState 对应 request.json 中的 parent_info/current_info
// 比 NodeDTO 多了 Status 字段
type NodeState struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Status      string `json:"status"`
	Information string `json:"information"`
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
	ActionType string  `json:"action_type"`
	Node       NodeDTO `json:"node"`
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

	return tasknode.NewTaskNode(n.ID, n.Name, typ, infos)
}
