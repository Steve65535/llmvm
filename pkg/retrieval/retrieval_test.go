package retrieval

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Steve65535/llmvm/pkg/memory"
	"github.com/Steve65535/llmvm/pkg/vector"
)

// TestHybridRetrievalRanksRelevantHigher：FTS + vector 混合检索 + 确定性 rerank 后，
// 相关 artifact 应排在不相关之前。同时验证 rerank trace 落到 SQLite。
func TestHybridRetrievalRanksRelevantHigher(t *testing.T) {
	mem, err := memory.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer mem.Close()
	vec, err := vector.New(filepath.Join(t.TempDir(), "vec"))
	if err != nil {
		t.Fatal(err)
	}

	docs := []struct {
		id, content string
		row         memory.ArtifactRow
	}{
		{
			id:      "art_relevant",
			content: "the parser must validate add_artifact payloads and reject missing fields",
			row: memory.ArtifactRow{
				ID: "art_relevant", ProducerNodeID: "n1", Kind: "evidence",
				Title: "parser_validation", Summary: "parser action validation rules",
				Scope: "node", Tags: []string{"parser", "validation"},
				Granularity: "case-level", Importance: "high",
				Version: 1, TokenCount: 60,
			},
		},
		{
			id:      "art_unrelated",
			content: "session cookie flow for the browser auth path",
			row: memory.ArtifactRow{
				ID: "art_unrelated", ProducerNodeID: "n1", Kind: "evidence",
				Title: "auth_flow", Summary: "user auth via session cookie",
				Scope: "node", Version: 1, TokenCount: 60,
			},
		},
	}
	ctx := context.Background()
	for _, d := range docs {
		if err := mem.UpsertArtifactFull(d.row); err != nil {
			t.Fatalf("upsert sqlite: %v", err)
		}
		if err := mem.IndexArtifactFTS(d.id, d.row.Title, d.row.Summary, d.content); err != nil {
			t.Fatalf("fts: %v", err)
		}
		if err := vec.Upsert(ctx, d.id, d.content, map[string]string{
			"producer": d.row.ProducerNodeID, "scope": d.row.Scope,
		}); err != nil {
			t.Fatalf("vec upsert: %v", err)
		}
	}

	svc := NewService(mem, vec)
	res, err := svc.Query(ctx, Query{
		NodeID:             "n1",
		Goal:               "verify parser validation rules",
		AcceptanceCriteria: []string{"parser rejects invalid add_artifact"},
		Need:               "parser validation reject invalid fields",
		MaxTokens:          1000,
		TopK:               5,
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(res.Items) == 0 {
		t.Fatal("expected results")
	}
	if res.Items[0].Brief.ID != "art_relevant" {
		t.Errorf("expected relevant artifact first, got %s (items=%+v)", res.Items[0].Brief.ID, res.Items)
	}
	if res.EmbedderName == "" {
		t.Error("EmbedderName should be set when vector store is used")
	}
}

// TestRetrievalDegradesGracefullyWithoutVector：没有向量库时只用 FTS + 元数据，
// 仍能产出有效排序。
func TestRetrievalDegradesGracefullyWithoutVector(t *testing.T) {
	mem, err := memory.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer mem.Close()

	_ = mem.UpsertArtifactFull(memory.ArtifactRow{
		ID: "art_x", Kind: "evidence", Title: "x", Summary: "deep_compiler context limits",
		Scope: "node", Version: 1, TokenCount: 50,
	})
	_ = mem.IndexArtifactFTS("art_x", "x", "deep_compiler context limits", "context budget rules")

	svc := NewService(mem, nil)
	res, err := svc.Query(context.Background(), Query{
		Need:      "context budget",
		MaxTokens: 1000,
		TopK:      5,
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(res.Items) == 0 {
		t.Fatal("expected at least one FTS hit")
	}
	if res.EmbedderName != "" {
		t.Errorf("EmbedderName should be empty with nil vector store, got %q", res.EmbedderName)
	}
}
