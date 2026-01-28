package runtime

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Steve65535/llmvm/pkg/llm"
	"github.com/Steve65535/llmvm/pkg/tasknode"
)

func mustMarshal(v interface{}) string {
	data, _ := json.Marshal(v)
	return string(data)
}

// MockAdvancedEngine 用于测试全局注意力和 CLI
type MockAdvancedEngine struct {
	Step       int
	LastPrompt string
}

func (m *MockAdvancedEngine) Call(prompt string) (*llm.Output, error) {
	m.Step++
	m.LastPrompt = prompt

	var action llm.Action
	switch m.Step {
	case 1:
		// 步骤 1: 执行一个写命令，然后创建一个任务节点
		action = llm.Action{
			ActionType: "execute_command",
			Command:    "write test_advanced.txt Hello_Global_Attention",
		}
		// 我们返回两个动作：执行命令和创建节点
		resp := llm.Response{
			Actions: []llm.Action{
				action,
				{
					ActionType: "create_node",
					Node: llm.NodeDTO{
						ID:          "checker",
						Name:        "Check File",
						Type:        "Leaf",
						Information: "Check the content of the file we just wrote",
					},
				},
			},
		}
		return &llm.Output{Response: mustMarshal(resp)}, nil
	case 2:
		// 步骤 2: 在 Checker 节点，执行完成并带上结果
		action = llm.Action{
			ActionType: "mark_complete",
			Result:     "File content verified as valid",
		}
		resp := llm.Response{Actions: []llm.Action{action}}
		return &llm.Output{Response: mustMarshal(resp)}, nil
	case 3:
		// 步骤 3: 回到根节点，检查历史记录是否包含 Checker 的结果
		action = llm.Action{
			ActionType: "mark_complete",
			Result:     "All advanced features verified",
		}
		resp := llm.Response{Actions: []llm.Action{action}}
		return &llm.Output{Response: mustMarshal(resp)}, nil
	}

	return &llm.Output{Response: `{"actions": []}`}, nil
}

func (m *MockAdvancedEngine) CallAsync(prompt string) <-chan *llm.Output {
	return nil
}

func TestAdvancedFeatures(t *testing.T) {
	// 清理测试文件
	os.Remove("test_advanced.txt")
	defer os.Remove("test_advanced.txt")

	engine := &MockAdvancedEngine{}
	root := tasknode.NewTaskNode("root", "Root Task", tasknode.Normal, []string{"Verify File Ops and Attention"})
	r := NewRuntime(engine, root)

	err := r.Execute("Initial Request")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// 验证文件是否被创建
	if _, err := os.Stat("test_advanced.txt"); os.IsNotExist(err) {
		t.Errorf("File test_advanced.txt was not created by VFS")
	}

	// 确认历史记录被正确格式化并注入 Prompt
	// 在第 3 步（回到根节点处理下一步时，其实已经结束了，但我们可以通过最后一次 Call 的 Prompt 检查）
	// 注意：Execute 循环在 cursor.Done() 为真时退出。
	// 第 2 步 Checker 完成后，MoveUp 回到 Root。Root 遍历完后 MoveUp 设为 nil。

	if !strings.Contains(engine.LastPrompt, "File content verified as valid") {
		t.Errorf("Prompt did not contain historical result from Checker node")
	}

	if !strings.Contains(engine.LastPrompt, "Global Attention") {
		t.Errorf("Prompt did not contain Global Attention section")
	}
}
