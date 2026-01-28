package runtime

import (
	"strings"
	"testing"

	"github.com/Steve65535/llmvm/pkg/llm"
	"github.com/Steve65535/llmvm/pkg/tasknode"
)

func TestHierarchicalANDCompletion(t *testing.T) {
	// Root (Normal) -> Child1 (Normal) -> Leaf1

	root := tasknode.NewTaskNode("root", "Root", tasknode.Normal, []string{"Top level"})
	engine := &MockLLMANDLogic{}
	rt := NewRuntime(engine, root)

	err := rt.Execute("Run AND logic test")
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	if !root.WetherFinished {
		t.Errorf("Root should be finished via AND inheritance")
	}

	if len(root.Children) == 0 {
		t.Fatalf("Root has no children")
	}
	child1 := root.Children[0]
	if !child1.WetherFinished {
		t.Errorf("Child1 should be finished via AND inheritance from Leaf1")
	}
}

type MockLLMANDLogic struct{}

func (m *MockLLMANDLogic) Call(prompt string) (*llm.Output, error) {
	// Look for the specific ID inside current_info block (with indentation)
	if strings.Contains(prompt, "\"current_info\": {\n        \"id\": \"root\"") {
		return &llm.Output{Response: `{"actions": [{"action_type": "create_node", "node": {"id": "child1", "name": "Child 1", "type": "Normal", "information": "Mid"}}]}`}, nil
	}

	if strings.Contains(prompt, "\"current_info\": {\n        \"id\": \"child1\"") {
		return &llm.Output{Response: `{"actions": [{"action_type": "create_node", "node": {"id": "leaf1", "name": "Leaf 1", "type": "Leaf", "information": "Work"}}]}`}, nil
	}

	if strings.Contains(prompt, "\"current_info\": {\n        \"id\": \"leaf1\"") {
		return &llm.Output{Response: `{"actions": [{"action_type": "mark_complete"}]}`}, nil
	}

	return &llm.Output{Response: `{"actions": [{"action_type": "mark_complete"}]}`}, nil
}

func (m *MockLLMANDLogic) CallAsync(prompt string) <-chan *llm.Output {
	ch := make(chan *llm.Output, 1)
	out, _ := m.Call(prompt)
	ch <- out
	close(ch)
	return ch
}
