package runtime

import (
	"testing"

	"github.com/Steve65535/llmvm/pkg/tasknode"
)

// TestPositionRoleRoot：根节点角色应为 root，且不允许 append_sibling_node。
func TestPositionRoleRoot(t *testing.T) {
	root := tasknode.NewTaskNode("r", "Root", tasknode.Normal, []string{"goal"})
	pos := BuildPosition(root)
	if pos.Role != RoleRoot {
		t.Errorf("expected RoleRoot, got %s", pos.Role)
	}
	if err := CheckAuthority(pos, "append_sibling_node"); err == nil {
		t.Error("root must not be allowed to append sibling")
	}
	if err := CheckAuthority(pos, "create_node"); err != nil {
		t.Errorf("root should be allowed create_node, got %v", err)
	}
}

// TestPositionRoleExecutorCannotCreateChild：Leaf 角色 = executor，不允许 create_node。
func TestPositionRoleExecutorCannotCreateChild(t *testing.T) {
	root := tasknode.NewTaskNode("r", "Root", tasknode.Normal, []string{"goal"})
	leaf := tasknode.NewTaskNode("l", "Leaf", tasknode.Leaf, []string{"work"})
	root.AddChild(leaf)

	pos := BuildPosition(leaf)
	if pos.Role != RoleExecutor {
		t.Errorf("expected RoleExecutor, got %s", pos.Role)
	}
	if err := CheckAuthority(pos, "create_node"); err == nil {
		t.Error("executor must not be allowed to create_node")
	}
	if err := CheckAuthority(pos, "execute_command"); err != nil {
		t.Errorf("executor must be allowed execute_command, got %v", err)
	}
	if err := CheckAuthority(pos, "mark_complete"); err != nil {
		t.Errorf("executor must be allowed mark_complete, got %v", err)
	}
}

// TestPositionRolePlannerNormal：Normal 子节点 = planner，create / append 都允许。
func TestPositionRolePlannerNormal(t *testing.T) {
	root := tasknode.NewTaskNode("r", "Root", tasknode.Normal, []string{"goal"})
	planner := tasknode.NewTaskNode("p", "Planner", tasknode.Normal, []string{"work"})
	root.AddChild(planner)

	pos := BuildPosition(planner)
	if pos.Role != RolePlanner {
		t.Errorf("expected RolePlanner, got %s", pos.Role)
	}
	for _, action := range []string{"create_node", "append_sibling_node", "query_memory", "request_context"} {
		if err := CheckAuthority(pos, action); err != nil {
			t.Errorf("planner should allow %s, got %v", action, err)
		}
	}
}

// TestPositionConstraintsIncludeRequiredAcceptance：节点带 Required 验收时，
// constraints 列表应明确告知模型。
func TestPositionConstraintsIncludeRequiredAcceptance(t *testing.T) {
	root := tasknode.NewTaskNode("r", "Root", tasknode.Normal, nil)
	leaf := tasknode.NewTaskNode("l", "Leaf", tasknode.Leaf, nil)
	leaf.AcceptanceCriteria = []tasknode.AcceptanceCriterion{
		{ID: "ac1", Description: "tests pass", Required: true, CheckType: tasknode.CheckTypeTestable},
		{ID: "ac2", Description: "doc updated", Required: false, CheckType: tasknode.CheckTypeManual},
	}
	root.AddChild(leaf)

	pos := BuildPosition(leaf)
	hit := false
	for _, c := range pos.Constraints {
		if contains(c, "1 required acceptance") {
			hit = true
		}
	}
	if !hit {
		t.Errorf("expected constraint mentioning 1 required acceptance, got %v", pos.Constraints)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || (len(s) > len(sub) && (s[:len(sub)] == sub || contains(s[1:], sub))))
}
