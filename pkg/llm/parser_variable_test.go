package llm

import (
	"testing"
)

func TestParseResponseWithVariables(t *testing.T) {
	jsonStr := `
	{
		"actions": [
			{
				"action_type": "create_node",
				"node": {
					"id": "node_v1",
					"name": "Node With Vars",
					"type": "Normal",
					"information": "Testing variable parsing",
					"variables": {
						"key1": "value1",
						"key2": 123,
						"key3": true
					},
					"is_important": true
				}
			}
		]
	}
	`

	resp, err := ParseResponse(jsonStr)
	if err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if len(resp.Actions) != 1 {
		t.Fatalf("Expected 1 action, got %d", len(resp.Actions))
	}

	action := resp.Actions[0]
	taskNode := action.Node.ToTaskNode()

	// Verify ID
	if taskNode.ID != "node_v1" {
		t.Errorf("Expected ID 'node_v1', got '%s'", taskNode.ID)
	}

	// Verify Variables
	if taskNode.Variables == nil {
		t.Fatal("Variables map is nil")
	}

	if val, ok := taskNode.Variables["key1"]; !ok || val != "value1" {
		t.Errorf("Expected key1='value1', got %v", val)
	}

	// JSON numbers are parsed as float64 by default in Go's map[string]interface{} unmarshal
	if val, ok := taskNode.Variables["key2"]; !ok || val.(float64) != 123 {
		t.Errorf("Expected key2=123, got %v", val)
	}

	if val, ok := taskNode.Variables["key3"]; !ok || val != true {
		t.Errorf("Expected key3=true, got %v", val)
	}

	// Verify IsImportant
	if !taskNode.IsImportant {
		t.Error("Expected IsImportant=true")
	}
}

func TestParseUpdateVariables(t *testing.T) {
	jsonStr := `
	{
		"actions": [
			{
				"action_type": "update_variables",
				"variables": {
					"new_key": "new_val"
				}
			}
		]
	}
	`
	resp, err := ParseResponse(jsonStr)
	if err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	action := resp.Actions[0]
	if action.Variables == nil {
		t.Fatal("Variables map is nil for update action")
	}

	if val, ok := action.Variables["new_key"]; !ok || val != "new_val" {
		t.Errorf("Expected new_key='new_val', got %v", val)
	}
}
