# LLMVM 当前实现问题报告

## 摘要

当前代码已经不再是纯设计实验。核心 VM 骨架已经进入主执行路径：

- Task Tree / AST 作为控制流权威。
- DFS Cursor 作为指令指针。
- Runtime 作为执行内核。
- Position / Authority 作为权限边界。
- JSON Action DSL 作为模型到 runtime 的指令集。
- Artifact Store 作为证据存储。
- SQLite / Vector 作为可重建检索索引。
- ContextPack 作为位置驱动上下文组装入口。
- Acceptance Criteria / Results 作为完成判定协议。

总体方向是正确的。当前最大问题不是“缺少功能”，而是几个关键边界还不够硬：Parser / DSL 诊断、ContextPack 预算与检索所有权、Acceptance 完成状态机。

如果这些边界继续松散，系统会在 action 数量、artifact 数量、retrieval 策略和验收语义增长后变得难以调试。

---

## 当前实现成熟度判断

| 模块 | 估计成熟度 | 判断 |
|---|---:|---|
| Task Tree VM | 75%+ | AST、Cursor、save/load、handoff 已形成主干 |
| Position / Authority | 70%+ | 已进入 `ExecuteAction` 前置校验，但 scope 还未充分驱动检索 |
| Artifact DSL | 70% | `add_artifact` / `modify_artifact` 已落地，粒度约束仍依赖 prompt |
| SQLite Memory | 75% | 结构化索引较完整，作为可重建镜像的边界清楚 |
| Vector / RAG | 50-60% | 有 hybrid retrieval，但还不是完整 ContextProvider 架构 |
| ContextPack | 50-60% | 已进入主路径，但仍是 activation + retrieval 的叠加式实现 |
| Acceptance | 60% | `testable` 较硬，`manual` / `llm_judge` 语义仍弱 |
| Parser / DSL 诊断 | 35-40% | 字段校验已有，但缺结构化 diagnostic 和语义分析层 |
| Prompt Protocol | 70% | 已用 `go:embed`，但仍有旧术语和预算职责混杂 |
| Human-in-the-loop | 65% | pause/resume 已可用，尚未形成完整审批/中断策略 |

---

## 问题 1：Parser 仍然偏“字段校验”，不是 DSL 诊断层

### 现状

`pkg/llm/parser.go` 已经能做基本 JSON decode 和 action 字段校验，例如：

- action_type 必填。
- `create_node` 拒绝 `Loop`。
- 文件工具校验 `file_path` / `content`。
- `query_memory` 校验 query_type。
- `add_artifact` 校验 artifact_name / content / scope。
- `acceptance_criteria` 校验 check_type。

这比纯 prompt 约束强很多，但仍然属于“字段级校验”。

### 风险

随着 DSL 增长，错误会越来越复杂：

- artifact ref 不存在。
- acceptance_result 引用不存在的 criterion。
- action 顺序不合法。
- unknown field 被 JSON decoder 静默忽略。
- 模型输出前后混入解释文本。
- 多个 JSON 对象或半截 JSON 无法给出可修复反馈。
- scope / authority / state transition 错误混在普通 error 字符串里。

如果继续只返回 `fmt.Errorf(...)`，模型下一轮很难精确修复，runtime 也难以统计错误类型。

### 建议

短期不需要做完整 compiler，但需要引入 compiler-style diagnostic：

```go
type Diagnostic struct {
    Kind       string
    Code       string
    Path       string
    Message    string
    RepairHint string
    RawExcerpt string
}
```

解析路径建议拆成四层：

```text
CleanLLMOutput
  -> ExtractJSONObject
  -> DecodeResponse
  -> ValidateResponseSchema
```

下一步应优先支持：

- `ParseErrorKind`
- JSON object extraction
- trailing text 检测
- unknown field 检测
- path-aware error，例如 `actions[0].node.type`
- repair_hint 注入下一轮 prompt

---

## 问题 2：ContextPack 仍然是叠加式实现，还不是完整上下文系统

### 现状

当前主路径已经变成：

```text
buildGlobalContext
  -> BuildPosition
  -> BuildContextPack
  -> buildNodeActivation
  -> retrieval.Query
  -> resolver.Resolve
  -> formatContextPack
```

这是很大的进步。上下文不再只是固定拼接，而是开始根据位置和目标检索 artifact。

### 风险

