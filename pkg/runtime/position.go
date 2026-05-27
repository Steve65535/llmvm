package runtime

import (
	"fmt"
	"strings"

	"github.com/Steve65535/llmvm/pkg/tasknode"
)

// PositionRole 描述节点在任务树中的角色。
//
// 角色由 BuildPosition 从节点类型 + 树位置 + 是否为 ErrorHandler 推导，runtime 据此
// 决定权限和默认上下文需求。
type PositionRole string

const (
	RoleRoot         PositionRole = "root"
	RolePlanner      PositionRole = "planner"
	RoleExecutor     PositionRole = "executor"
	RoleErrorHandler PositionRole = "error_handler"
)

// AuthorityDescriptor 描述节点的动作权限。Runtime 在 ExecuteAction 入口做 fail-fast 校验。
type AuthorityDescriptor struct {
	CanCreateChild       bool
	CanAppendSibling     bool
	CanWriteFile         bool
	CanExecuteCommand    bool
	CanQueryMemory       bool
	CanRequestHumanInput bool
	CanMarkComplete      bool
	CanAddArtifact       bool
	CanModifyArtifact    bool
	CanRequestContext    bool
}

// ScopeDescriptor 描述节点默认应该看到哪些范围的上下文。
type ScopeDescriptor struct {
	IncludeAncestorChain   bool
	IncludeSiblingHandoffs bool
	IncludeArtifactIndex   bool
	IncludeFailureHistory  bool
	DefaultArtifactScope   string // node / subtree / global
}

// Position 是当前节点的运行时位置摘要：在哪里、扮演什么角色、能做什么、看见什么。
//
// Position 不持久化，每次节点激活时 BuildPosition 重新推导。它的目的是把"位置"从
// 散落在 prompt 模板里的字段升格为 runtime 一等对象，prompt 生成、authority 校验、
// retrieval 计划都从 Position 出发。
type Position struct {
	NodeID       string
	ParentID     string
	Depth        int
	Path         []string
	SiblingIndex int
	NodeType     tasknode.TaskType

	Role        PositionRole
	Scope       ScopeDescriptor
	Authority   AuthorityDescriptor
	Constraints []string
}

// BuildPosition 从 TaskNode 推导 Position。
func BuildPosition(node *tasknode.TaskNode) Position {
	pos := Position{
		NodeID:   node.ID,
		NodeType: node.Type,
	}

	// 路径 + Depth + SiblingIndex
	depth := 0
	cur := node
	var path []string
	for cur != nil {
		path = append([]string{cur.Name}, path...)
		if cur.Parent != nil {
			depth++
		}
		cur = cur.Parent
	}
	pos.Path = path
	pos.Depth = depth

	if node.Parent != nil {
		pos.ParentID = node.Parent.ID
		for i, sib := range node.Parent.Children {
			if sib.ID == node.ID {
				pos.SiblingIndex = i
				break
			}
		}
	}

	// Role 推导
	switch {
	case node.Parent == nil:
		pos.Role = RoleRoot
	case isErrorHandlerOf(node):
		pos.Role = RoleErrorHandler
	case node.Type == tasknode.Leaf:
		pos.Role = RoleExecutor
	default:
		pos.Role = RolePlanner
	}

	pos.Authority = authorityFor(pos.Role)
	pos.Scope = scopeFor(pos.Role)
	pos.Constraints = constraintsFor(node, pos.Role)
	return pos
}

func isErrorHandlerOf(node *tasknode.TaskNode) bool {
	if node == nil || node.Parent == nil {
		return false
	}
	return node.Parent.ErrorHandler != nil && node.Parent.ErrorHandler.ID == node.ID
}

// authorityFor 返回某 Role 的默认权限。
//
// Root: 顶层规划，可创建子节点 / 请求人类，但不能 append sibling（无父级）
// Planner: 协调节点，可拆分 / append sibling / 查询记忆
// Executor: 叶子，可工具调用 + add_artifact，不允许 create child
// ErrorHandler: 恢复节点，权限同 Executor 但默认开 query_memory + request_context
func authorityFor(role PositionRole) AuthorityDescriptor {
	switch role {
	case RoleRoot:
		return AuthorityDescriptor{
			CanCreateChild:       true,
			CanAppendSibling:     false,
			CanWriteFile:         true,
			CanExecuteCommand:    true,
			CanQueryMemory:       true,
			CanRequestHumanInput: true,
			CanMarkComplete:      true,
			CanAddArtifact:       true,
			CanModifyArtifact:    true,
			CanRequestContext:    true,
		}
	case RolePlanner:
		return AuthorityDescriptor{
			CanCreateChild:       true,
			CanAppendSibling:     true,
			CanWriteFile:         true,
			CanExecuteCommand:    true,
			CanQueryMemory:       true,
			CanRequestHumanInput: true,
			CanMarkComplete:      true,
			CanAddArtifact:       true,
			CanModifyArtifact:    true,
			CanRequestContext:    true,
		}
	case RoleExecutor:
		return AuthorityDescriptor{
			CanCreateChild:       false,
			CanAppendSibling:     true,
			CanWriteFile:         true,
			CanExecuteCommand:    true,
			CanQueryMemory:       true,
			CanRequestHumanInput: true,
			CanMarkComplete:      true,
			CanAddArtifact:       true,
			CanModifyArtifact:    true,
			CanRequestContext:    true,
		}
	case RoleErrorHandler:
		return AuthorityDescriptor{
			CanCreateChild:       true,
			CanAppendSibling:     false,
			CanWriteFile:         true,
			CanExecuteCommand:    true,
			CanQueryMemory:       true,
			CanRequestHumanInput: true,
			CanMarkComplete:      true,
			CanAddArtifact:       true,
			CanModifyArtifact:    true,
			CanRequestContext:    true,
		}
	}
	return AuthorityDescriptor{CanMarkComplete: true}
}

