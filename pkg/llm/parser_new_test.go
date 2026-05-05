package llm

import (
	"testing"
)

func TestParseRequestHumanInput(t *testing.T) {
	json := `{"actions":[{"action_type":"request_human_input","question":"Continue?","context":"37 files found","options":["continue","abort"],"blocking":true}]}`
	resp, err := ParseResponse(json)
	if err != nil {
		t.Fatalf("ParseResponse: %v", err)
	}
	if len(resp.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(resp.Actions))
	}
	a := resp.Actions[0]
	if a.ActionType != "request_human_input" {
		t.Errorf("expected request_human_input, got %q", a.ActionType)
	}
	if a.Question != "Continue?" {
		t.Errorf("expected question 'Continue?', got %q", a.Question)
	}
	if len(a.Options) != 2 {
		t.Errorf("expected 2 options, got %d", len(a.Options))
	}
	if !a.Blocking {
		t.Error("expected blocking=true")
	}
}

func TestParseRequestHumanInputMissingQuestion(t *testing.T) {
	json := `{"actions":[{"action_type":"request_human_input","context":"some context"}]}`
	_, err := ParseResponse(json)
	if err == nil {
		t.Error("expected error for missing question")
	}
}

func TestParseAppendSiblingNode(t *testing.T) {
	json := `{"actions":[{"action_type":"append_sibling_node","node":{"id":"sib1","name":"Sibling","type":"Leaf","information":"do something"}}]}`
	resp, err := ParseResponse(json)
	if err != nil {
		t.Fatalf("ParseResponse: %v", err)
	}
	a := resp.Actions[0]
	if a.ActionType != "append_sibling_node" {
		t.Errorf("expected append_sibling_node, got %q", a.ActionType)
	}
	if a.Node.ID != "sib1" {
		t.Errorf("expected node id sib1, got %q", a.Node.ID)
	}
}

func TestParseAppendSiblingNodeMissingID(t *testing.T) {
	json := `{"actions":[{"action_type":"append_sibling_node","node":{"name":"Sibling","type":"Leaf"}}]}`
	_, err := ParseResponse(json)
	if err == nil {
		t.Error("expected error for missing node.id")
	}
}

func TestParseAppendSiblingNodeInvalidType(t *testing.T) {
	json := `{"actions":[{"action_type":"append_sibling_node","node":{"id":"s1","name":"S","type":"Invalid"}}]}`
	_, err := ParseResponse(json)
	if err == nil {
		t.Error("expected error for invalid node type")
	}
}

func TestParseQueryMemory(t *testing.T) {
	json := `{"actions":[{"action_type":"query_memory","query_type":"sibling_handoffs","filters":{"parent_id":"node_1"},"limit":3}]}`
	resp, err := ParseResponse(json)
	if err != nil {
		t.Fatalf("ParseResponse: %v", err)
	}
	a := resp.Actions[0]
	if a.QueryType != "sibling_handoffs" {
		t.Errorf("expected sibling_handoffs, got %q", a.QueryType)
	}
	if a.Limit != 3 {
		t.Errorf("expected limit 3, got %d", a.Limit)
	}
}

func TestParseQueryMemoryInvalidType(t *testing.T) {
	json := `{"actions":[{"action_type":"query_memory","query_type":"arbitrary_sql"}]}`
	_, err := ParseResponse(json)
	if err == nil {
		t.Error("expected error for invalid query_type")
	}
}

func TestParseRicherHandoff(t *testing.T) {
	json := `{"actions":[{
		"action_type":"mark_complete",
		"goal":"Parse config",
		"summary":"Parsed config.json successfully",
		"key_facts":["config at test/sandbox/config.json"],
		"decisions":["Used JSON over YAML"],
		"assumptions":["File is UTF-8"],
		"artifact_refs":["art_1"],
		"outputs":["test/sandbox/config.json"],
		"open_questions":["Is schema versioned?"],
		"handoff":"Config available in art_1",
		"confidence":"high"
	}]}`
	resp, err := ParseResponse(json)
	if err != nil {
		t.Fatalf("ParseResponse: %v", err)
	}
	a := resp.Actions[0]
	if a.Goal != "Parse config" {
		t.Errorf("expected goal, got %q", a.Goal)
	}
	if a.Confidence != "high" {
		t.Errorf("expected confidence high, got %q", a.Confidence)
	}
	if len(a.Decisions) != 1 || a.Decisions[0] != "Used JSON over YAML" {
		t.Errorf("unexpected decisions: %v", a.Decisions)
	}
	if len(a.OpenQuestions) != 1 {
		t.Errorf("expected 1 open question, got %d", len(a.OpenQuestions))
	}
}