`ContextPack` 目前仍然以 `NodeActivation.WorkingContext` 为底，再追加 retrieval 和 resolved spans。这容易导致：

- artifact index 和 retrieval 结果重复。
- Tree index、handoff、retrieved evidence 的优先级不清楚。
- 每层都可能自己决定 prompt 形状。
- omissions / provenance 还不够统一。
- scope 对 retrieval 的约束还比较粗。

### 建议

把 ContextPack 明确成唯一上下文打包入口：

```text
Position
  -> ContextNeeds
  -> ContextPlan
  -> ContextProviders
  -> RetrievedItems
  -> Resolver
  -> ContextPack Builder
  -> Prompt section rendering
```

第一阶段可以先不引入复杂接口，只要把职责收紧：

- `NodeActivation` 只产出结构化 baseline，不直接产出最终 prompt 字符串。
- `retrieval` 只产出候选和 trace。
- `resolver` 只产出 evidence spans。
- `ContextPack Builder` 统一排序、去重、裁剪、记录 omissions。
- `prompt.go` 只渲染最终 pack，不再做复杂预算分配。

---

## 问题 3：预算所有权分裂

### 现状

`budget.go` 已经表达了新方向：

- 全局 context token limit。
- retrieval token limit。
- artifact inline token limit。
- artifact async threshold。

但 `prompt.go` 仍然保留固定比例：

```go
globalContextBudget := remainingChars * 50 / 100
variablesBudget := remainingChars * 40 / 100
```

### 风险

这会造成文档与实现不一致：

- 设计上说“取消固定比例”。
- 代码上仍然按段裁剪。
- 关键 evidence 可能因为段比例被截掉。
- ContextPack 无法真正成为预算所有者。

### 建议

短期可以保留最终硬保护，但应调整职责：

- ContextPack 负责 relevance-driven 裁剪。
- Prompt 层只做最后安全截断。
- Scoped variables 也应变成 ContextPack 的一种 item，而不是 prompt 层单独分配预算。
- 所有 omissions 都应该进入 `## Omissions`，方便模型知道缺了什么。

---

## 问题 4：Acceptance 还不是完整完成状态机

### 现状

当前实现已经支持：

- `create_node.acceptance_criteria`
- `mark_complete.acceptance_results`
- required criteria 未满足时拒绝完成
- `testable` check 由 runtime 执行命令验证
- acceptance evidence 写入 artifact / memory

这是非常重要的硬边界。

### 风险

`manual` 和 `llm_judge` 目前仍然偏弱：

- `manual` 如果只是模型提交 passed=true，就没有真实人类确认。
- `llm_judge` 如果只靠当前模型自评，等于把完成判定还给模型。
- acceptance_result 引用不存在 criterion 时目前是 skip / warning，语义偏软。
- `mark_complete` 前后的 action 顺序约束还不够明确。

### 建议

把 completion 建成状态机：

```text
Pending
  -> EvidenceCollected
  -> AcceptanceChecking
  -> Accepted
  -> Completed

or

AcceptanceFailed
  -> Retry
  -> Escalate
```

规则建议：

- `testable` 必须 runtime 执行。
- `manual` 必须触发 `request_human_input` 或外部 approval。
- `llm_judge` 必须引用 evidence artifact，并标记低信任等级。
- 未知 `criterion_id` 应该直接拒绝 `mark_complete`，不应只跳过。
- required acceptance 失败时，应生成结构化 failure handoff。

---

## 问题 5：Resolver 仍是关键词滑窗，不是完整大 Artifact 缩小流程

### 现状

`pkg/resolver` 已实现同步 resolver：

- 读取 artifact 全文。
- 根据 ContextNeed / acceptance criteria 分词。
- 滑窗扫描关键词命中。
- 返回 evidence spans。

这是合理的第一版。

### 风险

关键词滑窗对以下场景不够：

- 语义相关但关键词不同。
- 代码结构相关，但 query 不含精确 token。
- 需要跨多个片段综合。
- 大日志中关键原因需要排序和归因。

### 建议

保留当前 deterministic resolver 作为 fallback，后续增加分层策略：

```text
keyword window
  -> chunk-level FTS
  -> chunk vector search
  -> optional LLM compression
  -> evidence span with provenance
```

Resolver 输出应始终包含：

- artifact_id
- line range
- score
- rationale
- truncated 标志
- confidence

---

## 问题 6：Position 的 scope 还没有充分驱动检索和权限

