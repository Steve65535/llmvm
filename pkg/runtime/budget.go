package runtime

import (
	"os"
	"strconv"
)

const (
	DefaultContextBudget  = 200000 // 默认总预算（token），可通过 LLMVM_CONTEXT_TOKEN_LIMIT 或老 CONTEXT_BUDGET 覆盖
	MaxHistoryEntryLength = 1000
)

// BudgetConfig 上下文预算（位置工程改造后只保留全局 token 上限）。
//
// 文档 2 取消了固定比例派生：原先 MaxTreeIndexChars / MaxArtifactIndexChars / MaxHandoffChars
// 把候选裁剪写死成模板，关键资料经常被排除。新策略：每次节点激活只要 prompt 总 token 不超过
// ContextTokenLimit 即可，relevance-driven 的 Context Pack Builder 决定取舍。
//
//   - ContextTokenLimit:           最终发给模型的 prompt 上限（兼容字段名 ContextBudget）
//   - RetrievalTokenLimit:         检索候选总预算（FTS+vector 合并后裁剪到这个上限再 rerank）
//   - ArtifactInlineTokenLimit:    artifact 直接进入 prompt 的内联上限
//   - ArtifactAsyncTokenThreshold: 超过这个阈值触发 resolver 异步缩小（Phase 9）
//   - MaxCommandResultChars:       单次工具结果上限（read_artifact 切片用），保留兼容
type BudgetConfig struct {
	ContextBudget               int // 总 prompt 预算（token）
	ContextTokenLimit           int // 同 ContextBudget，alias
	RetrievalTokenLimit         int
	ArtifactInlineTokenLimit    int
	ArtifactAsyncTokenThreshold int
	MaxCommandResultChars       int
}

// envInt 读取 env，缺省 fallback。允许多个 alias（如 LLMVM_CONTEXT_TOKEN_LIMIT 和 CONTEXT_BUDGET）。
func envInt(fallback int, keys ...string) int {
	for _, k := range keys {
		if s := os.Getenv(k); s != "" {
			if v, err := strconv.Atoi(s); err == nil && v > 0 {
				return v
			}
		}
	}
	return fallback
}

// newBudgetConfig 从 .env 读取全局上下文上限（替代原有比例派生）。
func newBudgetConfig() BudgetConfig {
	totalTokens := envInt(DefaultContextBudget, "LLMVM_CONTEXT_TOKEN_LIMIT", "CONTEXT_BUDGET")
	retrieval := envInt(totalTokens/2, "LLMVM_RETRIEVAL_TOKEN_LIMIT")
	inlineLimit := envInt(6000, "LLMVM_ARTIFACT_INLINE_TOKEN_LIMIT")
	asyncThreshold := envInt(12000, "LLMVM_ARTIFACT_ASYNC_TOKEN_THRESHOLD")
	cmdChars := envInt(8000, "LLMVM_COMMAND_RESULT_CHARS")

	return BudgetConfig{
		ContextBudget:               totalTokens,
		ContextTokenLimit:           totalTokens,
		RetrievalTokenLimit:         retrieval,
		ArtifactInlineTokenLimit:    inlineLimit,
		ArtifactAsyncTokenThreshold: asyncThreshold,
		MaxCommandResultChars:       cmdChars,
	}
}
