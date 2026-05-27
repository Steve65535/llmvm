package runtime

import (
	"fmt"
	"strings"

	"github.com/Steve65535/llmvm/pkg/tasknode"
)

// EstimateTokenCount 估算文本的 Token 数量。
//
// 简单规则：英文约 4 字符 = 1 token，中文（多字节）约 1.5 字符 = 1 token。
// 用于 prompt 预检与压缩判定，并非精确 BPE。
func EstimateTokenCount(text string) int {
	chineseCount := 0
	for _, r := range text {
		if r > 127 {
			chineseCount++
		}
	}
	englishCount := len(text) - chineseCount
	return (englishCount / 4) + (chineseCount * 2 / 3)
}

// printTokenStats 打印 Token 统计信息，供 Execute 主循环 banner 使用。
func (r *Runtime) printTokenStats(prompt string, contextLimit int) {
	tokenCount := EstimateTokenCount(prompt)
	percentage := float64(tokenCount) / float64(contextLimit) * 100

	fmt.Printf("\n📊 Token Statistics:\n")
	fmt.Printf("   Prompt length: %d characters\n", len(prompt))
	fmt.Printf("   Estimated tokens: %d / %d (%.1f%%)\n", tokenCount, contextLimit, percentage)

	switch {
	case percentage > 90:
		fmt.Printf("   ⚠️  WARNING: Approaching context limit!\n")
	case percentage > 75:
		fmt.Printf("   ⚡ CAUTION: Using >75%% of context window\n")
	default:
		fmt.Printf("   ✅ Context usage is healthy\n")
	}
	fmt.Printf("\n")
}

// nodeTypeStr 节点类型 → 字符串（Loop 已删除）。
func nodeTypeStr(t tasknode.TaskType) string {
	switch t {
	case tasknode.Leaf:
		return "Leaf"
	default:
		return "Normal"
	}
}

// nodeStatusStr 节点状态 → 字符串。
func nodeStatusStr(s tasknode.TaskStatus) string {
	switch s {
	case tasknode.Running:
		return "Running"
	case tasknode.Completed:
		return "Completed"
	case tasknode.Failed:
		return "Failed"
	case tasknode.WaitingHuman:
		return "WaitingHuman"
	default:
		return "Pending"
	}
}

// isContextOverflow 检测 API 返回的上下文溢出错误。
func isContextOverflow(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "context_length_exceeded") ||
		strings.Contains(s, "max_tokens") ||
		strings.Contains(s, "context window") ||
		strings.Contains(s, "too many tokens") ||
		strings.Contains(s, "reduce the length")
}

// applyCompression 按压缩级别裁剪 scopedVariables（滑动窗口）。
//
//	Level 0: 全量
//	Level 1: 清空 workspace（由调用方处理）
//	Level 2: history 保留最近 5 条
//	Level 3: history 保留最近 2 条
//	Level 4+: 删除全部 history 和非关键变量
func applyCompression(vars map[string]interface{}, level int) {
	if level < 2 {
		return
	}
	keep := 5
	if level >= 3 {
		keep = 2
	}
	if level >= 4 {
		delete(vars, "command_output_history")
		delete(vars, "last_command_result")
		delete(vars, "last_error")
		return
	}
	if hist, ok := vars["command_output_history"]; ok {
		var history []interface{}
		switch v := hist.(type) {
		case []string:
			for _, s := range v {
				history = append(history, s)
			}
		case []interface{}:
			history = v
		}
		if len(history) > keep {
			vars["command_output_history"] = history[len(history)-keep:]
		}
	}
}

// generateOperationLog 为没有提供 key_facts 的节点生成操作日志（Runtime 兜底）。
//
// 输出带 [auto] 前缀，明确标记为机器生成，不伪装成语义事实。
func (r *Runtime) generateOperationLog(node *tasknode.TaskNode) []string {
	var log []string
	for k, v := range node.Variables {
		if strings.HasPrefix(k, "last_") {
			log = append(log, fmt.Sprintf("[auto] %s = %v", k, v))
		}
	}
	for _, art := range r.artifacts.ListByNode(node.ID) {
		log = append(log, fmt.Sprintf("[auto] produced %s: %s", art.ID, art.Summary))
	}
	return log
}