### 现状

`BuildPosition` 已经能推导 role / authority / scope。

`CheckAuthority` 已经进入 `ExecuteAction` 前置校验。

### 风险

当前 scope 主要用于默认 artifact scope filter，还没有完全变成上下文和权限策略：

- planner / executor 的 retrieval 范围还比较粗。
- error_handler 对失败历史的默认获取还不完整。
- root 默认不做语义召回是合理的，但顶层 integration 场景可能仍需要 curated evidence。
- action 权限还偏粗，例如读写文件、shell、artifact 修改的风险等级没有细分。

### 建议

分阶段增强：

- 给每个 role 定义默认 ContextNeeds。
- ErrorHandler 默认获取 failure history / last_error / failed action trace。
- Root 允许 curated summary retrieval，但不直接拉大 evidence。
- 将 `execute_command` 进一步区分 read-only / write / destructive。
- 将 `modify_artifact` 受 scope 限制，避免 executor 修改 global artifact。

---

## 问题 7：Prompt 仍有旧术语和协议噪声

### 现状

Prompt 已经通过 `go:embed` 从 markdown 加载，这是正确方向。

但仍有一些旧术语：

- `Loop Context`
- `Agentic Loop`
- `Global Workspace`
- 一些 Phase 注释仍然停留在代码里

### 风险

LLMVM 已经转向：

```text
Position + ContextPack + Acceptance
```

如果 prompt 里同时出现旧概念，模型可能混淆：

- Loop 已删除，但又看到 Loop Context。
- ContextPack 是主机制，但 Global Workspace 仍在。
- Acceptance 是完成协议，但 Agentic Loop 又像旧 retry loop。

### 建议

术语收敛：

- `Loop Context` 改成 `Execution Iteration Context`。
- `Agentic Loop` 改成 `Leaf Refinement`.
- `Global Workspace` 改成 `ContextPack` 或 `Retrieved Context`。
- 移除 prompt 中过时示例。
- 把 action catalog 和 parser schema 建立一致性检查。

---

## 问题 8：Action dispatch 目前健康，但需要防止重新膨胀

### 现状

`dispatch.go` 的 switch 已经变成调度表：

```go
case "create_node":
    return r.actionCreateNode(...)
case "mark_complete":
    return r.actionMarkComplete(...)
```

这是当前阶段的最优解。

### 风险

随着 DSL 继续增长，容易重新变成巨大 switch 和巨大 handler。

### 建议

保留 switch，但建立纪律：

- switch 只负责 dispatch。
- 每个 action handler 控制在可读范围内。
- 复杂语义拆到独立文件，例如 `acceptance.go`、`artifact_actions.go`。
- 每个 action 至少有 parser test + runtime semantic test。
- 新 action 必须同步更新 system prompt、parser、runtime handler、README / docs。

---

## 优先级路线

### P0：让现有实现可信

1. 清理 prompt 旧术语。
2. 修正文档和代码里的 Phase 过时注释。
3. 确认 README 中默认配置与代码一致。
4. 为新增 runtime 文件补齐关键测试。

### P1：做硬边界

1. Parser diagnostic。
2. Unknown field 检测。
3. Acceptance unknown criterion 直接拒绝。
4. ContextPack 统一预算和 omissions。

### P2：增强位置工程

1. Role -> ContextNeeds 的确定性规则。
2. ErrorHandler failure context。
3. Scope-aware artifact modification。
4. Shell action 风险分级。

### P3：完善 RAG

1. ContextProvider 抽象。
2. Chunk-level artifact index。
3. Resolver 支持 vector chunk search。
4. Retrieval trace 在 visualizer 中展示。

### P4：产品化可观察性

1. TUI cockpit。
2. Runtime event stream。
3. Prompt / ContextPack / action trace viewer。
4. Human approval workflow。

---

## 总结

当前实现已经把 LLMVM 的核心理念落到了主路径：

```text
AST + Cursor + Position + ContextPack + Action DSL + Artifact + Memory + Acceptance
```

这已经明显区别于普通 Agent loop。

下一阶段不应主要继续堆 feature，而应把三个边界做硬：

1. **Parser / DSL diagnostic**
2. **ContextPack ownership**
3. **Acceptance completion protocol**

只要这三条边界稳住，后续接更复杂的 RAG、TUI、approval、provider schema mode 都会比较自然。
