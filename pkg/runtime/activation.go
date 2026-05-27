package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Steve65535/llmvm/pkg/memory"
	"github.com/Steve65535/llmvm/pkg/resolver"
	"github.com/Steve65535/llmvm/pkg/retrieval"
	"github.com/Steve65535/llmvm/pkg/tasknode"
)

// NodeBrief 节点摘要（用于 activation context）
type NodeBrief struct {
	ID          string
	Name        string
	Type        string
	Status      string
	Information string
	Depth       int
}

// NodeActivation 是每个节点激活前装配的工作上下文。
// 确定性构建，无额外 LLM 调用，预算感知。
type NodeActivation struct {
	NodeID          string
	HierarchyPath   []NodeBrief
	ParentGoal      string
	SiblingHandoffs []memory.HandoffBrief
	ArtifactIndex   []memory.ArtifactBrief
	OpenQuestions   []string
	WorkingContext  string
}

// buildNodeActivation 在每次节点 LLM 调用前装配激活上下文。
// 优先通过 SQLite 查询候选上下文；SQLite 不可用时回退到 AST 内存读取。
func (r *Runtime) buildNodeActivation(current *tasknode.TaskNode) NodeActivation {
	act := NodeActivation{NodeID: current.ID}

	// 1. 层级路径：优先 SQLite，回退 AST
	act.HierarchyPath = r.buildHierarchyPath(current)

	// 2. 父节点目标
	if current.Parent != nil {
		if current.Parent.Goal != "" {
			act.ParentGoal = current.Parent.Goal
		} else if len(current.Parent.Information) > 0 {
			act.ParentGoal = current.Parent.Information[0]
		}
	}

	// 3. 兄弟 handoff：优先 SQLite，回退 AST
	act.SiblingHandoffs = r.buildSiblingHandoffs(current)

	// 4. Artifact 索引：优先 SQLite（pinned 优先），回退 artifact store
	act.ArtifactIndex = r.buildArtifactIndex()

	// 5. 收集 open questions（来自兄弟 handoff）
	for _, h := range act.SiblingHandoffs {
		act.OpenQuestions = append(act.OpenQuestions, h.OpenQuestions...)
	}

	// 6. 构建 working context 字符串（预算感知）
	act.WorkingContext = r.formatActivationContext(act, current)

	return act
}

// buildHierarchyPath 从根到当前节点构建层级路径。
// 优先通过 SQLite QueryAncestorChain；SQLite 不可用时回退 AST 遍历。
func (r *Runtime) buildHierarchyPath(current *tasknode.TaskNode) []NodeBrief {
	// 尝试 SQLite
	if r.memStore != nil {
		chain, err := r.memStore.QueryAncestorChain(current.ID)
		if err == nil && len(chain) > 0 {
			briefs := make([]NodeBrief, len(chain))
			for i, n := range chain {
				briefs[i] = NodeBrief{
					ID:          n.ID,
					Name:        n.Name,
					Type:        n.Type,
					Status:      n.Status,
					Information: n.Information,
					Depth:       n.Depth,
				}
			}
			return briefs
		}
	}

	// 回退：AST 遍历
	var path []*tasknode.TaskNode
	node := current
	for node != nil {
		path = append([]*tasknode.TaskNode{node}, path...)
		node = node.Parent
	}
	var briefs []NodeBrief
	for depth, n := range path {
		briefs = append(briefs, NodeBrief{
			ID:          n.ID,
			Name:        n.Name,
			Type:        nodeTypeStr(n.Type),
			Status:      nodeStatusStr(n.Status),
			Information: firstInfo(n.Information),
			Depth:       depth,
		})
	}
	return briefs
}

