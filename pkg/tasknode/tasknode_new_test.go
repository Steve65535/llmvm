package tasknode

import (
	"testing"
)

func TestAppendSiblingAfter(t *testing.T) {
	root := NewTaskNode("root", "Root", Normal, nil)
	child1 := NewTaskNode("c1", "Child1", Leaf, nil)
	child2 := NewTaskNode("c2", "Child2", Leaf, nil)
	root.AddChild(child1)
	root.AddChild(child2)

	sibling := NewTaskNode("sib1", "Sibling", Leaf, nil)
	if err := child1.AppendSiblingAfter(sibling); err != nil {
		t.Fatalf("AppendSiblingAfter: %v", err)
	}

	// Order should be: c1, sib1, c2
	if len(root.Children) != 3 {
		t.Fatalf("expected 3 children, got %d", len(root.Children))
	}
	if root.Children[0].ID != "c1" || root.Children[1].ID != "sib1" || root.Children[2].ID != "c2" {
		t.Errorf("unexpected order: %v %v %v", root.Children[0].ID, root.Children[1].ID, root.Children[2].ID)
	}
	if sibling.Parent != root {
		t.Error("sibling.Parent should be root")
	}
}

func TestAppendSiblingAfterRoot(t *testing.T) {
	root := NewTaskNode("root", "Root", Normal, nil)
	sibling := NewTaskNode("sib", "Sib", Leaf, nil)
	err := root.AppendSiblingAfter(sibling)
	if err == nil {
		t.Error("expected error when appending sibling to root")
	}
}

func TestAppendSiblingAfterLoopChild(t *testing.T) {
	root := NewTaskNode("root", "Root", Normal, nil)
	loop := NewTaskNode("loop", "Loop", Loop, nil)
	child := NewTaskNode("c1", "Child", Leaf, nil)
	root.AddChild(loop)
	loop.AddChild(child)

	sibling := NewTaskNode("sib", "Sib", Leaf, nil)
	err := child.AppendSiblingAfter(sibling)
	if err == nil {
		t.Error("expected error when appending sibling inside Loop")
	}
}

func TestAppendSiblingAfterLast(t *testing.T) {
	root := NewTaskNode("root", "Root", Normal, nil)
	child1 := NewTaskNode("c1", "Child1", Leaf, nil)
	child2 := NewTaskNode("c2", "Child2", Leaf, nil)
	root.AddChild(child1)
	root.AddChild(child2)

	sibling := NewTaskNode("sib", "Sib", Leaf, nil)
	if err := child2.AppendSiblingAfter(sibling); err != nil {
		t.Fatalf("AppendSiblingAfter last: %v", err)
	}

	// Order: c1, c2, sib
	if len(root.Children) != 3 {
		t.Fatalf("expected 3 children, got %d", len(root.Children))
	}
	if root.Children[2].ID != "sib" {
		t.Errorf("expected sib at index 2, got %q", root.Children[2].ID)
	}
}

func TestWaitingHumanStatus(t *testing.T) {
	node := NewTaskNode("n1", "Node", Leaf, nil)
	node.Status = WaitingHuman
	if node.Status != WaitingHuman {
		t.Error("expected WaitingHuman status")
	}
}

func TestStructuredHandoffFields(t *testing.T) {
	node := NewTaskNode("n1", "Node", Leaf, nil)
	node.Goal = "Parse config"
	node.Summary = "Parsed successfully"
	node.Decisions = []string{"Used JSON"}
	node.Assumptions = []string{"UTF-8 encoding"}
	node.Outputs = []string{"config.json"}
	node.OpenQuestions = []string{"Is schema versioned?"}
	node.Confidence = "high"

	if node.Goal != "Parse config" {
		t.Errorf("unexpected goal: %q", node.Goal)
	}
	if len(node.Decisions) != 1 {
		t.Errorf("unexpected decisions: %v", node.Decisions)
	}
	if node.Confidence != "high" {
		t.Errorf("unexpected confidence: %q", node.Confidence)
	}
}
