package resolver

import (
	"context"
	"strings"
	"testing"

	"github.com/Steve65535/llmvm/pkg/artifact"
)

// TestResolveFindsKeywordWindow：大 artifact 内有命中段时，resolver 应返回
// 包含命中行的 evidence span，且把不相关的窗口排除在外。
func TestResolveFindsKeywordWindow(t *testing.T) {
	store := artifact.New()

	// 构造一个 200 行的 artifact，关键词只在第 80-90 行出现
	var lines []string
	for i := 1; i <= 200; i++ {
		lines = append(lines, "filler line about session auth flow line")
	}
	lines[80] = "parser must reject add_artifact with missing artifact_name field"
	lines[81] = "the validation rule rejects empty content"
	lines[82] = "structured error returned to caller"
	body := strings.Join(lines, "\n")

	art := store.Add("evidence", "parser_long_test", body, "n1")
	r := New(store)

	res, err := r.Resolve(context.Background(), Request{
		ArtifactID:  art.ID,
		NodeID:      "n1",
		ContextNeed: "parser validation reject add_artifact",
		MaxTokens:   2000,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Evidence) == 0 {
		t.Fatal("expected at least one evidence span")
	}
	// 至少一个 span 必须覆盖命中区域
	covered := false
	for _, ev := range res.Evidence {
		if ev.StartLine <= 81 && ev.EndLine >= 81 {
			covered = true
			break
		}
	}
	if !covered {
		t.Errorf("expected evidence around line 81, got %+v", res.Evidence)
	}
	if res.Confidence == 0 {
		t.Error("expected non-zero confidence when keyword hits exist")
	}
}

// TestResolveNoMatchReturnsEmptyEvidence：完全没命中时不应崩，返回空 evidence。
func TestResolveNoMatchReturnsEmptyEvidence(t *testing.T) {
	store := artifact.New()
	body := strings.Repeat("nothing relevant here\n", 100)
	art := store.Add("evidence", "unrelated", body, "n1")

	res, err := New(store).Resolve(context.Background(), Request{
		ArtifactID:  art.ID,
		ContextNeed: "parser validation specific term that does not appear",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Evidence) != 0 {
		t.Errorf("expected no evidence, got %d spans", len(res.Evidence))
	}
	if res.Confidence != 0 {
		t.Errorf("expected zero confidence, got %.2f", res.Confidence)
	}
}

// TestResolveRejectsMissingArtifact：未知 artifact ID 应返回错误。
func TestResolveRejectsMissingArtifact(t *testing.T) {
	r := New(artifact.New())
	_, err := r.Resolve(context.Background(), Request{
		ArtifactID:  "art_does_not_exist",
		ContextNeed: "x",
	})
	if err == nil {
		t.Fatal("expected error for missing artifact")
	}
}
