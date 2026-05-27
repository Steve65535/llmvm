package runtime

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Steve65535/llmvm/pkg/tasknode"
)

// buildGlobalContext 由 Runtime 自动组装全局上下文（0 额外 LLM 调用）。
//
// 现在委托给 BuildContextPack：先 BuildPosition，再走 retrieval + resolver pipeline。
// 同时把 current 节点状态同步到 SQLite index，保证 query_memory 一致。
func (r *Runtime) buildGlobalContext(current *tasknode.TaskNode) string {
	pos := BuildPosition(current)
	pack := r.BuildContextPack(current, pos)
	r.syncNodeToMemory(current)
	return pack.WorkingContext
}

// getRelevantTreeIndex 大树裁剪：只展示祖先链 + 兄弟 + 最近完成节点。
//
// 在 formatActivationContext 里被调用：当全树索引超过软阈值时切换到这个版本。
func (r *Runtime) getRelevantTreeIndex(current *tasknode.TaskNode, budget int) string {
	ancestorIDs := make(map[string]bool)
	node := current
	for node != nil {
		ancestorIDs[node.ID] = true
		node = node.Parent
	}

	siblingIDs := make(map[string]bool)
	if current.Parent != nil {
		for _, s := range current.Parent.Children {
			siblingIDs[s.ID] = true
		}
	}

	var recentFinished []*tasknode.TaskNode
	root := r.cursor.GetRoot()
	if root != nil {
		root.Traverse(func(n *tasknode.TaskNode) {
			if n.WetherFinished && n.Index > 0 {
				recentFinished = append(recentFinished, n)
			}
		})
	}
	// 按 Index 倒序，取最近 10 个
	for i := 0; i < len(recentFinished); i++ {
		for j := i + 1; j < len(recentFinished); j++ {
			if recentFinished[i].Index < recentFinished[j].Index {
				recentFinished[i], recentFinished[j] = recentFinished[j], recentFinished[i]
			}
		}
	}
	recentIDs := make(map[string]bool)
	limit := 10
	if len(recentFinished) < limit {
		limit = len(recentFinished)
	}
	for i := 0; i < limit; i++ {
		recentIDs[recentFinished[i].ID] = true
	}

	var sb strings.Builder
	omitted := 0
	if root != nil {
		r.buildFilteredIndex(root, 0, ancestorIDs, siblingIDs, recentIDs, &sb, &omitted, budget)
	}
	if omitted > 0 {
		sb.WriteString(fmt.Sprintf("... (%d more nodes omitted)\n", omitted))
	}
	return sb.String()
}

// buildFilteredIndex 递归构建裁剪后的树索引，只输出祖先 / 兄弟 / 最近完成节点。
func (r *Runtime) buildFilteredIndex(node *tasknode.TaskNode, indent int, ancestors, siblings, recent map[string]bool, sb *strings.Builder, omitted *int, budget int) {
	show := ancestors[node.ID] || siblings[node.ID] || recent[node.ID]
	if !show {
		*omitted++
		return
	}
	if sb.Len() >= budget {
		*omitted++
		return
	}

	line := r.formatTreeIndexLine(node, indent)
	sb.WriteString(line)

	for _, child := range node.Children {
		r.buildFilteredIndex(child, indent+1, ancestors, siblings, recent, sb, omitted, budget)
	}
}

// getTreeIndex 递归生成紧凑的树索引（enriched：含 result 摘要 + artifact refs）
func (r *Runtime) getTreeIndex(node *tasknode.TaskNode, indent int) string {
	line := r.formatTreeIndexLine(node, indent)
	for _, child := range node.Children {
		line += r.getTreeIndex(child, indent+1)
	}
	return line
}

