package runtime

import (
	"testing"

	"github.com/Steve65535/llmvm/pkg/llm"
	"github.com/Steve65535/llmvm/pkg/tasknode"
)

func TestVariablePropagation(t *testing.T) {
	root := tasknode.NewTaskNode("root", "Root", tasknode.Normal, []string{"Root task"})
	root.Variables["global"] = "v1"

	child := tasknode.NewTaskNode("child", "Child", tasknode.Normal, []string{"Child task"})
	child.Variables["local"] = "v2"
	root.AddChild(child)

	engine := &llm.StubEngine{}
	r := NewRuntime(engine, root)

	// Collect variables for child
	vars := r.collectScopedVariables(child)

	if vars["global"] != "v1" {
		t.Errorf("Expected global=v1, got %v", vars["global"])
	}
	if vars["local"] != "v2" {
		t.Errorf("Expected local=v2, got %v", vars["local"])
	}
}

func TestVariableOverride(t *testing.T) {
	root := tasknode.NewTaskNode("root", "Root", tasknode.Normal, []string{"Root task"})
	root.Variables["key"] = "parent_val"

	child := tasknode.NewTaskNode("child", "Child", tasknode.Normal, []string{"Child task"})
	child.Variables["key"] = "child_val"
	root.AddChild(child)

	engine := &llm.StubEngine{}
	r := NewRuntime(engine, root)

	vars := r.collectScopedVariables(child)

	if vars["key"] != "child_val" {
		t.Errorf("Expected key=child_val (override), got %v", vars["key"])
	}
}

func TestExecuteActionUpdateVariables(t *testing.T) {
	root := tasknode.NewTaskNode("root", "Root", tasknode.Normal, []string{"Root task"})
	engine := &llm.StubEngine{}
	r := NewRuntime(engine, root)

	action := llm.Action{
		ActionType: "update_variables",
		Variables: map[string]interface{}{
			"new_var": "val1",
		},
	}

	err := r.executeAction(action, root)
	if err != nil {
		t.Fatalf("executeAction failed: %v", err)
	}

	if root.Variables["new_var"] != "val1" {
		t.Errorf("Expected new_var=val1, got %v", root.Variables["new_var"])
	}
}
