// Package resolver 处理"大 artifact 异步缩小"流程。
//
// 文档 2 设计：当 retrieval 命中的 artifact 超过 LLMVM_ARTIFACT_ASYNC_TOKEN_THRESHOLD 时，
// 不能整段塞进主上下文。Resolver 起一个独立流程读全文 / chunk-level FTS / 文件片段定位，
// 返回一份"足够小、足够精确"的 evidence slice 给主节点。
//
// 第一版（本文件）：
//   - 同步接口（resolver.Resolve(...)）
//   - 不调 LLM；用 chunked window + sanitize FTS 在大 artifact 内部做关键词命中
//   - 命中片段按行号排序合并，预算裁剪到 MaxTokens
//   - 接口预留了 goroutine 化的形态（Async）；主流程当前调同步版避免一致性问题
//
// 真正的 LLM 精读压缩留待第二版（需要把 engine 注入 resolver）。
package resolver

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Steve65535/llmvm/pkg/artifact"
)

// Request 是单次 resolve 的输入。
type Request struct {
	ArtifactID         string
	NodeID             string
	CurrentGoal        string
	AcceptanceCriteria []string // 验收标准的 description 列表
	ContextNeed        string   // 当前需要从大 artifact 里取出来的信息（自然语言）
	MaxTokens          int      // evidence slice 的 token 上限
}

// EvidenceSpan 一段命中的 evidence。
type EvidenceSpan struct {
	StartLine int
	EndLine   int
	Text      string
	Score     float64
	Rationale string
}

// Result 是一次 resolve 的全部输出。
type Result struct {
	ArtifactID string
	Summary    string
	Evidence   []EvidenceSpan
	Confidence float64 // 0~1，按命中密度估算
	Truncated  bool    // true 表示因为预算裁剪了 evidence
}

// Resolver 持有 artifact store。
type Resolver struct {
	Artifacts *artifact.Store
}

func New(arts *artifact.Store) *Resolver {
	return &Resolver{Artifacts: arts}
}