// formatTreeIndexLine 格式化单个节点的索引行
func (r *Runtime) formatTreeIndexLine(node *tasknode.TaskNode, indent int) string {
	line := fmt.Sprintf("%s- [%s] %s (%s)", strings.Repeat("  ", indent), node.ID, node.Name, nodeStatusStr(node.Status))

	if node.WetherFinished {
		if node.Result != "" {
			summary := node.Result
			if len(summary) > 80 {
				summary = summary[:80] + "..."
			}
			line += fmt.Sprintf(" → %s", summary)
		}
		if len(node.ArtifactRefs) > 0 && len(node.ArtifactRefs) <= 3 {
			line += fmt.Sprintf(" [refs: %s]", strings.Join(node.ArtifactRefs, ","))
		} else if len(node.ArtifactRefs) > 3 {
			line += fmt.Sprintf(" [%d refs]", len(node.ArtifactRefs))
		}
	}

	return line + "\n"
}

// collectSiblingHandoffs 收集已完成兄弟节点的 handoff（向后兼容，activation.go 中有更丰富的版本）
func (r *Runtime) collectSiblingHandoffs(current *tasknode.TaskNode) string {
	if current.Parent == nil {
		return ""
	}
	var sb strings.Builder
	for _, sibling := range current.Parent.Children {
		if sibling.ID != current.ID && sibling.WetherFinished && sibling.Handoff != "" {
			sb.WriteString(fmt.Sprintf("[%s] %s: %s\n", sibling.ID, sibling.Name, sibling.Handoff))
		}
	}
	return sb.String()
}

// === Global Workspace（向后兼容，selectAttentionNodes 已废弃） ===

// NodeResult 辅助结构用于排序
type NodeResult struct {
	Index       int
	Name        string
	Result      string
	Variables   map[string]interface{}
	IsImportant bool
}

// selectAttentionNodes 已废弃，保留空实现以防外部调用
func (r *Runtime) selectAttentionNodes(current *tasknode.TaskNode, request string) ([]string, error) {
	return nil, nil
}

// FormatGlobalWorkspace 根据选中的 ID 组合详细的 RAM 快照
func (r *Runtime) FormatGlobalWorkspace(nodeIDs []string) string {
	if len(nodeIDs) == 0 {
		return "No nodes selected for global workspace."
	}

	var sb strings.Builder
	sb.WriteString("Useful information picked from selected nodes:\n")

	root := r.cursor.GetRoot()
	foundAny := false

	for _, id := range nodeIDs {
		node := r.findNodeByID(root, id)
		if node != nil {
			foundAny = true
			sb.WriteString(fmt.Sprintf("- [%s] %s:\n", node.ID, node.Name))
			if node.Result != "" {
				resultStr := node.Result
				if len(resultStr) > 1000 {
					resultStr = resultStr[:1000] + " ... [TRUNCATED]"
				}
				sb.WriteString(fmt.Sprintf("  Result: %s\n", resultStr))
			}
			if len(node.Variables) > 0 {
				varsJSON, _ := json.Marshal(node.Variables)
				varsStr := string(varsJSON)
				if len(varsStr) > 2000 {
					varsStr = varsStr[:2000] + " ... [TRUNCATED]"
				}
				sb.WriteString(fmt.Sprintf("  Variables: %s\n", varsStr))
			}
		}
	}

	if !foundAny {
		return "No historical context available yet (no matching nodes found)."
	}
	return sb.String()
}

// findNodeByID 递归查找节点
func (r *Runtime) findNodeByID(root *tasknode.TaskNode, id string) *tasknode.TaskNode {
	if root.ID == id {
		return root
	}
	for _, child := range root.Children {
		found := r.findNodeByID(child, id)
		if found != nil {
			return found
		}
	}
	return nil
}

// collectGlobalAttention 递归扫描整棵树，提取所有"有用"的节点信息（重要的或有结果的）
func (r *Runtime) collectGlobalAttention(node *tasknode.TaskNode) []NodeResult {
	var results []NodeResult
	if node.IsImportant || node.Result != "" {
		results = append(results, NodeResult{
			Index:       node.Index,
			Name:        node.Name,
			Result:      node.Result,
			Variables:   node.Variables,
			IsImportant: node.IsImportant,
		})
	}
	for _, child := range node.Children {
		results = append(results, r.collectGlobalAttention(child)...)
	}
	return results
}

