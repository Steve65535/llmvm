package runtime

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/Steve65535/llmvm/pkg/llm"
	"github.com/Steve65535/llmvm/pkg/tasknode"
)

func TestNestedLoopTraversal(t *testing.T) {
	// Root -> OuterLoop -> InnerLoop -> Leaf

	root := tasknode.NewTaskNode("root", "Root", tasknode.Normal, []string{"Perform nested loops"})

	engine := &MockLLMNestedLoop{}
	rt := NewRuntime(engine, root)

	err := rt.Execute("Run nested loop test")
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	if !root.WetherFinished {
		t.Errorf("Root not finished")
	}
}

type MockLLMNestedLoop struct {
	outerCount int
	innerCount int
}

func (m *MockLLMNestedLoop) Call(prompt string) (*llm.Output, error) {
	// Extremely specific matching by verifying the ID is immediately after the "current_info" tag
	// Our JSON output has 8 spaces of indentation in the prompt

	if strings.Contains(prompt, "\"current_info\": {\n        \"id\": \"root\"") {
		return &llm.Output{Response: `{"actions": [{"action_type": "create_node", "node": {"id": "outer", "name": "Outer Loop", "type": "Loop", "information": "Outer"}}]}`}, nil
	}

	if strings.Contains(prompt, "\"current_info\": {\n        \"id\": \"outer\"") {
		m.outerCount++
		if m.outerCount <= 2 {
			id := "inner_" + strconv.Itoa(m.outerCount)
			return &llm.Output{Response: fmt.Sprintf(`{"actions": [{"action_type": "create_node", "node": {"id": "%s", "name": "Inner Loop", "type": "Loop", "information": "Inner"}}]}`, id)}, nil
		}
		return &llm.Output{Response: `{"actions": [{"action_type": "mark_complete"}]}`}, nil
	}

	if strings.Contains(prompt, "\"current_info\": {") && strings.Contains(prompt, "inner_") {
		m.innerCount++
		if m.innerCount <= 4 {
			return &llm.Output{Response: `{"actions": [{"action_type": "create_node", "node": {"id": "leaf", "name": "Leaf", "type": "Leaf", "information": "Work"}}]}`}, nil
		}
		return &llm.Output{Response: `{"actions": [{"action_type": "mark_complete"}]}`}, nil
	}

	if strings.Contains(prompt, "\"current_info\": {\n        \"id\": \"leaf\"") {
		return &llm.Output{Response: `{"actions": [{"action_type": "mark_complete"}]}`}, nil
	}

	return &llm.Output{Response: `{"actions": [{"action_type": "mark_complete"}]}`}, nil
}

func (m *MockLLMNestedLoop) CallAsync(prompt string) <-chan *llm.Output {
	ch := make(chan *llm.Output, 1)
	out, _ := m.Call(prompt)
	ch <- out
	close(ch)
	return ch
}
