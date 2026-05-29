package runtime

// artifact_getter.go — ArtifactGetter mini agentic-loop
//
// 设计原则：
//   - 和 main loop 用同一个 engine，但上下文完全隔离
//   - 只知道 artifact 元信息(ID/类型/总行数/summary) + goal + acceptance criteria
//   - 通过私有工具集（read_slice / search / done）主动探索 artifact
//   - 不接收全文，不污染 main loop 状态
//   - 最多 maxGetterTurns 轮；超时/失败退化到 resolver → 元信息占位

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	resolverPkg "github.com/Steve65535/llmvm/pkg/resolver"
	"github.com/Steve65535/llmvm/pkg/tasknode"
)

const (
	maxGetterTurns      = 5
	getterObsMaxChars   = 2000 // 单次 observation 截断上限（进 transcript）
	getterSummaryMaxLen = 1200 // done.summary 截断上限
	getterTimeout       = 60 * time.Second
)

// getterTool 是 ArtifactGetter 私有的最小 action 协议。
// 不复用 pkg/llm/parser.go（那是 main loop 的契约）。
type getterTool struct {
	Tool      string `json:"tool"`                 // "read_slice" | "search" | "done"
	StartLine int    `json:"start_line,omitempty"` // read_slice
	EndLine   int    `json:"end_line,omitempty"`   // read_slice
	Keyword   string `json:"keyword,omitempty"`    // search
	Summary   string `json:"summary,omitempty"`    // done
}

// getterObservation 是 getter transcript 里的一条记录。
type getterObservation struct {
	tool   string
	input  string // 工具参数描述
	output string // 截断后的结果
}

// runArtifactGetter 启动 ArtifactGetter mini-loop，在独立 goroutine 里运行，
// 主线程通过 channel 等待结果（带 getterTimeout 超时）。
//
// 返回：精确摘要字符串（写进调用方 history/variables）。
// 失败/超时时退化到 resolver 切片，再失败则返回元信息占位。
func (r *Runtime) runArtifactGetter(
	artifactID string,
	goal string,
	acCriteria []tasknode.AcceptanceCriterion,
	exitFailed bool,
) string {
	type result struct{ summary string }
	ch := make(chan result, 1)

	go func() {
		summary := r.artifactGetterLoop(artifactID, goal, acCriteria, exitFailed)
		ch <- result{summary}
	}()

	select {
	case res := <-ch:
		return res.summary
	case <-time.After(getterTimeout):
		fmt.Printf("  ⚠️  ArtifactGetter timeout (%s), falling back (artifact=%s)\n",
			getterTimeout, artifactID)
		return r.getterFallback(artifactID, goal)
	}
}