// FormatHistory 输出全局工作区窗口（按 Important 优先 + Index 倒序，限 10 条）。
func (r *Runtime) FormatHistory() string {
	root := r.cursor.GetRoot()
	if root == nil {
		return "No global workspace context available."
	}

	allResults := r.collectGlobalAttention(root)
	if len(allResults) == 0 {
		return "No global workspace entries available yet."
	}

	for i := 0; i < len(allResults); i++ {
		for j := i + 1; j < len(allResults); j++ {
			iImportance := 0
			if allResults[i].IsImportant {
				iImportance = 1
			}
			jImportance := 0
			if allResults[j].IsImportant {
				jImportance = 1
			}

			if iImportance < jImportance || (iImportance == jImportance && allResults[i].Index < allResults[j].Index) {
				allResults[i], allResults[j] = allResults[j], allResults[i]
			}
		}
	}

	const maxWindow = 10
	limit := len(allResults)
	if limit > maxWindow {
		limit = maxWindow
	}
	window := allResults[:limit]
	for i := 0; i < len(window); i++ {
		for j := i + 1; j < len(window); j++ {
			if window[i].Index > window[j].Index {
				window[i], window[j] = window[j], window[i]
			}
		}
	}

	var sb strings.Builder
	sb.WriteString("Global Workspace (RAM - Key findings and important variables picked from all nodes):\n")
	for _, res := range window {
		impTag := ""
		if res.IsImportant {
			impTag = " [PINNED]"
		}
		sb.WriteString(fmt.Sprintf("- [%d] %s%s:\n", res.Index, res.Name, impTag))
		if res.Result != "" {
			sb.WriteString(fmt.Sprintf("  Result: %s\n", res.Result))
		}
		if len(res.Variables) > 0 {
			varsJSON, _ := json.Marshal(res.Variables)
			sb.WriteString(fmt.Sprintf("  Variables: %s\n", string(varsJSON)))
		}
	}
	return sb.String()
}

// CollectScopedVariables 收集从根节点到当前路径的所有变量（越靠近当前节点优先级越高）。
func (r *Runtime) CollectScopedVariables(current *tasknode.TaskNode) map[string]interface{} {
	vars := make(map[string]interface{})
	path := []*tasknode.TaskNode{}
	node := current
	for node != nil {
		path = append([]*tasknode.TaskNode{node}, path...)
		node = node.Parent
	}

	for _, n := range path {
		for k, v := range n.Variables {
			vars[k] = v
		}
	}
	return vars
}

// formatVariables 格式化变量为可读字符串
func formatVariables(vars map[string]interface{}, maxLen int) string {
	if len(vars) == 0 {
		return "No scoped variables."
	}

	var sb strings.Builder

	if hist, ok := vars["command_output_history"]; ok {
		sb.WriteString("### Command Execution History (Last 20):\n")
		var history []string
		if casted, ok := hist.([]string); ok {
			history = casted
		} else if castedInterface, ok := hist.([]interface{}); ok {
			for _, item := range castedInterface {
				if str, ok := item.(string); ok {
					history = append(history, str)
				}
			}
		}

		for _, entry := range history {
			sb.WriteString(entry + "\n\n")
		}
		delete(vars, "command_output_history")
	}

	if len(vars) > 0 {
		data, _ := json.MarshalIndent(vars, "", "  ")
		varsStr := string(data)
		if len(varsStr) > maxLen {
			varsStr = varsStr[:maxLen] + "\n... [TRUNCATED DUE TO SIZE]"
		}
		sb.WriteString("\n### Other Variables:\n")
		sb.WriteString(varsStr)
	}

	return sb.String()
}

// formatPath 格式化路径为可读字符串
func formatPath(path []string) string {
	if len(path) == 0 {
		return "root"
	}
	result := ""
	for i, p := range path {
		if i > 0 {
			result += " -> "
		}
		result += p
	}
	return result
}
