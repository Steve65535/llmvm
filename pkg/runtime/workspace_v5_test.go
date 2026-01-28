package runtime

import (
	"fmt"
	"testing"

	"github.com/Steve65535/llmvm/pkg/llm"
	"github.com/Steve65535/llmvm/pkg/tasknode"
)

// MockLLMWorkspace 模拟一个理解 v5 Workspace 的 LLM
type MockLLMWorkspace struct {
	Step int
}

func (m *MockLLMWorkspace) Call(prompt string) (*llm.Output, error) {
	m.Step++
	fmt.Printf("\n--- Step %d Prompt Snippet ---\n", m.Step)
	// 在测试中打印 Workspace 部分以便观察
	if m.Step > 1 {
		// 寻找 Global Workspace 关键词并打印
	}

	switch m.Step {
	case 1:
		// 步骤 1：在分支 A 中设置一个重要变量并标记完成
		return &llm.Output{
			Response: `{"actions": [
				{"action_type": "create_node", "node": {"id": "branch_a", "name": "Branch A", "type": "Leaf", "information": "Store data"}},
				{"action_type": "create_node", "node": {"id": "branch_b", "name": "Branch B", "type": "Leaf", "information": "Read data"}}
			]}`,
		}, nil
	case 2:
		// 执行 Branch A
		return &llm.Output{
			Response: `{"actions": [
				{"action_type": "update_variables", "variables": {"secret_key": "v5_works"}, "is_important": true},
				{"action_type": "mark_complete", "result": "Stored key in workspace"}
			]}`,
		}, nil
	case 3:
		// 执行 Branch B
		// 这里应该能在 Global Workspace 中看到 Branch A 的 secret_key
		return &llm.Output{
			Response: `{"actions": [
				{"action_type": "mark_complete", "result": "Successfully read secret_key from Workspace"}
			]}`,
		}, nil
	default:
		return &llm.Output{
			Response: `{"actions": [{"action_type": "mark_complete", "result": "Done"}]}`,
		}, nil
	}
}

func (m *MockLLMWorkspace) CallAsync(prompt string) <-chan *llm.Output {
	ch := make(chan *llm.Output, 1)
	out, _ := m.Call(prompt)
	ch <- out
	close(ch)
	return ch
}

func TestGlobalWorkspace(t *testing.T) {
	root := tasknode.NewTaskNode("root", "Root", tasknode.Normal, []string{"Root task"})
	mockLLM := &MockLLMWorkspace{}
	rt := NewRuntime(mockLLM, root)

	err := rt.Execute("Run cross-branch test")
	if err != nil {
		t.Fatalf("Runtime failed: %v", err)
	}

	// 验证最终状态
	// 虽然是一个集成测试，但我们可以打印出整棵树的感知情况
	fmt.Println("\nFinal Global Workspace State:")
	fmt.Println(rt.formatHistory())
}
