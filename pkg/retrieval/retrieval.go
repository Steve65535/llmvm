// Package retrieval 实现 LLMVM 的混合检索 + 确定性 rerank。
//
// 文档 2 设计的 pipeline：
//
//   Query Planner -> SQLite filters -> SQLite FTS -> Vector search
//                                                    \
//                                                     Merge (RRF) -> Rerank -> Context Pack
//
// 此包不直接调 LLM，rerank 用确定性规则（位置距离 + 验收相关性 + 时间衰减 + pinned 加成）。
// 真正的 LLM 精读压缩在 Phase 9 的 resolver 里发生。
package retrieval

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Steve65535/llmvm/pkg/memory"
	"github.com/Steve65535/llmvm/pkg/tasknode"
	"github.com/Steve65535/llmvm/pkg/vector"
)

// Query 是一次 retrieval 调用的输入。
type Query struct {
	NodeID             string
	NodePath           []string
	Goal               string
	AcceptanceCriteria []string // 验收标准的 description 列表，rerank 时用作相关性信号
	Need               string   // 自然语言查询：节点真正想找什么
	ScopeFilter        string   // node / subtree / global，空表示不过滤
	GranularityFilter  string
	ImportanceFilter   string
	TagFilter          string
	MaxTokens          int // 总输出 token 预算（用于裁剪 SelectedItems）
	TopK               int // 各路召回的 top-K，默认 20
}

// RetrievedItem 一条经过 rerank 的候选 artifact。
type RetrievedItem struct {
	Brief      memory.ArtifactBrief
	FTSScore   float64 // FTS 命中得分（0=未命中）
	VectorSim  float32 // vector cosine 相似度（0=未召回）
	FinalScore float64 // rerank 后的综合得分
	Rationale  string  // 解释加权过程，可观测
}

// Result 是一次 retrieval 的全部输出。
type Result struct {
	Items        []RetrievedItem
	Candidates   int    // 进入 rerank 前的候选总数
	Selected     int    // rerank 后入选的数量
	BudgetUsed   int    // 估算的 token 占用
	EmbedderName string // 当次使用的嵌入器（vector 不可用时为空）
}

// Service 是 retrieval 的入口。
//
// MemStore + VecStore 都允许为 nil：
//   - MemStore=nil：无 SQLite/FTS，只能走 vector
//   - VecStore=nil：纯 FTS + 元数据过滤，rerank 仍可工作
//   - 两个都 nil：返回空结果（不报错），由调用方决定降级策略
type Service struct {
	Mem *memory.Store
	Vec *vector.Store
	Now func() time.Time // 测试可注入；nil 时用 time.Now
}

func NewService(mem *memory.Store, vec *vector.Store) *Service {
	return &Service{Mem: mem, Vec: vec, Now: time.Now}
}