func scopeFor(role PositionRole) ScopeDescriptor {
	switch role {
	case RoleRoot:
		return ScopeDescriptor{
			IncludeAncestorChain:   false,
			IncludeSiblingHandoffs: false,
			IncludeArtifactIndex:   true,
			IncludeFailureHistory:  true,
			DefaultArtifactScope:   "global",
		}
	case RolePlanner:
		return ScopeDescriptor{
			IncludeAncestorChain:   true,
			IncludeSiblingHandoffs: true,
			IncludeArtifactIndex:   true,
			IncludeFailureHistory:  true,
			DefaultArtifactScope:   "subtree",
		}
	case RoleExecutor:
		return ScopeDescriptor{
			IncludeAncestorChain:   true,
			IncludeSiblingHandoffs: true,
			IncludeArtifactIndex:   true,
			IncludeFailureHistory:  false,
			DefaultArtifactScope:   "node",
		}
	case RoleErrorHandler:
		return ScopeDescriptor{
			IncludeAncestorChain:   true,
			IncludeSiblingHandoffs: true,
			IncludeArtifactIndex:   true,
			IncludeFailureHistory:  true,
			DefaultArtifactScope:   "subtree",
		}
	}
	return ScopeDescriptor{}
}

func constraintsFor(node *tasknode.TaskNode, role PositionRole) []string {
	var out []string
	if role == RoleExecutor {
		out = append(out, "Cannot create child nodes; if work needs decomposition, append a sibling planner.")
	}
	if role == RoleRoot {
		out = append(out, "Cannot append sibling: this is the root.")
	}
	if len(node.AcceptanceCriteria) > 0 {
		var required int
		for _, c := range node.AcceptanceCriteria {
			if c.Required {
				required++
			}
		}
		if required > 0 {
			out = append(out, fmt.Sprintf("Must satisfy %d required acceptance criteria before mark_complete.", required))
		}
	}
	return out
}

// FormatPositionSummary 把 Position 转为 prompt 友好的字符串。
func FormatPositionSummary(pos Position) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "## Position\n")
	fmt.Fprintf(&sb, "- node_id: %s\n", pos.NodeID)
	fmt.Fprintf(&sb, "- role: %s\n", pos.Role)
	fmt.Fprintf(&sb, "- depth: %d, sibling_index: %d\n", pos.Depth, pos.SiblingIndex)
	if len(pos.Path) > 0 {
		fmt.Fprintf(&sb, "- path: %s\n", strings.Join(pos.Path, " > "))
	}
	if len(pos.Constraints) > 0 {
		sb.WriteString("- constraints:\n")
		for _, c := range pos.Constraints {
			fmt.Fprintf(&sb, "  - %s\n", c)
		}
	}
	return sb.String()
}

// FormatAllowedActions 列出当前位置允许的 action_type。
func FormatAllowedActions(pos Position) string {
	var allowed []string
	a := pos.Authority
	if a.CanCreateChild {
		allowed = append(allowed, "create_node")
	}
	if a.CanAppendSibling {
		allowed = append(allowed, "append_sibling_node")
	}
	if a.CanWriteFile {
		allowed = append(allowed, "write_file", "append_to_file", "read_file", "list_dir", "search")
	}
	if a.CanExecuteCommand {
		allowed = append(allowed, "execute_command")
	}
	if a.CanQueryMemory {
		allowed = append(allowed, "query_memory")
	}
	if a.CanRequestHumanInput {
		allowed = append(allowed, "request_human_input")
	}
	if a.CanMarkComplete {
		allowed = append(allowed, "mark_complete", "update_variables", "read_artifact")
	}
	if a.CanAddArtifact {
		allowed = append(allowed, "add_artifact")
	}
	if a.CanModifyArtifact {
		allowed = append(allowed, "modify_artifact")
	}
	if a.CanRequestContext {
		allowed = append(allowed, "request_context")
	}
	return "## Allowed Actions\n" + strings.Join(allowed, ", ") + "\n"
}

// CheckAuthority 在 ExecuteAction 入口做 fail-fast 校验。
//
// 返回 nil 表示允许；非 nil 时 ExecuteAction 应直接返回该错误，避免越权动作落地。
func CheckAuthority(pos Position, actionType string) error {
	a := pos.Authority
	allowed := true
	switch actionType {
	case "create_node":
		allowed = a.CanCreateChild
	case "append_sibling_node":
		allowed = a.CanAppendSibling
	case "write_file", "append_to_file":
		allowed = a.CanWriteFile
	case "read_file", "list_dir", "search", "read_artifact":
		allowed = a.CanWriteFile || a.CanExecuteCommand // 读类工具放宽：能写就能读
	case "execute_command":
		allowed = a.CanExecuteCommand
	case "query_memory":
		allowed = a.CanQueryMemory
	case "request_human_input":
		allowed = a.CanRequestHumanInput
	case "mark_complete", "update_variables", "shutdown":
		allowed = a.CanMarkComplete
	case "add_artifact":
		allowed = a.CanAddArtifact
	case "modify_artifact":
		allowed = a.CanModifyArtifact
	case "request_context":
		allowed = a.CanRequestContext
	}
	if !allowed {
		return fmt.Errorf("authority denied: role=%s cannot %s (use a different node or escalate)",
			pos.Role, actionType)
	}
	return nil
}
