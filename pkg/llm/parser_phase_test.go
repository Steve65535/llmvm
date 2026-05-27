package llm

import (
	"strings"
	"testing"
)

// === add_artifact / modify_artifact 校验 ===

func TestAddArtifactRequiresName(t *testing.T) {
	input := `{"actions":[{"action_type":"add_artifact","content":"x"}]}`
	_, err := ParseResponse(input)
	if err == nil || !strings.Contains(err.Error(), "missing artifact_name") {
		t.Fatalf("expected missing artifact_name error, got %v", err)
	}
}

func TestAddArtifactRequiresContent(t *testing.T) {
	input := `{"actions":[{"action_type":"add_artifact","artifact_name":"foo"}]}`
	_, err := ParseResponse(input)
	if err == nil || !strings.Contains(err.Error(), "missing content") {
		t.Fatalf("expected missing content error, got %v", err)
	}
}

func TestAddArtifactRejectsInvalidScope(t *testing.T) {
	input := `{"actions":[{"action_type":"add_artifact","artifact_name":"foo","content":"x","scope":"galaxy"}]}`
	_, err := ParseResponse(input)
	if err == nil || !strings.Contains(err.Error(), "invalid scope") {
		t.Fatalf("expected invalid scope error, got %v", err)
	}
}

func TestAddArtifactValidPayload(t *testing.T) {
	input := `{"actions":[{
		"action_type":"add_artifact",
		"artifact_name":"parser_invalid_cases",
		"artifact_type":"evidence",
		"scope":"subtree",
		"tags":["parser","tests"],
		"granularity":"case-level",
		"importance":"high",
		"content":"Test cases for invalid parser inputs",
		"summary":"3 invalid action shapes the parser must reject"
	}]}`
	resp, err := ParseResponse(input)
	if err != nil {
		t.Fatalf("expected valid add_artifact to parse, got %v", err)
	}
	a := resp.Actions[0]
	if a.ArtifactName != "parser_invalid_cases" || a.ArtifactScope != "subtree" {
		t.Errorf("unexpected fields: %+v", a)
	}
	if len(a.ArtifactTags) != 2 {
		t.Errorf("tags lost in parse: %v", a.ArtifactTags)
	}
}

func TestModifyArtifactRequiresSupersedes(t *testing.T) {
	input := `{"actions":[{"action_type":"modify_artifact","content":"x"}]}`
	_, err := ParseResponse(input)
	if err == nil || !strings.Contains(err.Error(), "missing supersedes") {
		t.Fatalf("expected missing supersedes error, got %v", err)
	}
}

// === acceptance_criteria 校验 ===

func TestCreateNodeAcceptanceCriteriaTestableNeedsCommand(t *testing.T) {
	input := `{"actions":[{
		"action_type":"create_node",
		"node":{
			"id":"n1","name":"N","type":"Leaf","information":"i",
			"acceptance_criteria":[
				{"description":"foo","required":true,"check_type":"testable"}
			]
		}
	}]}`
	_, err := ParseResponse(input)
	if err == nil || !strings.Contains(err.Error(), "testable check_type requires check_command") {
		t.Fatalf("expected testable+missing-command error, got %v", err)
	}
}

func TestCreateNodeAcceptanceCriteriaInvalidCheckType(t *testing.T) {
	input := `{"actions":[{
		"action_type":"create_node",
		"node":{
			"id":"n1","name":"N","type":"Leaf","information":"i",
			"acceptance_criteria":[
				{"description":"foo","check_type":"made_up_type"}
			]
		}
	}]}`
	_, err := ParseResponse(input)
	if err == nil || !strings.Contains(err.Error(), "invalid check_type") {
		t.Fatalf("expected invalid check_type error, got %v", err)
	}
}

func TestCreateNodeAcceptanceCriteriaValidPayload(t *testing.T) {
	input := `{"actions":[{
		"action_type":"create_node",
		"node":{
			"id":"impl","name":"Impl","type":"Leaf","information":"work",
			"acceptance_criteria":[
				{"description":"tests pass","required":true,"check_type":"testable","check_command":"go test ./..."},
				{"description":"reviewed","required":false,"check_type":"manual"}
			]
		}
	}]}`
	resp, err := ParseResponse(input)
	if err != nil {
		t.Fatalf("expected valid criteria to parse, got %v", err)
	}
	tn := resp.Actions[0].Node.ToTaskNode()
	if len(tn.AcceptanceCriteria) != 2 {
		t.Fatalf("expected 2 criteria, got %d", len(tn.AcceptanceCriteria))
	}
	if string(tn.AcceptanceCriteria[0].CheckType) != "testable" {
		t.Errorf("expected testable, got %q", tn.AcceptanceCriteria[0].CheckType)
	}
	if !tn.AcceptanceCriteria[0].Required {
		t.Errorf("first criterion should be required")
	}
}

func TestMarkCompleteAcceptanceResultsRequireCriterionID(t *testing.T) {
	input := `{"actions":[{
		"action_type":"mark_complete",
		"summary":"done",
		"acceptance_results":[{"passed":true}]
	}]}`
	_, err := ParseResponse(input)
	if err == nil || !strings.Contains(err.Error(), "missing criterion_id") {
		t.Fatalf("expected missing criterion_id error, got %v", err)
	}
}

// === request_context 校验 ===

func TestRequestContextRequiresNeeds(t *testing.T) {
	input := `{"actions":[{"action_type":"request_context"}]}`
	_, err := ParseResponse(input)
	if err == nil || !strings.Contains(err.Error(), "missing needs") {
		t.Fatalf("expected missing needs error, got %v", err)
	}
}

func TestRequestContextNeedRequiresKindAndQuery(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{
			input: `{"actions":[{"action_type":"request_context","needs":[{"query":"x"}]}]}`,
			want:  "missing kind",
		},
		{
			input: `{"actions":[{"action_type":"request_context","needs":[{"kind":"fts"}]}]}`,
			want:  "missing query",
		},
	}
	for _, tc := range cases {
		_, err := ParseResponse(tc.input)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("input=%s expected %q, got %v", tc.input, tc.want, err)
		}
	}
}

// === Loop 节点已删除 ===

func TestCreateNodeRejectsLoopType(t *testing.T) {
	input := `{"actions":[{"action_type":"create_node","node":{"id":"x","name":"X","type":"Loop","information":"old"}}]}`
	_, err := ParseResponse(input)
	if err == nil || !strings.Contains(err.Error(), "Loop type has been removed") {
		t.Fatalf("expected Loop removal error, got %v", err)
	}
}

func TestAppendSiblingRejectsLoopType(t *testing.T) {
	input := `{"actions":[{"action_type":"append_sibling_node","node":{"id":"x","name":"X","type":"Loop"}}]}`
	_, err := ParseResponse(input)
	if err == nil || !strings.Contains(err.Error(), "Loop type has been removed") {
		t.Fatalf("expected Loop removal error, got %v", err)
	}
}
