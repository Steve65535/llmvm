package memory

import (
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestUpsertAndQueryNode(t *testing.T) {
	s := newTestStore(t)

	if err := s.UpsertNode("root", "", "Root", "Normal", "Pending", "root task", 0, 0); err != nil {
		t.Fatalf("UpsertNode root: %v", err)
	}
	if err := s.UpsertNode("child1", "root", "Child1", "Leaf", "Completed", "child task", 1, 1); err != nil {
		t.Fatalf("UpsertNode child1: %v", err)
	}
	if err := s.UpsertNode("child2", "root", "Child2", "Leaf", "Completed", "child task 2", 1, 2); err != nil {
		t.Fatalf("UpsertNode child2: %v", err)
	}

	chain, err := s.QueryAncestorChain("child1")
	if err != nil {
		t.Fatalf("QueryAncestorChain: %v", err)
	}
	if len(chain) != 2 {
		t.Fatalf("expected 2 nodes in chain, got %d", len(chain))
	}
	if chain[0].ID != "root" || chain[1].ID != "child1" {
		t.Errorf("unexpected chain: %v", chain)
	}
}

func TestUpsertAndQueryHandoff(t *testing.T) {
	s := newTestStore(t)

	_ = s.UpsertNode("parent", "", "Parent", "Normal", "Running", "", 0, 0)
	_ = s.UpsertNode("sib1", "parent", "Sibling1", "Leaf", "Completed", "", 1, 1)
	_ = s.UpsertNode("sib2", "parent", "Sibling2", "Leaf", "Completed", "", 1, 2)
	_ = s.UpsertNode("current", "parent", "Current", "Leaf", "Pending", "", 1, 3)

	_ = s.UpsertHandoff("sib1", "goal1", "summary1",
		[]string{"fact1"}, []string{"dec1"}, nil,
		[]string{"art_1"}, nil, []string{"oq1"},
		"handoff1", "high")

	handoffs, err := s.QuerySiblingHandoffs("parent", "current", []string{"Completed"}, 5)
	if err != nil {
		t.Fatalf("QuerySiblingHandoffs: %v", err)
	}
	if len(handoffs) != 2 {
		t.Fatalf("expected 2 sibling handoffs, got %d", len(handoffs))
	}
	// sib1 should have structured handoff
	found := false
	for _, h := range handoffs {
		if h.NodeID == "sib1" {
			found = true
			if h.Goal != "goal1" {
				t.Errorf("expected goal1, got %q", h.Goal)
			}
			if len(h.KeyFacts) != 1 || h.KeyFacts[0] != "fact1" {
				t.Errorf("unexpected key_facts: %v", h.KeyFacts)
			}
			if h.Confidence != "high" {
				t.Errorf("expected confidence high, got %q", h.Confidence)
			}
		}
	}
	if !found {
		t.Error("sib1 handoff not found")
	}
}

func TestArtifactUpsertAndQuery(t *testing.T) {
	s := newTestStore(t)

	_ = s.UpsertArtifact("art_1", "node1", "command", "ls -la", "listed 5 files", "", false)
	_ = s.UpsertArtifact("art_2", "node1", "file_read", "config.json", "config file", "", true)

	pinned, err := s.QueryPinnedArtifacts(10)
	if err != nil {
		t.Fatalf("QueryPinnedArtifacts: %v", err)
	}
	if len(pinned) != 1 || pinned[0].ID != "art_2" {
		t.Errorf("expected art_2 pinned, got %v", pinned)
	}

	recent, err := s.QueryRecentArtifacts(10)
	if err != nil {
		t.Fatalf("QueryRecentArtifacts: %v", err)
	}
	if len(recent) != 2 {
		t.Errorf("expected 2 recent artifacts, got %d", len(recent))
	}
}

func TestArtifactFTS(t *testing.T) {
	s := newTestStore(t)

	_ = s.UpsertArtifact("art_1", "node1", "command", "grep output", "grep result", "", false)
	_ = s.IndexArtifactFTS("art_1", "grep output", "grep result", "found database schema in config.json")

	_ = s.UpsertArtifact("art_2", "node1", "file_read", "readme.txt", "readme", "", false)
	_ = s.IndexArtifactFTS("art_2", "readme.txt", "readme", "this is a simple readme file")

	results, err := s.SearchArtifactsFTS("database", 5)
	if err != nil {
		t.Fatalf("SearchArtifactsFTS: %v", err)
	}
	if len(results) != 1 || results[0].ID != "art_1" {
		t.Errorf("expected art_1 from FTS, got %v", results)
	}
}

func TestRebuild(t *testing.T) {
	s := newTestStore(t)

	input := RebuildInput{
		Nodes: []RebuildNode{
			{ID: "root", Name: "Root", Type: "Normal", Status: "Pending", Depth: 0},
			{
				ID: "n1", ParentID: "root", Name: "N1", Type: "Leaf", Status: "Completed", Depth: 1,
				HasHandoff: true, Goal: "do something", Summary: "did it",
				KeyFacts: []string{"fact"}, Confidence: "high",
			},
		},
		Artifacts: []RebuildArtifact{
			{ID: "art_1", ProducerNodeID: "n1", Kind: "command", Title: "ls", Summary: "listed files", Content: "file1.txt\nfile2.txt"},
		},
	}

	if err := s.Rebuild(input); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	chain, err := s.QueryAncestorChain("n1")
	if err != nil {
		t.Fatalf("QueryAncestorChain after rebuild: %v", err)
	}
	if len(chain) != 2 {
		t.Fatalf("expected 2 in chain after rebuild, got %d", len(chain))
	}

	arts, err := s.QueryArtifactsByNode("n1")
	if err != nil {
		t.Fatalf("QueryArtifactsByNode: %v", err)
	}
	if len(arts) != 1 || arts[0].ID != "art_1" {
		t.Errorf("expected art_1, got %v", arts)
	}
}

func TestUpdateArtifactPinned(t *testing.T) {
	s := newTestStore(t)

	_ = s.UpsertArtifact("art_1", "n1", "command", "cmd", "summary", "", false)

	pinned, _ := s.QueryPinnedArtifacts(10)
	if len(pinned) != 0 {
		t.Errorf("expected 0 pinned before update")
	}

	_ = s.UpdateArtifactPinned("art_1", true)

	pinned, _ = s.QueryPinnedArtifacts(10)
	if len(pinned) != 1 {
		t.Errorf("expected 1 pinned after update")
	}
}
