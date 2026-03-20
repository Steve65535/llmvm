package artifact

import (
	"os"
	"strings"
	"testing"
)

func TestAddAndGet(t *testing.T) {
	s := New()
	art := s.Add("file_read", "test.go", "line1\nline2\nline3", "node_1")

	if art.ID != "art_1" {
		t.Errorf("expected art_1, got %s", art.ID)
	}
	if art.Type != "file_read" {
		t.Errorf("expected file_read, got %s", art.Type)
	}
	if art.TotalLines != 3 {
		t.Errorf("expected 3 lines, got %d", art.TotalLines)
	}
	if art.CreatedBy != "node_1" {
		t.Errorf("expected node_1, got %s", art.CreatedBy)
	}

	got := s.Get("art_1")
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.ID != art.ID {
		t.Errorf("Get returned wrong artifact")
	}

	if s.Get("art_999") != nil {
		t.Error("expected nil for nonexistent artifact")
	}
}

func TestReadSlice(t *testing.T) {
	s := New()
	content := "line1\nline2\nline3\nline4\nline5"
	s.Add("file_read", "test.go", content, "node_1")

	// Read lines 2-4
	slice, err := s.ReadSlice("art_1", 2, 5)
	if err != nil {
		t.Fatal(err)
	}
	expected := "line2\nline3\nline4"
	if slice != expected {
		t.Errorf("expected %q, got %q", expected, slice)
	}

	// Default slice (no end_line)
	slice, err = s.ReadSlice("art_1", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if slice != content {
		t.Errorf("expected full content, got %q", slice)
	}

	// Out of range
	_, err = s.ReadSlice("art_1", 100, 200)
	if err == nil {
		t.Error("expected error for out of range")
	}

	// Nonexistent artifact
	_, err = s.ReadSlice("art_999", 1, 10)
	if err == nil {
		t.Error("expected error for nonexistent artifact")
	}
}

func TestEviction(t *testing.T) {
	s := New()

	// Fill store beyond MaxStoreSize
	for i := 0; i < MaxStoreSize+5; i++ {
		s.Add("command", "cmd", "output", "node_1")
	}

	// First few should be evicted
	art1 := s.Get("art_1")
	if art1 == nil {
		t.Fatal("art_1 should still exist as tombstone")
	}
	if !art1.Evicted {
		t.Error("art_1 should be evicted")
	}
	if art1.Summary == "" {
		t.Error("evicted artifact should retain summary")
	}

	// Reading evicted artifact should fail
	_, err := s.ReadSlice("art_1", 1, 10)
	if err == nil {
		t.Error("expected error reading evicted artifact")
	}

	// Latest should still be alive
	latestID := "art_55"
	latest := s.Get(latestID)
	if latest == nil {
		t.Fatalf("%s should exist", latestID)
	}
	if latest.Evicted {
		t.Errorf("%s should not be evicted", latestID)
	}
}

func TestPin(t *testing.T) {
	s := New()

	// Add and pin first artifact
	s.Add("file_read", "important.go", "critical data", "node_1")
	s.Pin("art_1")

	// Fill store to trigger eviction
	for i := 0; i < MaxStoreSize+10; i++ {
		s.Add("command", "cmd", "output", "node_2")
	}

	// Pinned artifact should survive
	art1 := s.Get("art_1")
	if art1 == nil {
		t.Fatal("pinned artifact should exist")
	}
	if art1.Evicted {
		t.Error("pinned artifact should not be evicted")
	}

	// Should still be readable
	slice, err := s.ReadSlice("art_1", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if slice != "critical data" {
		t.Errorf("expected 'critical data', got %q", slice)
	}
}

func TestSpillToDisk(t *testing.T) {
	// Clean up spill dir
	_ = os.RemoveAll(SpillDir)
	defer os.RemoveAll(SpillDir)

	s := New()

	// Create content larger than MaxContentSize
	bigContent := strings.Repeat("x", MaxContentSize+1000)
	art := s.Add("file_read", "big.txt", bigContent, "node_1")

	if art.SpillPath == "" {
		t.Fatal("expected spill path for large content")
	}
	if art.Content != "" {
		t.Error("large content should not be in memory")
	}

	// Should still be readable via ReadSlice
	slice, err := s.ReadSlice("art_1", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(slice) != len(bigContent) {
		t.Errorf("expected %d chars, got %d", len(bigContent), len(slice))
	}
}

func TestListByNode(t *testing.T) {
	s := New()
	s.Add("file_read", "a.go", "content_a", "node_1")
	s.Add("search", "pattern@dir", "results", "node_2")
	s.Add("command", "ls", "output", "node_1")

	list := s.ListByNode("node_1")
	if len(list) != 2 {
		t.Errorf("expected 2 artifacts for node_1, got %d", len(list))
	}

	list = s.ListByNode("node_2")
	if len(list) != 1 {
		t.Errorf("expected 1 artifact for node_2, got %d", len(list))
	}

	list = s.ListByNode("node_999")
	if len(list) != 0 {
		t.Errorf("expected 0 artifacts for nonexistent node, got %d", len(list))
	}
}

func TestIndex(t *testing.T) {
	s := New()
	s.Add("file_read", "a.go", "line1\nline2", "node_1")
	s.Add("search", "foo@bar", "", "node_2")

	idx := s.Index(10, 0)
	if !strings.Contains(idx, "art_1") {
		t.Error("index should contain art_1")
	}
	if !strings.Contains(idx, "art_2") {
		t.Error("index should contain art_2")
	}

	// Test max entries limit
	for i := 0; i < 30; i++ {
		s.Add("command", "cmd", "out", "node_1")
	}
	idx = s.Index(5, 0)
	count := strings.Count(idx, "- art_")
	if count > 5 {
		t.Errorf("expected at most 5 entries, got %d", count)
	}
}

func TestSummaryGeneration(t *testing.T) {
	s := New()

	art := s.Add("file_read", "test.go", "a\nb\nc", "n1")
	if !strings.Contains(art.Summary, "test.go") {
		t.Errorf("file_read summary should contain path, got: %s", art.Summary)
	}

	art = s.Add("search", "pattern@dir", "match1\nmatch2\n", "n1")
	if !strings.Contains(art.Summary, "matches") {
		t.Errorf("search summary should contain matches, got: %s", art.Summary)
	}

	art = s.Add("search", "nope@dir", "", "n1")
	if !strings.Contains(art.Summary, "no matches") {
		t.Errorf("empty search summary should say no matches, got: %s", art.Summary)
	}

	art = s.Add("dir_list", "/tmp", "a\nb\nc", "n1")
	if !strings.Contains(art.Summary, "3 entries") {
		t.Errorf("dir_list summary should contain entry count, got: %s", art.Summary)
	}

	art = s.Add("command", "ls -la", "total 42\ndrwxr", "n1")
	if !strings.Contains(art.Summary, "total 42") {
		t.Errorf("command summary should contain first line, got: %s", art.Summary)
	}
}