// buildSiblingHandoffs 收集同父节点下已完成/失败兄弟的结构化 handoff（最多 3 个）。
// 优先通过 SQLite QuerySiblingHandoffs；SQLite 不可用时回退 AST 遍历。
func (r *Runtime) buildSiblingHandoffs(current *tasknode.TaskNode) []memory.HandoffBrief {
	if current.Parent == nil {
		return nil
	}

	// 尝试 SQLite
	if r.memStore != nil {
		handoffs, err := r.memStore.QuerySiblingHandoffs(
			current.Parent.ID, current.ID,
			[]string{"Completed", "Failed"}, 3,
		)
		if err == nil {
			// SQLite 有数据时直接返回
			if len(handoffs) > 0 {
				return handoffs
			}
		}
	}

	// 回退：AST 遍历
	var result []memory.HandoffBrief
	count := 0
	for _, sibling := range current.Parent.Children {
		if count >= 3 {
			break
		}
		if sibling.ID == current.ID {
			continue
		}
		if !sibling.WetherFinished && sibling.Status != tasknode.Failed {
			continue
		}
		b := memory.HandoffBrief{
			NodeID:        sibling.ID,
			Goal:          sibling.Goal,
			Summary:       sibling.Summary,
			KeyFacts:      sibling.KeyFacts,
			Decisions:     sibling.Decisions,
			ArtifactRefs:  sibling.ArtifactRefs,
			OpenQuestions: sibling.OpenQuestions,
			Handoff:       sibling.Handoff,
			Confidence:    sibling.Confidence,
		}
		if b.Summary == "" {
			b.Summary = sibling.Result
		}
		result = append(result, b)
		count++
	}
	return result
}

// buildArtifactIndex 构建 artifact 索引（pinned 优先，最多 20 条）。
// 优先通过 SQLite；SQLite 不可用时回退 artifact store。
func (r *Runtime) buildArtifactIndex() []memory.ArtifactBrief {
	if r.memStore != nil {
		// 先取 pinned，再取 recent，合并去重
		pinned, err1 := r.memStore.QueryPinnedArtifacts(10)
		recent, err2 := r.memStore.QueryRecentArtifacts(20)
		if err1 == nil && err2 == nil {
			seen := map[string]bool{}
			var combined []memory.ArtifactBrief
			for _, a := range pinned {
				if !seen[a.ID] {
					seen[a.ID] = true
					combined = append(combined, a)
				}
			}
			for _, a := range recent {
				if !seen[a.ID] {
					seen[a.ID] = true
					combined = append(combined, a)
				}
			}
			if len(combined) > 20 {
				combined = combined[:20]
			}
			if len(combined) > 0 {
				return combined
			}
		}
	}

	// 回退：artifact store
	arts := r.artifacts.ListAll()
	var pinned, rest []memory.ArtifactBrief
	for _, a := range arts {
		b := memory.ArtifactBrief{
			ID:             a.ID,
			ProducerNodeID: a.CreatedBy,
			Kind:           a.Type,
			Title:          a.Source,
			Summary:        a.Summary,
			Pinned:         a.Pinned,
		}
		if a.Pinned {
			pinned = append(pinned, b)
		} else {
			rest = append(rest, b)
		}
	}
	combined := append(pinned, rest...)
	if len(combined) > 20 {
		combined = combined[:20]
	}
	return combined
}

// formatActivationContext 将 NodeActivation 格式化为 prompt 字符串。
//
// 文档 2 取消固定比例预算：context pack 只受总预算软约束（在 buildPromptInternalV2 末段
// 整体裁剪），单段不再有硬上限。Context Pack Builder 按相关性优先填充。
func (r *Runtime) formatActivationContext(act NodeActivation, current *tasknode.TaskNode) string {
	var sb strings.Builder

	// 1. 层级路径
	sb.WriteString("## Hierarchy Path\n")
	for _, n := range act.HierarchyPath {
		prefix := strings.Repeat("  ", n.Depth)
		marker := "→"
		if n.ID == current.ID {
			marker = "★ (current)"
		}
		sb.WriteString(fmt.Sprintf("%s[%s] %s (%s) %s\n", prefix, n.ID, n.Name, n.Status, marker))
	}

	// 2. 父节点目标
	if act.ParentGoal != "" {
		sb.WriteString("\n## Parent Goal\n")
		sb.WriteString(act.ParentGoal + "\n")
	}

	// 3. 兄弟 handoff
	if len(act.SiblingHandoffs) > 0 {
		sb.WriteString("\n## Sibling Handoffs\n")
		sb.WriteString(formatSiblingHandoffs(act.SiblingHandoffs))
	}

	// 4. Artifact 索引（不再硬限字符数；rerank + 全局预算共同决定）
	sb.WriteString("\n## Available Artifacts\n")
	sb.WriteString(formatArtifactBriefs(act.ArtifactIndex, 0))

	// 5. 树索引：超过软目标后切换到只显示祖先 + 兄弟 + 最近完成节点的裁剪版
	root := r.cursor.GetRoot()
	if root != nil {
		sb.WriteString("\n## Tree Index\n")
		treeIdx := r.getTreeIndex(root, 0)
		// 软阈值：tree index 不应吞掉超过总预算字符的 1/8
		softLimit := r.budget.ContextTokenLimit * 4 / 8
		if softLimit > 0 && len(treeIdx) > softLimit {
			treeIdx = r.getRelevantTreeIndex(current, softLimit)
		}
		sb.WriteString(treeIdx)
	}

	return sb.String()
}

