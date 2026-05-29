# Runtime Open Loops — 改造之后未闭环的部分

写这份文档的目的是把"今天 RAG / 位置工程改造结束时还没闭合的回路"摊开，明天直接据此推进。每条按 P0/P1/P2/P3 打了优先级，配了"为什么需要"和"动哪里"。

完成的部分见 `rag_artifact_position_engineering_experiment.md` 与 `position_engineering_deep_refactor.md`。这份文档**只**写没做完的。

> 时间：2026-05-28
> 仓库状态：所有测试绿，但下面这些回路还没真正闭合

---

## 已闭合的回路（一览）

| 回路 | 状态 |
|---|---|
| 感知-动作（ReAct）| ✅ Leaf 节点 execute → observe → mark_complete |
| 验收-完成 | ✅ mark_complete 缺 acceptance_results 时被拒，下一 turn 重试 |
| 错误恢复 | ✅ ErrorHandler 重定向 + RetryCount 升级 |
| 上下文溢出 | ✅ 4 级压缩升级 + fail-fast |
| 停滞检测 | ✅ identical-response → 警告 → 强制变更 → Failed |
| 持久化-恢复 | ✅ SaveState + 从 AST 重建 SQLite |
| 工具结果-记忆 | ✅ artifact → SQLite/FTS 同步 + 异步 vector embed |
| Position 鉴权 | ✅ Authority fail-fast，4 角色权限矩阵 |

下面是没闭合的。

---

## P0：Context Acquisition Loop 没做完整版

### 现状
当前 `request_context` 是 fire-and-forget：
- 模型这一 turn 发了 `request_context` action
- runtime 落到 retrieval.Service → 结果写入 artifact
- 但**这一 turn 已经结束**，模型要等**下一 turn**的 prompt 才能读到检索结果

### 文档 1 Phase 4 期望
节点应该可以"在一次 activation 内多次取证"：
```
build Position
plan Context Acquisition
retrieve Context
LLM decide
  -> emit request_context
  -> runtime 同步执行 retrieval
  -> 把结果直接拼到当前节点的下一段 prompt 里（不推进 cursor）
  -> LLM 第二段决策
  -> emit final action（mark_complete / create_node / ...）
```

### 改造点
- `pkg/runtime/execute.go` 主循环：`request_context` 不再算"完成 action"，特殊化处理
- `request_context` 后立即 rebuild prompt（带新 artifact）→ 再次 engine.Call
- 加硬上限：单节点每 turn 最多 N 次 request_context（默认 3），防无限循环
- 当前 turn 累积的 retrieval 结果应该传成 prompt 的一段，例如 `## Just-In-Time Context`

### 验收
- 一个集成测试：scriptedEngine 第一次返回 request_context，第二次返回 mark_complete + 引用第一次召回的 artifact ID。整个过程在一个 cursor step 内完成。

### 风险
- 成本暴涨：单节点 LLM 调用从 1 次变成 N+1 次
- 必须保留"模型自愿放弃 request_context 直接 mark_complete"的路径

---

## P0：自动 retrieval 与模型 request_context 会重复

### 现状
- `BuildContextPack` 在 Planner/Executor 节点会自动跑一次 retrieval（pkg/runtime/activation.go）
- 模型同 turn 又发 `request_context` 时，runtime 会再跑一次 retrieval.Service.Query
- 没有去重：相同 query 会被检索两次，相同 artifact 也可能进 prompt 两次

### 改造点
- 在 Runtime 上加 `seenRetrievalKeys map[string]int64`（key = query+scope, value = artifact_id of last result）
- request_context 命中已有 key 时直接返回上次的 context_pack artifact ID，不重检索
- 节点切换时清空 map（每个节点独立一份缓存）

### 验收
- 集成测试：同一 turn 内连续两次相同 query 的 request_context，runtime 只调一次 retrieval.Service.Query

---

## P1：Resolver 的 LLM 精读路径没接通

### 现状
`pkg/resolver/resolver.go` 只做 keyword window matching：
- 输入 ContextNeed → 切窗 30 行 + 步长 15
- 命中关键词 ≥1 的窗口入候选
- 按命中数排序、合并相邻窗、按 token 预算裁剪
- 输出 EvidenceSpan 列表

