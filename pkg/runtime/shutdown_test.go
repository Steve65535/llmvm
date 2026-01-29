package runtime

import (
	"strings"
	"testing"

	"github.com/Steve65535/llmvm/pkg/llm"
	"github.com/Steve65535/llmvm/pkg/tasknode"
)

func TestEmergencyShutdown(t *testing.T) {
	root := tasknode.NewTaskNode("root", "Root", tasknode.Normal, []string{"Kill me"})
	engine := &MockShutdownEngine{}
	rt := NewRuntime(engine, root)

	err := rt.Execute("Shutdown test")
	if err == nil {
		t.Fatalf("Expected error, got nil")
	}

	if !strings.Contains(err.Error(), "EMERGENCY_SHUTDOWN") {
		t.Errorf("Expected EMERGENCY_SHUTDOWN error, got: %v", err)
	}
}

type MockShutdownEngine struct{}

func (m *MockShutdownEngine) Call(prompt string) (*llm.Output, error) {
	return &llm.Output{Response: `{"actions": [{"action_type": "shutdown", "result": "Test shutdown"}]}`}, nil
}

func (m *MockShutdownEngine) CallAsync(prompt string) <-chan *llm.Output {
	return nil
}
