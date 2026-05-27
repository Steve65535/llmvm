package vector

import (
	"context"
	"path/filepath"
	"testing"
)

// TestHashingEmbedderDeterministic 嵌入器对相同输入必须返回相同向量。
func TestHashingEmbedderDeterministic(t *testing.T) {
	e := NewHashingEmbedder(384)
	ctx := context.Background()
	v1, err := e.Embed(ctx, "parser action validation")
	if err != nil {
		t.Fatal(err)
	}
	v2, err := e.Embed(ctx, "parser action validation")
	if err != nil {
		t.Fatal(err)
	}
	if len(v1) != 384 {
		t.Fatalf("expected dim 384, got %d", len(v1))
	}
	for i := range v1 {
		if v1[i] != v2[i] {
			t.Fatalf("non-deterministic at idx %d: %v vs %v", i, v1[i], v2[i])
		}
	}
}

// TestHashingEmbedderSimilarityRanking 相关文本得分应高于无关文本。
func TestHashingEmbedderSimilarityRanking(t *testing.T) {
	e := NewHashingEmbedder(384)
	ctx := context.Background()
	q, _ := e.Embed(ctx, "parser validation rejects invalid action_type")
	relevant, _ := e.Embed(ctx, "the parser must validate every action_type and reject unknown ones")
	unrelated, _ := e.Embed(ctx, "user authentication uses session cookies for browser flows")

	simRel := cosine(q, relevant)
	simUnrel := cosine(q, unrelated)
	if simRel <= simUnrel {
		t.Fatalf("expected relevant>unrelated, got rel=%.3f unrel=%.3f", simRel, simUnrel)
	}
}

func cosine(a, b []float32) float64 {
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}
	return dot
}

// TestStoreUpsertQueryRoundtrip 验证持久化目录可以做 upsert + query 的最小回环。
func TestStoreUpsertQueryRoundtrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vec")
	store, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if store.Count() != 0 {
		t.Fatalf("expected empty store, got %d", store.Count())
	}
	ctx := context.Background()

	docs := map[string]string{
		"art_1": "parser must reject unknown action_type with structured error",
		"art_2": "user authentication flow uses session cookies and CSRF tokens",
		"art_3": "context budget rules: handoff section never exceeds 2% of total tokens",
	}
	for id, body := range docs {
		if err := store.Upsert(ctx, id, body, map[string]string{
			"producer": "node1", "kind": "evidence", "scope": "node",
		}); err != nil {
			t.Fatalf("Upsert %s: %v", id, err)
		}
	}

	res, err := store.Query(ctx, "parser action validation rejects invalid input", 3, nil)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(res) == 0 {
		t.Fatalf("expected at least 1 result")
	}
	if res[0].ID != "art_1" {
		t.Errorf("expected art_1 as top hit, got %s (full: %+v)", res[0].ID, res)
	}
}

// TestStoreQueryRespectsWhereFilter scope=subtree 应该被过滤掉。
func TestStoreQueryRespectsWhereFilter(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vec_filter")
	store, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	_ = store.Upsert(ctx, "node_doc", "parser action validation", map[string]string{"scope": "node"})
	_ = store.Upsert(ctx, "tree_doc", "parser action validation", map[string]string{"scope": "subtree"})

	res, err := store.Query(ctx, "parser validation", 10, map[string]string{"scope": "node"})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.ID != "node_doc" {
			t.Errorf("scope filter failed: got %s with scope=%s", r.ID, r.Metadata["scope"])
		}
	}
	if len(res) == 0 {
		t.Fatal("expected at least node_doc")
	}
}