// formatSiblingHandoffs 格式化兄弟 handoff 列表。
func formatSiblingHandoffs(handoffs []memory.HandoffBrief) string {
	var sb strings.Builder
	for _, h := range handoffs {
		sb.WriteString(fmt.Sprintf("[%s]", h.NodeID))
		if h.Goal != "" {
			sb.WriteString(fmt.Sprintf(" Goal: %s", h.Goal))
		}
		sb.WriteString("\n")
		if h.Summary != "" {
			sb.WriteString(fmt.Sprintf("  Summary: %s\n", h.Summary))
		}
		if len(h.KeyFacts) > 0 {
			sb.WriteString(fmt.Sprintf("  KeyFacts: %s\n", strings.Join(h.KeyFacts, "; ")))
		}
		if len(h.Decisions) > 0 {
			sb.WriteString(fmt.Sprintf("  Decisions: %s\n", strings.Join(h.Decisions, "; ")))
		}
		if len(h.ArtifactRefs) > 0 {
			sb.WriteString(fmt.Sprintf("  ArtifactRefs: %s\n", strings.Join(h.ArtifactRefs, ", ")))
		}
		if h.Handoff != "" {
			sb.WriteString(fmt.Sprintf("  Handoff: %s\n", h.Handoff))
		}
		if h.Confidence != "" {
			sb.WriteString(fmt.Sprintf("  Confidence: %s\n", h.Confidence))
		}
	}
	return sb.String()
}

// formatArtifactBriefs 格式化 artifact 摘要列表（预算感知）。
func formatArtifactBriefs(briefs []memory.ArtifactBrief, maxChars int) string {
	if len(briefs) == 0 {
		return "No artifacts yet.\n"
	}
	var sb strings.Builder
	for i, b := range briefs {
		pinTag := ""
		if b.Pinned {
			pinTag = " [PINNED]"
		}
		line := fmt.Sprintf("- %s (%s) %s%s — %s\n", b.ID, b.Kind, b.Title, pinTag, b.Summary)
		if maxChars > 0 && sb.Len()+len(line) > maxChars {
			sb.WriteString(fmt.Sprintf("... (%d more artifacts omitted)\n", len(briefs)-i))
			break
		}
		sb.WriteString(line)
	}
	return sb.String()
}

// firstInfo 返回 information 列表的第一个元素，或空字符串。
func firstInfo(info []string) string {
	if len(info) > 0 {
		return info[0]
	}
	return ""
}

// === ContextPack pipeline (Phase 10) ===

// ContextPack 是节点激活后的最终上下文打包结果。
//
// 流程（按文档 2）：
//   BuildPosition(node)
//     -> buildNodeActivation(node)        （deterministic 字段：祖先链、handoff、artifact 索引）
//     -> retrievalSvc.Query(...)          （混合检索 + rerank，Leaf 节点必走）
//     -> resolver.Resolve(...) for big artifacts
//     -> AssembleContextPack
//
// ContextPack 不替代 NodeActivation，它在 Activation 之上加了一层"按 position 决定收什么"。
type ContextPack struct {
	PositionSummary string
	Activation      NodeActivation
	Retrieved       []retrieval.RetrievedItem
	ResolvedSpans   []resolver.EvidenceSpan
	Omissions       []string
	BudgetUsed      int
	WorkingContext  string // 最终 prompt 字符串（兼容老路径）
}

