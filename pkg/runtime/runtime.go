// Package runtime 是 LLMVM 的核心执行引擎。
//
// 文件分布：
//
//	runtime.go         — Runtime struct + NewRuntime + 简单 getter
//	budget.go          — BudgetConfig + .env 解析
//	execute.go         — Execute 主循环 + decideNextStep + handleError
//	dispatch.go        — ExecuteAction switch + 各 action handler
//	prompt.go          — buildPromptInternalV2 + nodeToState + getChildrenInfo
//	prompt_template.go — 长 prompt 模板字符串（Phase 15 会迁到 .md + embed）
//	context_assembly.go— buildGlobalContext / tree index / scoped variables / workspace
//	activation.go      — NodeActivation + ContextPack pipeline (位置工程入口)
//	position.go        — Position / Authority / Scope 抽象
//	index.go           — IndexNodeArtifactsAsync / syncNodeToMemory / RebuildIndexFromAST
//	shell.go           — HandleCLI + sandboxPath
//	tokens.go          — 估算 / 压缩 / 兜底 operation log
//	actions.go         — 几个特殊 action handler（human_input / append_sibling / query_memory / add_artifact / modify_artifact / request_context / acceptance）
//	agentic_loop.go    — Leaf 节点的 ReAct 多轮迭代
package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Steve65535/llmvm/pkg/artifact"
	"github.com/Steve65535/llmvm/pkg/cursor"
	"github.com/Steve65535/llmvm/pkg/llm"
	"github.com/Steve65535/llmvm/pkg/memory"
	"github.com/Steve65535/llmvm/pkg/resolver"
	"github.com/Steve65535/llmvm/pkg/retrieval"
	"github.com/Steve65535/llmvm/pkg/tasknode"
	"github.com/Steve65535/llmvm/pkg/vector"
	"github.com/Steve65535/llmvm/pkg/vfs"
)

// Runtime 是 LLM 运行时引擎。
//
// AST 是控制流权威源；memStore + vecStore 是可重建的检索镜像。
// 所有 mutation 走 ExecuteAction，dispatch.go 内的 handler 负责把节点变更同步到 SQLite/向量库。
type Runtime struct {
	engine      llm.Engine
	cursor      *cursor.Cursor
	initialReq  string
	nodeCounter int
	vfs         *vfs.VirtualFileSystem
	artifacts   *artifact.Store
	budget      BudgetConfig
	memStore    *memory.Store      // SQLite 结构化检索层
	vecStore    *vector.Store      // chromem-go 向量索引（语义召回，可选；nil 时降级为纯 FTS）
	retrieve    *retrieval.Service // 混合检索 + 确定性 rerank
	resolver    *resolver.Resolver // 大 artifact 缩小（Phase 9）
	embedWG     sync.WaitGroup     // 跟踪后台 embed goroutine（Shutdown / 测试用）

	// Stagnation Detection
	lastResponse    string
	stagnationCount int
	lastNodeID      string

	// Sliding Window Compression (0=full, 1=no workspace, 2=history≤5, 3=history≤2, 4=minimal)
	compressionLevel int

	// OnStepComplete is called after each node execution step
	OnStepComplete func(*tasknode.TaskNode)

	// LeafTurnTimeout 单次 leaf turn 的超时（含 LLM 调用 + action 执行）。
	// 0 表示不超时（默认）。
	LeafTurnTimeout time.Duration

	// HumanInputFunc 是 runtime 请求人类输入时调用的函数。
	// 如果为 nil，则使用标准输入（stdin）。
	HumanInputFunc func(req *tasknode.HumanRequest) (*tasknode.HumanResponse, error)
}

// NewRuntime 创建新的运行时实例。dbPath 为空时使用进程内 :memory: 库。
//
// SQLite + 向量库初始化失败都不致命：
//   - SQLite 失败 → 不开 query_memory；retrieval 走纯 vector。
//   - 向量库失败 → retrieval 走纯 FTS。
//   - 两者都失败 → context pack 退化为 deterministic activation（祖先链 + handoff）。
func NewRuntime(engine llm.Engine, root *tasknode.TaskNode, dbPath ...string) *Runtime {
	vfsInstance := vfs.New(".")
	budget := newBudgetConfig()
	fmt.Printf("📊 Context budget: total=%d retrieval=%d artifact_inline=%d artifact_async_threshold=%d\n",
		budget.ContextTokenLimit, budget.RetrievalTokenLimit,
		budget.ArtifactInlineTokenLimit, budget.ArtifactAsyncTokenThreshold)

	sqlitePath := ":memory:"
	if len(dbPath) > 0 && dbPath[0] != "" {
		sqlitePath = dbPath[0]
	}
	mem, err := memory.New(sqlitePath)
	if err != nil {
		fmt.Printf("⚠️  Failed to init memory store (%s): %v (continuing without SQLite)\n", sqlitePath, err)
	} else if sqlitePath != ":memory:" {
		fmt.Printf("🗄️  SQLite memory store: %s\n", sqlitePath)
	}

	// 向量库目录默认与 SQLite 同名（foo.sqlite → foo.chromem）
	vecDir := os.Getenv("LLMVM_VECTOR_DIR")
	if vecDir == "" {
		if sqlitePath != ":memory:" {
			ext := filepath.Ext(sqlitePath)
			vecDir = strings.TrimSuffix(sqlitePath, ext) + ".chromem"
		}
	}
	var vec *vector.Store
	if vecDir != "" {
		v, err := vector.New(vecDir)
		if err != nil {
			fmt.Printf("⚠️  Vector store init failed (%s): %v (FTS-only mode)\n", vecDir, err)
		} else {
			vec = v
			fmt.Printf("🧭 Vector store: %s [embedder=%s]\n", vecDir, vec.EmbedderName())
		}
	}

	rt := &Runtime{
		engine:      engine,
		cursor:      cursor.New(root),
		nodeCounter: 0,
		vfs:         vfsInstance,
		artifacts:   artifact.New(),
		budget:      budget,
		memStore:    mem,
		vecStore:    vec,
		retrieve:    retrieval.NewService(mem, vec),
	}
	rt.resolver = resolver.New(rt.artifacts)
	return rt
}

// GetCurrentNode 获取当前节点
func (r *Runtime) GetCurrentNode() *tasknode.TaskNode {
	return r.cursor.Current
}

// GetArtifacts 返回 artifact store（用于序列化）
func (r *Runtime) GetArtifacts() *artifact.Store {
	return r.artifacts
}

// SetArtifacts 设置 artifact store（用于反序列化恢复）
func (r *Runtime) SetArtifacts(store *artifact.Store) {
	r.artifacts = store
}
