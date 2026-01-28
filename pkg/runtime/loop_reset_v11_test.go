package runtime

import (
	"strings"
	"testing"

	"github.com/Steve65535/llmvm/pkg/llm"
	"github.com/Steve65535/llmvm/pkg/tasknode"
)

func TestLoopResetStatus(t *testing.T) {
	// Root -> LoopNode -> Leaf1
	// Pass 1: Leaf1 processed, but DOES NOT mark complete. Loop should reset.
	// Pass 2: Leaf1 processed, MARKS complete. Loop should finish.

	root := tasknode.NewTaskNode("root", "Root", tasknode.Normal, []string{"Loop test"})
	engine := &MockLLMLoopReset{}
	rt := NewRuntime(engine, root)

	err := rt.Execute("Run loop reset test")
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	if engine.leafCallCount != 2 {
		t.Errorf("Leaf1 should have been called twice (one for reset), but was called %d times", engine.leafCallCount)
	}

	if !root.WetherFinished {
		t.Errorf("Root should be finished after loop completes")
	}
}

type MockLLMLoopReset struct {
	leafCallCount int
}

func (m *MockLLMLoopReset) Call(prompt string) (*llm.Output, error) {
	if strings.Contains(prompt, "\"id\": \"root\"") {
		return &llm.Output{Response: `{"actions": [{"action_type": "create_node", "node": {"id": "loop1", "name": "Loop Node", "type": "Loop", "information": "Loop"}}]}`}, nil
	}

	if strings.Contains(prompt, "\"current_info\": {\n        \"id\": \"loop1\"") {
		// Only create the leaf if it doesn't exist yet.
		// In a real scenario, the LLM might see the existing child.
		if !strings.Contains(prompt, "leaf1") {
			return &llm.Output{Response: `{"actions": [{"action_type": "create_node", "node": {"id": "leaf1", "name": "Leaf 1", "type": "Leaf", "information": "Work"}}]}`}, nil
		}
		return &llm.Output{Response: `{"actions": []}`}, nil
	}

	if strings.Contains(prompt, "\"id\": \"leaf1\"") {
		m.leafCallCount++
		if m.leafCallCount == 1 {
			// First pass: Don't mark complete. Just update some variable to show progress.
			return &llm.Output{Response: `{"actions": [{"action_type": "update_variables", "variables": {"progress": "half"}}]}`}, nil
		}
		// Second pass: Mark complete.
		return &llm.Output{Response: `{"actions": [{"action_type": "mark_complete"}]}`}, nil
	}

	return &llm.Output{Response: `{"actions": [{"action_type": "mark_complete"}]}`}, nil
}

func (m *MockLLMLoopReset) CallAsync(prompt string) <-chan *llm.Output {
	ch := make(chan *llm.Output, 1)
	out, _ := m.Call(prompt)
	ch <- out
	close(ch)
	return ch
}