这覆盖了"快速定位关键词附近"的场景，但文档 2 说的"LLM 精读压缩"没做。

### 文档 2 期望
对超大 artifact，启动一个独立 LLM 调用（独立 prompt cache，不污染主上下文），输入 `ContextNeed + AcceptanceCriteria + 完整 artifact`，输出 `Summary + EvidenceSpan + Confidence`。

### 改造点
- `pkg/resolver/resolver.go` 加 LLM 接口：
  ```go
  type LLMResolver interface {
      Compact(ctx, art *artifact.Artifact, need string, ac []string, maxTokens int) (*Result, error)
  }
  ```
- Resolver 接 engine 注入：keyword 模式 + llm 模式可选（env: `LLMVM_RESOLVER_MODE=keyword|llm|hybrid`）
- 默认 keyword（保持当前行为），用户显式开启 llm
- llm 模式有 hard timeout（30s）+ 失败回退 keyword

### 验收
- 单元测试：mock engine 返回压缩结果 → resolver 返回 LLM 给的 EvidenceSpan
- 性能：llm 模式不应阻塞主循环超过 30s

---

## P1：Visualizer 没接 retrieval / rerank trace

### 现状
SQLite 已经有两张可观测表（runtime 每次 retrieval 都落 trace）：
```sql
retrieval_events  (id, node_id, query, candidate_ids, selected_ids, budget_used, ...)
rerank_events     (retrieval_event_id, artifact_id, score, rationale)
```
但 visualizer 没拉这些。debug 检索决策时只能 `sqlite3 foo.sqlite "SELECT ..."`。

### 改造点
- `visualizer/server.go` 加 endpoint：
  - `GET /api/retrieval?node_id=X` — 返回该节点所有 retrieval_events
  - `GET /api/retrieval/<event_id>/rerank` — 返回该次检索的 rerank trace
- `visualizer/frontend/src/App.jsx` 加面板：
  - 节点 hover 时若有 retrieval events，显示展开按钮
  - 点开展示候选列表 + 每条的得分 + rationale（"[fts:rank=1]" "[vec:sim=0.83]"）

### 验收
- 跑端到端集成 → 打开 visualizer → 点节点 → 看到当次 request_context 的所有候选 + rerank 决策

---

## P1：Escalation Handoff 没机制

### 现状
节点 `RetryCount > MaxRetries` → 直接 `current.Status = tasknode.Failed` + 上抛。父节点下一次激活只能看到 Failed 子节点 handoff 的兜底字段。

### 文档 2 期望
> 超过 retry 限制时把失败状态结构化交接（compact failure state / escalation handoff）

应该让 runtime 在 Failed 之前，给当前节点最后一次机会做"escalation handoff"：模型生成失败摘要 + 失败模式 + 建议（"换语言/换工具/拆分"），父节点下次激活就能拿到这份结构化升级报告。

### 改造点
- `pkg/runtime/execute.go`：retry 用尽时，发一次特殊 prompt（含 `## ESCALATION REQUIRED`），让模型只输出一个 `escalation_handoff` action
- `pkg/llm/parser.go` 加 `escalation_handoff` action_type，字段 = `failure_mode / attempts_summary / suggested_next_steps / blocking_dependencies`
- 落 SQLite `node_handoffs` 表（用现有 schema 即可，多写 escalation 字段）
- 父节点的 ContextPack 优先展示子节点的 escalation handoff（独立段落）

### 验收
- 集成测试：Leaf 连续失败 3 次 → 第 4 次 turn 看到 escalation prompt → 父节点下次激活的 prompt 中包含 ESCALATION 段

---

## P2：acceptance check_type=llm_judge 没真正实现

### 现状
runtime 把 `llm_judge` 当成 `manual` 处理：模型自评 `passed=true`，runtime 直接信任。

### 文档 2 期望
> llm_judge — 由当前 LLM 给出 pass/fail

应该起一次独立的 LLM 调用，输入 `criterion description + node 产出 + evidence_artifact_refs`，输出结构化 `{passed, reasoning}`。

