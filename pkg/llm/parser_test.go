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
					"type": "Leaf",
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
	// Loop has been removed; Leaf is now iota = 1.
	if taskNode.Type != 1 {
		t.Errorf("Expected TaskType Leaf (1), got %d", taskNode.Type)
	}
	if len(taskNode.Information) != 1 || taskNode.Information[0] != "Test info" {
		t.Errorf("Information mismatch")
	}
}

func TestParseResponseRejectsLoop(t *testing.T) {
	input := `{"actions":[{"action_type":"create_node","node":{"id":"x","name":"X","type":"Loop","information":"deprecated"}}]}`
	if _, err := ParseResponse(input); err == nil {
		t.Fatal("expected ParseResponse to reject Loop type")
	}
}

func TestParseResponseError(t *testing.T) {
	input := `{"broken_json": ...`
	_, err := ParseResponse(input)
	if err == nil {
		t.Fatal("Expected error for broken json, got nil")
	}
}