// Resolve 同步入口。
//
// 步骤：
//  1. 取出 artifact 全文（spill 也读）
//  2. 把 ContextNeed 和验收标准的关键词分词
//  3. 滑窗扫描，按命中密度打分
//  4. top 几段合并，预算裁剪
//  5. 输出 Summary（命中关键词 + 命中段计数）
func (r *Resolver) Resolve(_ context.Context, req Request) (*Result, error) {
	if r.Artifacts == nil {
		return nil, fmt.Errorf("resolver: artifact store unavailable")
	}
	art := r.Artifacts.Get(req.ArtifactID)
	if art == nil {
		return nil, fmt.Errorf("resolver: artifact %q not found", req.ArtifactID)
	}
	if art.Evicted {
		return nil, fmt.Errorf("resolver: artifact %q has been evicted (only summary remains: %s)",
			req.ArtifactID, art.Summary)
	}

	// 关键词集合：ContextNeed + 验收标准
	keywords := tokenize(req.ContextNeed)
	for _, ac := range req.AcceptanceCriteria {
		keywords = append(keywords, tokenize(ac)...)
	}
	keywords = uniq(keywords)
	if len(keywords) == 0 {
		// 无关键词时退化为返回前 N 行
		fullSlice, err := r.Artifacts.ReadSlice(req.ArtifactID, 1, 80)
		if err != nil {
			return nil, err
		}
		return &Result{
			ArtifactID: req.ArtifactID,
			Summary:    fmt.Sprintf("no keywords; returning head 80 lines of %s", art.Source),
			Evidence: []EvidenceSpan{{
				StartLine: 1, EndLine: 80, Text: fullSlice, Score: 0,
				Rationale: "head-fallback",
			}},
			Confidence: 0.2,
		}, nil
	}

	// 读全文
	full, err := r.Artifacts.ReadSlice(req.ArtifactID, 1, art.TotalLines+1)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(full, "\n")

	// 滑窗：每窗 30 行，步长 15，计算命中密度
	const windowSize = 30
	const step = 15
	var wins []resolveWindow
	seen := make(map[string]bool)
	for start := 0; start < len(lines); start += step {
		end := start + windowSize
		if end > len(lines) {
			end = len(lines)
		}
		body := strings.ToLower(strings.Join(lines[start:end], "\n"))
		hits := 0
		for k := range seen {
			delete(seen, k)
		}
		for _, kw := range keywords {
			if strings.Contains(body, kw) {
				hits++
				seen[kw] = true
			}
		}
		if hits > 0 {
			wins = append(wins, resolveWindow{
				start: start, end: end, hits: hits, uniqKW: len(seen),
			})
		}
		if end >= len(lines) {
			break
		}
	}

	if len(wins) == 0 {
		return &Result{
			ArtifactID: req.ArtifactID,
			Summary:    fmt.Sprintf("no keyword hits in %s for need=%q", art.Source, req.ContextNeed),
			Evidence:   nil,
			Confidence: 0,
		}, nil
	}

	// 按 hits 倒序，挑前几个；再按行号正序合并相邻窗
	sort.Slice(wins, func(i, j int) bool {
		if wins[i].hits != wins[j].hits {
			return wins[i].hits > wins[j].hits
		}
		return wins[i].uniqKW > wins[j].uniqKW
	})
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4000
	}
	// token 估算：每行 ~10 tokens
	const tokensPerLine = 10

	var picked []resolveWindow
	usedTokens := 0
	for _, w := range wins {
		span := (w.end - w.start) * tokensPerLine
		if usedTokens+span > maxTokens && len(picked) > 0 {
			break
		}
		picked = append(picked, w)
		usedTokens += span
	}
	sort.Slice(picked, func(i, j int) bool { return picked[i].start < picked[j].start })

	// 合并相邻 / 重叠窗
	merged := mergeWindows(picked)

	var evidence []EvidenceSpan
	for _, w := range merged {
		text := strings.Join(lines[w.start:w.end], "\n")
		evidence = append(evidence, EvidenceSpan{
			StartLine: w.start + 1,
			EndLine:   w.end,
			Text:      text,
			Score:     float64(w.hits),
			Rationale: fmt.Sprintf("%d keyword hits, %d unique", w.hits, w.uniqKW),
		})
	}

	conf := float64(len(picked)) / float64(len(wins))
	if conf > 1 {
		conf = 1
	}

	summary := fmt.Sprintf("%s: %d windows scanned, %d picked (%d tokens budget=%d)",
		art.Source, len(wins), len(merged), usedTokens, maxTokens)

	return &Result{
		ArtifactID: req.ArtifactID,
		Summary:    summary,
		Evidence:   evidence,
		Confidence: conf,
		Truncated:  len(picked) < len(wins),
	}, nil
}

// Async 占位接口：第一版直接调 Resolve；保留签名方便后续切到 goroutine。
func (r *Resolver) Async(ctx context.Context, req Request) <-chan *Result {
	ch := make(chan *Result, 1)
	go func() {
		res, err := r.Resolve(ctx, req)
		if err != nil {
			ch <- &Result{ArtifactID: req.ArtifactID, Summary: "resolver error: " + err.Error()}
		} else {
			ch <- res
		}
		close(ch)
	}()
	return ch
}

// resolveWindow 表示 artifact 文本上的一段命中窗。
type resolveWindow struct {
	start, end, hits, uniqKW int
}

func mergeWindows(in []resolveWindow) []resolveWindow {
	if len(in) == 0 {
		return in
	}
	out := []resolveWindow{in[0]}
	for _, w := range in[1:] {
		last := &out[len(out)-1]
		if w.start <= last.end {
			if w.end > last.end {
				last.end = w.end
			}
			last.hits += w.hits
			if w.uniqKW > last.uniqKW {
				last.uniqKW = w.uniqKW
			}
		} else {
			out = append(out, w)
		}
	}
	return out
}

func tokenize(s string) []string {
	var out []string
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r > 127:
			b.WriteRune(r)
		default:
			if b.Len() >= 4 { // 至少 4 字符的 token 才有信息量
				out = append(out, b.String())
			}
			b.Reset()
		}
	}
	if b.Len() >= 4 {
		out = append(out, b.String())
	}
	return out
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