// BuildContextPack 是 buildGlobalContext 的"位置工程版"：根据 Position 决定要不要走 retrieval。
//
// 第一版规则（确定性，不调 LLM 规划检索）：
//   - Root：只读 artifact 索引 + 顶层子节点 handoff，不做语义召回（顶层不该被噪声污染）
//   - Planner / Executor / ErrorHandler：如果节点有 Goal 或 acceptance criteria，自动跑一次
//     retrieval 召回相关 artifact，并对超过阈值的 artifact 调 resolver 缩小
//
// 不改变 cursor 或 LLM 调用次数：retrieval/resolver 都是本地索引操作。
func (r *Runtime) BuildContextPack(current *tasknode.TaskNode, pos Position) ContextPack {
	pack := ContextPack{
		PositionSummary: FormatPositionSummary(pos),
		Activation:      r.buildNodeActivation(current),
	}

	// Root 不跑语义召回（避免顶层被污染）
	if pos.Role == RoleRoot || r.retrieve == nil {
		pack.WorkingContext = pack.Activation.WorkingContext
		return pack
	}

	// 收集查询信号
	goal := current.Goal
	if goal == "" && len(current.Information) > 0 {
		goal = current.Information[0]
	}
	if goal == "" {
		// 没有目标信号 → 不跑 retrieval，避免无意义召回
		pack.WorkingContext = pack.Activation.WorkingContext
		return pack
	}

	var acDesc []string
	for _, c := range current.AcceptanceCriteria {
		acDesc = append(acDesc, c.Description)
	}

	q := retrieval.Query{
		NodeID:             current.ID,
		Goal:               goal,
		AcceptanceCriteria: acDesc,
		Need:               goal,
		ScopeFilter:        pos.Scope.DefaultArtifactScope,
		MaxTokens:          r.budget.RetrievalTokenLimit,
		TopK:               10,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := r.retrieve.Query(ctx, q)
	if err != nil {
		pack.Omissions = append(pack.Omissions, fmt.Sprintf("retrieval failed: %v", err))
		pack.WorkingContext = pack.Activation.WorkingContext
		return pack
	}
	pack.Retrieved = res.Items
	pack.BudgetUsed = res.BudgetUsed

	// 对超过 ArtifactInlineTokenLimit 的命中调 resolver
	if r.resolver != nil && r.budget.ArtifactInlineTokenLimit > 0 {
		threshold := r.budget.ArtifactInlineTokenLimit
		for _, it := range res.Items {
			if it.Brief.TokenCount <= threshold {
				continue
			}
			rctx, rcancel := context.WithTimeout(context.Background(), 15*time.Second)
			rres, rerr := r.resolver.Resolve(rctx, resolver.Request{
				ArtifactID:         it.Brief.ID,
				NodeID:             current.ID,
				CurrentGoal:        goal,
				AcceptanceCriteria: acDesc,
				ContextNeed:        goal,
				MaxTokens:          r.budget.ArtifactInlineTokenLimit,
			})
			rcancel()
			if rerr != nil {
				pack.Omissions = append(pack.Omissions, fmt.Sprintf("resolver(%s): %v", it.Brief.ID, rerr))
				continue
			}
			pack.ResolvedSpans = append(pack.ResolvedSpans, rres.Evidence...)
		}
	}

	// 拼最终 working context
	pack.WorkingContext = formatContextPack(pack)
	return pack
}

// formatContextPack 拼装 ContextPack 到 prompt 友好的字符串。
func formatContextPack(p ContextPack) string {
	var sb strings.Builder
	sb.WriteString(p.Activation.WorkingContext)

	if len(p.Retrieved) > 0 {
		sb.WriteString("\n## Retrieved (rerank top results)\n")
		sb.WriteString(fmt.Sprintf("budget_used=%d tokens\n", p.BudgetUsed))
		for i, it := range p.Retrieved {
			fmt.Fprintf(&sb, "%d. [%s] %s — score=%.3f %s\n",
				i+1, it.Brief.ID, it.Brief.Title, it.FinalScore, it.Rationale)
			if it.Brief.Summary != "" {
				fmt.Fprintf(&sb, "   %s\n", it.Brief.Summary)
			}
		}
	}
	if len(p.ResolvedSpans) > 0 {
		sb.WriteString("\n## Resolved Evidence (large artifacts compacted)\n")
		for _, ev := range p.ResolvedSpans {
			fmt.Fprintf(&sb, "lines %d-%d (%s):\n", ev.StartLine, ev.EndLine, ev.Rationale)
			body := ev.Text
			if len(body) > 1500 {
				body = body[:1500] + "..."
			}
			fmt.Fprintf(&sb, "%s\n\n", body)
		}
	}
	if len(p.Omissions) > 0 {
		sb.WriteString("\n## Omissions\n")
		for _, o := range p.Omissions {
			fmt.Fprintf(&sb, "- %s\n", o)
		}
	}
	return sb.String()
}