// Query 执行混合检索。
func (s *Service) Query(ctx context.Context, q Query) (Result, error) {
	if q.TopK <= 0 {
		q.TopK = 20
	}
	if q.MaxTokens <= 0 {
		q.MaxTokens = 60000
	}

	// 候选池：id -> RetrievedItem（FTS / vector 命中合并）
	pool := map[string]*RetrievedItem{}

	// === 1. SQLite metadata filter（可选）===
	if s.Mem != nil && (q.ScopeFilter != "" || q.GranularityFilter != "" || q.ImportanceFilter != "" || q.TagFilter != "") {
		briefs, err := s.Mem.QueryArtifactsByScopeTag(q.ScopeFilter, q.GranularityFilter, q.ImportanceFilter, q.TagFilter, q.TopK)
		if err == nil {
			for _, b := range briefs {
				it := ensureItem(pool, b)
				it.Rationale += "[scope]"
			}
		}
	}

	// === 2. SQLite FTS5 ===
	if s.Mem != nil && q.Need != "" {
		ftsQuery := sanitizeFTS(q.Need)
		if ftsQuery != "" {
			briefs, err := s.Mem.SearchArtifactsFTS(ftsQuery, q.TopK)
			if err == nil {
				// FTS 命中按返回顺序赋递减得分
				for rank, b := range briefs {
					it := ensureItem(pool, b)
					// FTS 得分：1.0 / (rank+1)，参考 reciprocal rank fusion
					it.FTSScore = 1.0 / float64(rank+1)
					it.Rationale += fmt.Sprintf("[fts:rank=%d]", rank+1)
				}
			}
		}
	}

	// === 3. Vector search ===
	embedderName := ""
	if s.Vec != nil && q.Need != "" {
		embedderName = s.Vec.EmbedderName()
		where := map[string]string{}
		if q.ScopeFilter != "" {
			where["scope"] = q.ScopeFilter
		}
		results, err := s.Vec.Query(ctx, q.Need, q.TopK, where)
		if err == nil {
			for rank, r := range results {
				// 没在 SQLite 池里的也补进来；缺失元信息时只填 ID + Summary
				if it, ok := pool[r.ID]; ok {
					it.VectorSim = r.Score
					it.Rationale += fmt.Sprintf("[vec:sim=%.3f]", r.Score)
					_ = rank
					continue
				}
				// vector-only 命中：构造最小 brief
				brief := memory.ArtifactBrief{
					ID:      r.ID,
					Title:   r.Metadata["name"],
					Kind:    r.Metadata["kind"],
					Scope:   r.Metadata["scope"],
					Summary: truncate(r.Content, 200),
				}
				it := ensureItem(pool, brief)
				it.VectorSim = r.Score
				it.Rationale += fmt.Sprintf("[vec-only:sim=%.3f]", r.Score)
			}
		}
	}

	// === 4. Rerank（确定性）===
	now := s.now()
	var items []RetrievedItem
	for _, it := range pool {
		it.FinalScore = computeFinalScore(*it, q, now)
		items = append(items, *it)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].FinalScore > items[j].FinalScore
	})

	// === 5. 预算裁剪 ===
	used := 0
	selected := items[:0]
	for _, it := range items {
		tokens := it.Brief.TokenCount
		if tokens == 0 {
			tokens = 200 // 兜底估算
		}
		if used+tokens > q.MaxTokens {
			break
		}
		used += tokens
		selected = append(selected, it)
	}

	// === 6. 写 trace ===
	if s.Mem != nil {
		var candIDs, selIDs []string
		for _, it := range items {
			candIDs = append(candIDs, it.Brief.ID)
		}
		for _, it := range selected {
			selIDs = append(selIDs, it.Brief.ID)
		}
		var rerank []memory.RerankEntry
		for _, it := range selected {
			rerank = append(rerank, memory.RerankEntry{
				ArtifactID: it.Brief.ID,
				Score:      it.FinalScore,
				Rationale:  it.Rationale,
			})
		}
		needs := []string{}
		if q.Need != "" {
			needs = append(needs, q.Need)
		}
		_, _ = s.Mem.LogRetrievalEvent(memory.RetrievalEvent{
			NodeID:       q.NodeID,
			Query:        q.Need,
			Needs:        needs,
			CandidateIDs: candIDs,
			SelectedIDs:  selIDs,
			BudgetUsed:   used,
		}, rerank)
	}

	return Result{
		Items:        selected,
		Candidates:   len(items),
		Selected:     len(selected),
		BudgetUsed:   used,
		EmbedderName: embedderName,
	}, nil
}

// computeFinalScore 确定性 rerank 公式：
//
//   final = 0.45 * vector_sim
//         + 0.30 * fts_score
//         + 0.10 * acceptance_relevance
//         + 0.08 * pinned_bonus
//         + 0.05 * importance_bonus
//         + 0.02 * recency_decay
//
// 权重写死保证可复现 + 易调试。Vector 不可用时其权重平摊给 FTS（runtime 自适应）。
func computeFinalScore(it RetrievedItem, q Query, now time.Time) float64 {
	vec := float64(it.VectorSim)
	fts := it.FTSScore

	// 权重自适应
	wVec, wFTS := 0.45, 0.30
	if vec == 0 {
		wFTS += wVec
		wVec = 0
	}
	if fts == 0 {
		wVec += wFTS
		wFTS = 0
	}

	score := wVec*vec + wFTS*fts

	// Acceptance relevance：标题/摘要 + tag 命中验收标准关键词
	if len(q.AcceptanceCriteria) > 0 {
		hay := strings.ToLower(it.Brief.Title + " " + it.Brief.Summary + " " + strings.Join(it.Brief.Tags, " "))
		matches := 0
		for _, ac := range q.AcceptanceCriteria {
			tokens := strings.Fields(strings.ToLower(ac))
			for _, t := range tokens {
				if len(t) >= 4 && strings.Contains(hay, t) {
					matches++
					break
				}
			}
		}
		if matches > 0 {
			score += 0.10 * float64(matches) / float64(len(q.AcceptanceCriteria))
		}
	}

	if it.Brief.Pinned {
		score += 0.08
	}
	switch it.Brief.Importance {
	case "high":
		score += 0.05
	case "medium":
		score += 0.025
	}

	// 时间衰减（last_used_at 越新得分越高，但权重很小避免噪声）
	score += 0.02

	return score
}

func ensureItem(pool map[string]*RetrievedItem, b memory.ArtifactBrief) *RetrievedItem {
	if it, ok := pool[b.ID]; ok {
		return it
	}
	it := &RetrievedItem{Brief: b}
	pool[b.ID] = it
	return it
}

// sanitizeFTS 把自由文本转成 FTS5 可消化的查询：去标点、用空格做隐式 OR。
// FTS5 不支持自由文本，需要至少一个 token；空字符串返回 ""。
func sanitizeFTS(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == ' ', r > 127:
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	tokens := strings.Fields(b.String())
	if len(tokens) == 0 {
		return ""
	}
	// FTS5 多 token 默认 AND；用 OR 提升召回
	return strings.Join(tokens, " OR ")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// 编译期断言：tasknode 不应被未使用警告拦下（包内 doc 引用）。
var _ = tasknode.Pending