// artifactGetterLoop 是 getter 的 ReAct 主循环（在 goroutine 里运行）。
func (r *Runtime) artifactGetterLoop(
	artifactID string,
	goal string,
	acCriteria []tasknode.AcceptanceCriterion,
	exitFailed bool,
) string {
	art := r.artifacts.Get(artifactID)
	if art == nil {
		return fmt.Sprintf("[ArtifactGetter: artifact %s not found]", artifactID)
	}

	var transcript []getterObservation

	for turn := 0; turn < maxGetterTurns; turn++ {
		prompt := buildGetterPrompt(art.ID, art.Type, art.TotalLines, art.Summary,
			goal, acCriteria, exitFailed, transcript)

		output, err := r.engine.Call(prompt)
		if err != nil {
			fmt.Printf("  ⚠️  ArtifactGetter turn %d LLM error: %v\n", turn+1, err)
			break
		}

		tool, parseErr := parseGetterTool(output.Response)
		if parseErr != nil {
			fmt.Printf("  ⚠️  ArtifactGetter turn %d parse error: %v\n", turn+1, parseErr)
			break
		}

		fmt.Printf("  🔍 ArtifactGetter turn %d: tool=%s (artifact=%s)\n", turn+1, tool.Tool, artifactID)

		switch tool.Tool {
		case "done":
			summary := strings.TrimSpace(tool.Summary)
			if len(summary) > getterSummaryMaxLen {
				summary = summary[:getterSummaryMaxLen] + "..."
			}
			if summary == "" {
				break
			}
			fmt.Printf("  ✅ ArtifactGetter done: %d chars (artifact=%s)\n", len(summary), artifactID)
			return fmt.Sprintf("[ArtifactGetter:%s]\n%s", artifactID, summary)

		case "read_slice":
			start := tool.StartLine
			if start <= 0 {
				start = 1
			}
			end := tool.EndLine
			slice, err := r.artifacts.ReadSlice(artifactID, start, end)
			if err != nil {
				obs := getterObservation{tool: "read_slice",
					input:  fmt.Sprintf("lines %d-%d", start, end),
					output: fmt.Sprintf("[ERROR: %v]", err)}
				transcript = append(transcript, obs)
				continue
			}
			if len(slice) > getterObsMaxChars {
				slice = slice[:getterObsMaxChars] + "\n... [truncated]"
			}
			transcript = append(transcript, getterObservation{
				tool:   "read_slice",
				input:  fmt.Sprintf("lines %d-%d", start, end),
				output: slice,
			})

		case "search":
			hits := searchInArtifact(r, artifactID, tool.Keyword)
			if len(hits) > getterObsMaxChars {
				hits = hits[:getterObsMaxChars] + "\n... [truncated]"
			}
			transcript = append(transcript, getterObservation{
				tool:   "search",
				input:  tool.Keyword,
				output: hits,
			})

		default:
			fmt.Printf("  ⚠️  ArtifactGetter unknown tool %q, stopping\n", tool.Tool)
			break
		}
	}

	// 用尽轮次或提前退出 → 退化
	fmt.Printf("  ⚠️  ArtifactGetter exhausted turns, falling back (artifact=%s)\n", artifactID)
	return r.getterFallback(artifactID, goal)
}

// buildGetterPrompt 构造 ArtifactGetter 的隔离 prompt。
// 只包含 artifact 元信息 + goal + transcript，不含 main loop 任何状态。
func buildGetterPrompt(
	artifactID, artType string, totalLines int, artSummary string,
	goal string,
	acCriteria []tasknode.AcceptanceCriterion,
	exitFailed bool,
	transcript []getterObservation,
) string {
	var sb strings.Builder

	sb.WriteString("You are an ArtifactGetter agent. Your job is to extract the most relevant context from an artifact.\n\n")
	sb.WriteString("## Artifact\n")
	fmt.Fprintf(&sb, "- id: %s\n- type: %s\n- total_lines: %d\n- summary: %s\n",
		artifactID, artType, totalLines, artSummary)
	if exitFailed {
		sb.WriteString("- note: this artifact is from a FAILED command (non-zero exit)\n")
	}

	sb.WriteString("\n## Goal\n")
	sb.WriteString(goal + "\n")

	if len(acCriteria) > 0 {
		sb.WriteString("\n## Acceptance Criteria\n")
		for _, c := range acCriteria {
			fmt.Fprintf(&sb, "- [%s] %s\n", c.ID, c.Description)
		}
	}

	if len(transcript) > 0 {
		sb.WriteString("\n## Observations so far\n")
		for i, obs := range transcript {
			fmt.Fprintf(&sb, "### Turn %d: %s(%s)\n%s\n", i+1, obs.tool, obs.input, obs.output)
		}
	}

	sb.WriteString(`
## Your task
Explore the artifact using the tools below to find context most relevant to the goal.
Return ONLY a JSON object (no markdown, no explanation outside JSON).

Available tools:
- {"tool": "read_slice", "start_line": N, "end_line": M}   — read lines N to M
- {"tool": "search", "keyword": "..."}                      — search for keyword, returns matching lines with context
- {"tool": "done", "summary": "..."}                        — you have enough context; return a concise plain-text summary (max 1000 chars) of the parts most relevant to the goal

Rules:
- Start by reading a small slice to orient yourself (e.g. first 30 lines).
- Use search to locate errors, key symbols, or relevant sections.
- Call done as soon as you have enough context. Do NOT read the entire artifact.
- If the artifact is from a failed command, focus on error messages and failure causes.
- Return ONLY the JSON object for your chosen tool. No other text.
`)
	return sb.String()
}

