package llm

import (
	"testing"
)

func TestParseResponse(t *testing.T) {
	input := "```json\n" +
		`{
		"actions": [
			{
				"action_type": "create_node",
				"node": {
					"id": "child_1",
					"name": "Test Node",
					"type": "Loop",
					"information": "Test info"
				}
			}
		]
	}` + "\n```"

	resp, err := ParseResponse(input)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(resp.Actions) != 1 {
		t.Fatalf("Expected 1 action, got %d", len(resp.Actions))
	}

	action := resp.Actions[0]
	if action.ActionType != "create_node" {
		t.Errorf("Expected create_node, got %s", action.ActionType)
	}

	taskNode := action.Node.ToTaskNode()
	// Note: checking int value directly or trusting the mapping logic
	if taskNode.Type != 1 { // Loop is 1
		t.Errorf("Expected TaskType Loop (1), got %d", taskNode.Type)
	}
	if len(taskNode.Information) != 1 || taskNode.Information[0] != "Test info" {
		t.Errorf("Information mismatch")
	}
}

func TestParseResponseError(t *testing.T) {
	input := `{"broken_json": ...`
	_, err := ParseResponse(input)
	if err == nil {
		t.Fatal("Expected error for broken json, got nil")
	}
}