### 改造点
- `pkg/runtime/actions.go` 的 `runTestableCheck` 旁边加 `runLLMJudgeCheck`
- 单独的 prompt template（`pkg/runtime/prompts/judge_prompt.md`）
- 失败 / timeout 时降级为模型自评（避免 judge 不可用阻塞节点）

### 验收
- 集成测试：mock engine 第一次返回 mark_complete with llm_judge result → runtime 起 second engine call (judge) → 结果覆盖模型自评

---

## P2：异步 embed 失败无重试 + 无 backfill

### 现状（pkg/runtime/index.go）
```go
if err := r.vecStore.Upsert(ctx, id, body, meta); err != nil {
    fmt.Printf("  ⚠️  vector upsert %s failed: %v\n", id, err)
}
```
- 失败只 print 一行警告，artifact 永远进不了向量库
- 进程启动时如果 vector store 初始化失败（例如目录权限不对），后续即使可用，之前累积的 artifact 也不会自动补 index

### 改造点
- 失败的 artifact ID 加入 `pendingEmbedQueue`（in-memory 即可，进程退出时落 SQLite 表 `pending_embeddings`）
- 后台 worker 每 60s 扫描 pending 队列重试，指数退避，max 5 次后放弃
- 进程启动时若发现 `pending_embeddings` 有记录，恢复到内存队列

### 验收
- 单元测试：注入失败的 vector store → upsert 进 pending → mock 修复后 runtime 自动补 index

---

## P3：三个 token 阈值之间没有联动

### 现状
- `artifact.MaxContentSize = 8000 字节`（spill 到磁盘）
- `BudgetConfig.ArtifactInlineTokenLimit = 6000 token`（内联 vs resolver）
- `BudgetConfig.ArtifactAsyncTokenThreshold = 12000 token`（resolver 触发）

三个阈值各管各的，没交叉验证。例如一个 5KB（≈1250 token）的 artifact 既不 spill 也不 resolve，但 ContextPack 里堆 10 个就能撑爆 inline 预算。

### 改造点
- 把 `MaxContentSize` 改成"按 token 估算，对齐 ArtifactInlineTokenLimit"
- ContextPack 拼装时累加 tokens，触顶后剩余 artifact 全部走 resolver
- 文档化三个阈值的关系（哪个最严，哪个最松）

### 验收
- 单元测试：构造 N 个中等大小 artifact，让累计 token 超过 inline limit，验证后续 artifact 自动走 resolver

---

## P3：runtime 状态可观测性

### 现状
runtime 内部状态散在 stdout 日志：
- `compressionLevel` 升级了几次
- 每个节点用了多少 LLM call
- retrieval 命中了哪些 artifact
- vector 后台 embed 队列长度

调试需要翻日志拼上下文。

### 改造点
- 每个节点保留一份 `RunStats` 结构（LLM call 数、压缩升级次数、retrieval 次数、artifact 产出数、token 累计）
- visualizer 节点 hover 时显示
- 落到 SQLite `node_run_stats` 表，便于跨节点对比

### 验收
- visualizer hover 任意节点显示 `LLM calls: 3, compressions: 0, retrievals: 1, artifacts: 2`

---

## 推进顺序建议

按依赖关系：

1. **先做 P0 #2（retrieval 去重）**——半天，但它会减少所有后续 LLM 调用的成本
2. **再做 P0 #1（Context Acquisition Loop）**——这是文档 2 的核心承诺，1-2 天
3. **接下来 P1 #4（visualizer trace 面板）**——做完上面两个就急需调试视图
4. **P1 #3（LLM resolver）+ P1 #5（escalation）**——这两个独立可并行
5. **P2 / P3**——按需推进

---

## 不在这份清单里（已显式 punt 的）

- 多向量后端（OpenAI / Cohere）：env 切换设计已经预留，现在不做
- artifact GC / 过期：当前 LRU=50 + spill 已经够用
- 跨进程 retrieval 缓存（Redis 之类）：单机模式不需要
- artifact 跨节点修改的 ACL：modify_artifact 全局可见即够