// parseGetterTool 解析 ArtifactGetter 的私有 JSON 协议。
// 容忍 LLM 在 JSON 外包裹 markdown 代码块。
func parseGetterTool(raw string) (*getterTool, error) {
	s := strings.TrimSpace(raw)
	// 去掉 ```json ... ``` 包裹
	if idx := strings.Index(s, "{"); idx > 0 {
		s = s[idx:]
	}
	if idx := strings.LastIndex(s, "}"); idx >= 0 && idx < len(s)-1 {
		s = s[:idx+1]
	}
	var t getterTool
	if err := json.Unmarshal([]byte(s), &t); err != nil {
		return nil, fmt.Errorf("parseGetterTool: %w (raw: %.120s)", err, raw)
	}
	if t.Tool == "" {
		return nil, fmt.Errorf("parseGetterTool: missing tool field (raw: %.120s)", raw)
	}
	return &t, nil
}

// searchInArtifact 在 artifact 内容里做关键词搜索，返回命中行 ±2 行上下文。
// 纯本地，不调 grep（artifact 可能在内存或 spill 文件）。
func searchInArtifact(r *Runtime, artifactID, keyword string) string {
	if keyword == "" {
		return "[search: empty keyword]"
	}
	// 读全文（用 ReadSlice 1..0 = 全部）
	content, err := r.artifacts.ReadSlice(artifactID, 1, 0)
	if err != nil {
		return fmt.Sprintf("[search error: %v]", err)
	}
	lines := strings.Split(content, "\n")
	kw := strings.ToLower(keyword)

	const contextLines = 2
	const maxHits = 10
	seen := map[int]bool{}
	var result []string
	hitCount := 0

	for i, line := range lines {
		if !strings.Contains(strings.ToLower(line), kw) {
			continue
		}
		hitCount++
		if hitCount > maxHits {
			result = append(result, fmt.Sprintf("... (%d more matches omitted)", hitCount-maxHits))
			break
		}
		start := i - contextLines
		if start < 0 {
			start = 0
		}
		end := i + contextLines + 1
		if end > len(lines) {
			end = len(lines)
		}
		for j := start; j < end; j++ {
			if !seen[j] {
				seen[j] = true
				marker := "  "
				if j == i {
					marker = "> "
				}
				result = append(result, fmt.Sprintf("%s%4d: %s", marker, j+1, lines[j]))
			}
		}
		result = append(result, "---")
	}

	if len(result) == 0 {
		return fmt.Sprintf("[search: no matches for %q in artifact %s]", keyword, artifactID)
	}
	return strings.Join(result, "\n")
}

// getterFallback 退化链：resolver 关键词切片 → 元信息占位。
func (r *Runtime) getterFallback(artifactID, goal string) string {
	if r.resolver != nil && goal != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		rres, rerr := r.resolver.Resolve(ctx, resolverPkg.Request{
			ArtifactID:  artifactID,
			ContextNeed: goal,
			MaxTokens:   r.budget.ArtifactInlineTokenLimit,
		})
		cancel()
		if rerr == nil && len(rres.Evidence) > 0 {
			fmt.Printf("  🔬 ArtifactGetter fallback: resolver %d spans (artifact=%s)\n",
				len(rres.Evidence), artifactID)
			return formatResolvedSpans(artifactID, rres.Evidence)
		}
	}
	// 最终退化：元信息占位，调用方兜底截断
	art := r.artifacts.Get(artifactID)
	if art != nil {
		return fmt.Sprintf("[artifact=%s type=%s total_lines=%d summary=%s]",
			artifactID, art.Type, art.TotalLines, art.Summary)
	}
	return fmt.Sprintf("[artifact=%s: unavailable]", artifactID)
}

// summarizeLargeArtifact 是四条大输出路径的统一入口。
//
// 当 fullLen > threshold 且 goal 非空时，启动 ArtifactGetter mini-loop。
// 否则返回空字符串（调用方按原有逻辑处理）。
func (r *Runtime) summarizeLargeArtifact(
	artifactID string,
	fullLen int,
	goal string,
	acCriteria []tasknode.AcceptanceCriterion,
	exitFailed bool,
) string {
	threshold := r.budget.ArtifactAsyncTokenThreshold * 4
	if threshold <= 0 {
		threshold = MaxHistoryEntryLength
	}
	if fullLen <= threshold || goal == "" {
		return "" // 不触发 getter，调用方自行处理
	}
	return r.runArtifactGetter(artifactID, goal, acCriteria, exitFailed)
}
