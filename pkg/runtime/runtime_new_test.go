package runtime

import (
	"testing"

	"github.com/Steve65535/llmvm/pkg/llm"
	"github.com/Steve65535/llmvm/pkg/tasknode"
)

// stubEngine 用于测试，返回预设的 JSON 响应
type stubEngine struct {
	responses []string
	idx       int
}

func (s *stubEngine) Call(prompt string) (*llm.Output, error) {
	if s.idx >= len(s.responses) {
		return &llm.Output{Response: `{"actions":[{"action_type":"mark_complete","summary":"done","confidence":"high"}]}`}, nil
	}
	resp := s.responses[s.idx]
	s.idx++
	return &llm.Output{Response: resp}, nil
}

func (s *stubEngine) CallAsync(prompt string) <-chan *llm.Output {
	ch := make(chan *llm.Output, 1)
	out, _ := s.Call(prompt)
	ch <- out
	close(ch)
	return ch
}

func newTestRuntime(root *tasknode.TaskNode, responses []string) *Runtime {
	engine := &stubEngine{responses: responses}
	rt := NewRuntime(engine, root)
	return rt
}

// TestHumanInTheLoop 验证 request_human_input 暂停并恢复节点。
func TestHumanInTheLoop(t *testing.T) {
	root := tasknode.NewTaskNode("root", "Root", tasknode.Leaf, []string{"test task"})

	humanCalled := false
	rt := newTestRuntime(root, []string{
		`{"actions":[{"action_type":"request_human_input","question":"Continue?","options":["yes","no"],"blocking":true}]}`,
		`{"actions":[{"action_type":"mark_complete","summary":"done after human input","confidence":"high"}]}`,
	})
	rt.HumanInputFunc = func(req *tasknode.HumanRequest) (*tasknode.HumanResponse, error) {
		humanCalled = true
		if req.Question != "Continue?" {
			t.Errorf("unexpected question: %q", req.Question)
		}
		return &tasknode.HumanResponse{Value: "yes", Timestamp: 1234567890}, nil
	}

	if err := rt.Execute("test"); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !humanCalled {
		t.Error("HumanInputFunc was not called")
	}
	if root.Variables["human_response"] != "yes" {
		t.Errorf("expected human_response=yes, got %v", root.Variables["human_response"])
	}
}

// TestAppendSiblingNodeAction 验证 append_sibling_node 正确插入同级节点。
// DFS 顺序：root(Normal) → c1(Leaf) → sib1(Leaf)
// root 第一次被 LLM 处理（消耗响应1），然后 DFS 下到 c1
// c1 追加 sib1 并完成（消耗响应2），sib1 完成（消耗响应3）
func TestAppendSiblingNodeAction(t *testing.T) {
	root := tasknode.NewTaskNode("root", "Root", tasknode.Normal, []string{"root task"})
	child := tasknode.NewTaskNode("c1", "Child1", tasknode.Leaf, []string{"child task"})
	root.AddChild(child)

	rt := newTestRuntime(root, []string{
		// 响应1: root (Normal) 被处理，不做任何事（mark_traveled 后 DFS 下到 c1）
		`{"actions":[{"action_type":"update_variables","variables":{"root_visited":"true"}}]}`,
		// 响应2: c1 (Leaf) 追加 sibling 并完成
		`{"actions":[
			{"action_type":"append_sibling_node","node":{"id":"sib1","name":"Sibling","type":"Leaf","information":"sibling task"}},
			{"action_type":"mark_complete","summary":"child1 done","confidence":"high"}
		]}`,
		// 响应3: sib1 (Leaf) 完成
		`{"actions":[{"action_type":"mark_complete","summary":"sibling done","confidence":"high"}]}`,
	})

	if err := rt.Execute("test"); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// root should have 2 children: c1 and sib1
	if len(root.Children) != 2 {
		t.Fatalf("expected 2 children, got %d: %v", len(root.Children), func() []string {
			ids := []string{}
			for _, c := range root.Children {
				ids = append(ids, c.ID)
			}
			return ids
		}())
	}
	if root.Children[0].ID != "c1" || root.Children[1].ID != "sib1" {
		t.Errorf("unexpected children order: %v %v", root.Children[0].ID, root.Children[1].ID)
	}
	if !root.Children[1].WetherFinished {
		t.Error("sibling should be finished")
	}
}

// TestMarkCompleteRicherHandoff 验证 mark_complete 的扩展字段被正确保存。
func TestMarkCompleteRicherHandoff(t *testing.T) {
	root := tasknode.NewTaskNode("root", "Root", tasknode.Leaf, []string{"task"})

	rt := newTestRuntime(root, []string{
		`{"actions":[{
			"action_type":"mark_complete",
			"goal":"Parse config",
			"summary":"Parsed successfully",
			"key_facts":["config.json found"],
			"decisions":["Used JSON"],
			"assumptions":["UTF-8"],
			"outputs":["config.json"],
			"open_questions":["versioned?"],
			"handoff":"config in art_1",
			"confidence":"high"
		}]}`,
	})

	if err := rt.Execute("test"); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if root.Goal != "Parse config" {
		t.Errorf("expected goal, got %q", root.Goal)
	}
	if root.Confidence != "high" {
		t.Errorf("expected confidence high, got %q", root.Confidence)
	}
	if len(root.Decisions) != 1 || root.Decisions[0] != "Used JSON" {
		t.Errorf("unexpected decisions: %v", root.Decisions)
	}
	if len(root.OpenQuestions) != 1 {
		t.Errorf("expected 1 open question, got %d", len(root.OpenQuestions))
	}
}

