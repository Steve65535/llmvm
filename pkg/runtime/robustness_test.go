package runtime

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Steve65535/llmvm/pkg/llm"
	"github.com/Steve65535/llmvm/pkg/tasknode"
)

// MockRetryEngine 模拟前几次调用失败，最后一次成功的引擎
type MockRetryEngine struct {
	FailCount int
	Current   int
}

func (m *MockRetryEngine) Call(prompt string) (*llm.Output, error) {
	m.Current++
	if m.Current <= m.FailCount {
		// 返回损坏的 JSON 或错误的动作
		if m.Current == 1 {
			return &llm.Output{Response: "This is not JSON"}, nil
		}
		return &llm.Output{Response: `{"actions": [{"action_type": "invalid_action"}]}`}, nil
	}

	// 最后一次返回成功的 JSON
	resp := llm.Response{
		Actions: []llm.Action{
			{
				ActionType: "mark_complete",
			},
		},
	}
	data, _ := json.Marshal(resp)

	// 验证 prompt 是否包含错误消息
	if m.FailCount > 0 && !strings.Contains(prompt, "Previous Attempt Failed") {
		return nil, fmt.Errorf("prompt should contain error feedback on retry")
	}

	return &llm.Output{Response: string(data)}, nil
}

func (m *MockRetryEngine) CallAsync(prompt string) <-chan *llm.Output {
	ch := make(chan *llm.Output, 1)
	out, _ := m.Call(prompt)
	ch <- out
	close(ch)
	return ch
}

func TestRuntimeRetryLogic(t *testing.T) {
	root := tasknode.NewTaskNode("root", "Root", tasknode.Leaf, []string{"Task"})
	engine := &MockRetryEngine{FailCount: 2} // 前两次失败
	r := NewRuntime(engine, root)

	err := r.Execute("Do something")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if engine.Current != 3 {
		t.Errorf("Expected 3 calls (2 fails + 1 success), got %d", engine.Current)
	}

	if !root.IsCompleted() {
		t.Errorf("Root node should be completed")
	}
}

func TestRuntimeMaxRetriesReached(t *testing.T) {
	root := tasknode.NewTaskNode("root", "Root", tasknode.Leaf, []string{"Task"})
	engine := &MockRetryEngine{FailCount: 20} // 超过 maxRetries (3)
	r := NewRuntime(engine, root)

	err := r.Execute("Do something")
	if err == nil {
		t.Fatal("Expected error due to max retries, got nil")
	}

	if !strings.Contains(err.Error(), "maximum retries") {
		t.Errorf("Expected maximum retries error, got: %v", err)
	}
}
