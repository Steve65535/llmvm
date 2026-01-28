package runtime

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Steve65535/llmvm/pkg/llm"
	"github.com/Steve65535/llmvm/pkg/tasknode"
)

// MockLLMTwoPass 模拟支持两步式注意力的 LLM
type MockLLMTwoPass struct {
	Step        int
	SelectedIDs []string
}

func (m *MockLLMTwoPass) Call(prompt string) (*llm.Output, error) {
	// 判断是第一步还是第二步
	if strings.Contains(prompt, "Attention Selector") {
		fmt.Printf("\n--- [Selector Pass] ---\n")
		// 模拟选择逻辑
		if strings.Contains(prompt, "Goal: Run cross-branch test") {
			if m.Step >= 2 {
				return &llm.Output{Response: "branch_a"}, nil
			}
		}
		return &llm.Output{Response: "none"}, nil
	}

	fmt.Printf("\n--- [Execution Pass] ---\n")
	m.Step++
	switch m.Step {
	case 1:
		// 创建两个分支
		return &llm.Output{
			Response: `{"actions": [
				{"action_type": "create_node", "node": {"id": "branch_a", "name": "Branch A", "type": "Leaf", "information": "Store data"}},
				{"action_type": "create_node", "node": {"id": "branch_b", "name": "Branch B", "type": "Leaf", "information": "Read data"}}
			]}`,
		}, nil
	case 2:
		// 执行 Branch A：设置变量
		return &llm.Output{
			Response: `{"actions": [
				{"action_type": "update_variables", "variables": {"shared_key": "v6_val"}},
				{"action_type": "mark_complete", "result": "Key set in Branch A"}
			]}`,
		}, nil
	case 3:
		// 执行 Branch B：尝试读取
		// 验证 Prompt 中是否包含 shared_key (由于 Selector 选中了 branch_a)
		if strings.Contains(prompt, "v6_val") {
			fmt.Println("  ✅ RAM hit: shared_key found in workspace!")
		} else {
			fmt.Println("  ❌ RAM miss: shared_key NOT found in workspace!")
		}
		return &llm.Output{
			Response: `{"actions": [{"action_type": "mark_complete", "result": "Success"}]}`,
		}, nil
	default:
		return &llm.Output{
			Response: `{"actions": [{"action_type": "mark_complete", "result": "Done"}]}`,
		}, nil
	}
}

func (m *MockLLMTwoPass) CallAsync(prompt string) <-chan *llm.Output {
	ch := make(chan *llm.Output, 1)
	out, _ := m.Call(prompt)
	ch <- out
	close(ch)
	return ch
}

func TestTwoPassAttention(t *testing.T) {
	root := tasknode.NewTaskNode("root", "Root", tasknode.Normal, []string{"Root task"})
	mockLLM := &MockLLMTwoPass{}
	rt := NewRuntime(mockLLM, root)

	err := rt.Execute("Run cross-branch test")
	if err != nil {
		t.Fatalf("Runtime failed: %v", err)
	}

	fmt.Println("\nFinal Global State Check:")
	fmt.Println(rt.formatGlobalWorkspace([]string{"branch_a", "branch_b"}))
}
