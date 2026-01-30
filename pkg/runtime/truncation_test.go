package runtime

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Steve65535/llmvm/pkg/llm"
	"github.com/Steve65535/llmvm/pkg/tasknode"
)

func TestTruncationLogic(t *testing.T) {
	// Initialize runtime with stub engine
	engine := &llm.StubEngine{}
	root := tasknode.NewTaskNode("root", "Root", tasknode.Normal, []string{"test"})
	rt := NewRuntime(engine, root)

	// Test executeAction's truncation
	// Use a command that definitely produces large output
	largeCmd := "printf 'B%.0s' $(seq 1 6000) || head -c 6000 /dev/zero | tr '\\0' 'B'"
	actionLarge := llm.Action{
		ActionType: "execute_command",
		Command:    largeCmd,
	}

	err := rt.executeAction(actionLarge, root)
	if err != nil {
		t.Fatalf("executeAction large failed: %v", err)
	}

	res, ok := root.Variables["last_command_result"].(string)
	if !ok {
		t.Fatalf("last_command_result not found or not string")
	}

	if len(res) > MaxCommandResultLength+30 { // allow some buffer for [TRUNCATED]
		t.Errorf("Result not truncated: length %d, max %d", len(res), MaxCommandResultLength)
	}

	if !strings.Contains(res, "[TRUNCATED]") {
		t.Errorf("Result does not contain [TRUNCATED] marker")
	}

	// Verify History truncation
	history, ok := root.Variables["command_output_history"].([]string)
	if !ok {
		// Try []interface{} which can happen after some operations
		histAny := root.Variables["command_output_history"]
		if casted, ok := histAny.([]interface{}); ok {
			for _, item := range casted {
				if s, ok := item.(string); ok {
					history = append(history, s)
				}
			}
		} else {
			t.Fatalf("command_output_history not found or wrong type: %T", histAny)
		}
	}

	if len(history) == 0 {
		t.Fatalf("command_output_history is empty")
	}

	lastEntry := history[len(history)-1]
	if len(lastEntry) > MaxHistoryEntryLength+200 { // History entry includes timestamp and command
		t.Errorf("History entry too long: length %d", len(lastEntry))
	}
	if !strings.Contains(lastEntry, "[TRUNCATED]") {
		t.Errorf("History entry does not contain [TRUNCATED] marker")
	}

	fmt.Printf("Truncated result length: %d\n", len(res))
}