// TestNodeActivationHierarchyPath 验证 buildNodeActivation 正确构建层级路径。
func TestNodeActivationHierarchyPath(t *testing.T) {
	root := tasknode.NewTaskNode("root", "Root", tasknode.Normal, []string{"root"})
	child := tasknode.NewTaskNode("c1", "Child", tasknode.Normal, []string{"child"})
	leaf := tasknode.NewTaskNode("l1", "Leaf", tasknode.Leaf, []string{"leaf"})
	root.AddChild(child)
	child.AddChild(leaf)

	rt := newTestRuntime(root, nil)

	act := rt.buildNodeActivation(leaf)
	if len(act.HierarchyPath) != 3 {
		t.Fatalf("expected 3 nodes in hierarchy path, got %d", len(act.HierarchyPath))
	}
	if act.HierarchyPath[0].ID != "root" {
		t.Errorf("expected root at index 0, got %q", act.HierarchyPath[0].ID)
	}
	if act.HierarchyPath[2].ID != "l1" {
		t.Errorf("expected l1 at index 2, got %q", act.HierarchyPath[2].ID)
	}
	if act.NodeID != "l1" {
		t.Errorf("expected NodeID l1, got %q", act.NodeID)
	}
}

// TestNodeActivationSiblingHandoffs 验证 buildNodeActivation 正确收集兄弟 handoff。
func TestNodeActivationSiblingHandoffs(t *testing.T) {
	root := tasknode.NewTaskNode("root", "Root", tasknode.Normal, []string{"root"})
	sib1 := tasknode.NewTaskNode("s1", "Sib1", tasknode.Leaf, []string{"sib1"})
	sib2 := tasknode.NewTaskNode("s2", "Sib2", tasknode.Leaf, []string{"sib2"})
	current := tasknode.NewTaskNode("cur", "Current", tasknode.Leaf, []string{"current"})
	root.AddChild(sib1)
	root.AddChild(sib2)
	root.AddChild(current)

	sib1.WetherFinished = true
	sib1.Handoff = "sib1 handoff"
	sib1.Summary = "sib1 summary"
	sib1.Goal = "sib1 goal"
	sib1.Confidence = "high"

	sib2.WetherFinished = true
	sib2.Handoff = "sib2 handoff"

	rt := newTestRuntime(root, nil)
	act := rt.buildNodeActivation(current)

	if len(act.SiblingHandoffs) != 2 {
		t.Fatalf("expected 2 sibling handoffs, got %d", len(act.SiblingHandoffs))
	}

	found := false
	for _, h := range act.SiblingHandoffs {
		if h.NodeID == "s1" {
			found = true
			if h.Goal != "sib1 goal" {
				t.Errorf("expected sib1 goal, got %q", h.Goal)
			}
			if h.Confidence != "high" {
				t.Errorf("expected high confidence, got %q", h.Confidence)
			}
		}
	}
	if !found {
		t.Error("s1 handoff not found in activation")
	}
}

// TestSiblingInsertionOrdering 验证 sibling 插入后 DFS 顺序正确。
func TestSiblingInsertionOrdering(t *testing.T) {
	root := tasknode.NewTaskNode("root", "Root", tasknode.Normal, []string{"root"})
	c1 := tasknode.NewTaskNode("c1", "C1", tasknode.Leaf, []string{"c1"})
	c2 := tasknode.NewTaskNode("c2", "C2", tasknode.Leaf, []string{"c2"})
	root.AddChild(c1)
	root.AddChild(c2)

	sib := tasknode.NewTaskNode("sib", "Sib", tasknode.Leaf, []string{"sib"})
	if err := c1.AppendSiblingAfter(sib); err != nil {
		t.Fatalf("AppendSiblingAfter: %v", err)
	}

	// Expected order: c1, sib, c2
	ids := []string{}
	for _, ch := range root.Children {
		ids = append(ids, ch.ID)
	}
	expected := []string{"c1", "sib", "c2"}
	for i, id := range expected {
		if ids[i] != id {
			t.Errorf("position %d: expected %q, got %q", i, id, ids[i])
		}
	}
}

// TestActivationContextBudget 验证 working context 不超过预算。
func TestActivationContextBudget(t *testing.T) {
	root := tasknode.NewTaskNode("root", "Root", tasknode.Leaf, []string{"root"})
	rt := newTestRuntime(root, nil)

	act := rt.buildNodeActivation(root)
	if len(act.WorkingContext) > rt.budget.ContextBudget*4 {
		t.Errorf("working context %d chars exceeds budget %d chars",
			len(act.WorkingContext), rt.budget.ContextBudget*4)
	}
}
