# LLMVM 位置工程深度改造方案

## 核心判断

LLMVM 现在已经有“位置工程”的雏形，但还没有把它提升为一等架构。

当前系统已经具备：

- 显式任务树：`TaskNode`
- DFS 调度：`Cursor`
- 节点激活上下文：`NodeActivation`
- 结构化交接：`mark_complete` handoff
- Artifact Store：工具结果外置
- SQLite Memory Store：节点、handoff、变量、artifact 索引
- `query_memory`：LLM 主动查询结构化记忆

这些能力说明方向是对的。但它们现在仍然偏向“上下文拼装”和“检索索引”，还没有形成完整的“节点基于当前位置主动获取最小充分上下文，并据此完成职责”的 runtime 协议。

因此下一阶段的重点不是继续往 prompt 里堆字段，而是把“位置”变成 runtime 的核心抽象。

一句话：

> 位置工程是 LLMVM 中面向任务树位置的职责限定、上下文获取、证据选择、决策权限和交接协议。

---

## 为什么叫位置工程

传统 agent 系统通常围绕一条连续对话运行。模型依赖历史消息、工具调用结果和压缩摘要继续工作。

LLMVM 的路线不同。它不是一个人一直聊天，而是一棵组织化任务树在运行：

- 根节点像 CEO，负责最终目标和全局责任。
- Normal 节点像经理，负责拆分、协调、复盘和整合。
- Loop 节点像周期性流程，负责在条件满足前重复执行。
- Leaf 节点像执行员工，负责局部取证、实现、验证和交付。

每个节点都不应该读取全部历史。它应该先理解自己在树里的位置，然后只获取完成当前位置职责所需的上下文。

所以问题不是：

> 这个 prompt 该怎么写？

也不只是：

> 这个节点该看到哪些上下文？

而是：

> 当前节点处在什么位置、承担什么职责、拥有什么权限、应该获取什么证据、完成后如何交接？

这就是位置工程。

---

## 与 Prompt Engineering / Context Engineering 的区别

| 概念 | 关注点 | 在 LLMVM 中的位置 |
|---|---|---|
| Prompt Engineering | 单次模型输入怎么表达 | `llm/api.go` 的系统提示和 action schema |
| Context Engineering | 给模型哪些上下文 | `runtime/activation.go` 和 artifact / memory 检索 |
| Position Engineering | 节点基于树中位置如何确定职责、权限、上下文需求和下一步 | 应成为 runtime 的核心协议 |

位置工程包含上下文工程，但它比上下文工程更上层。

上下文工程回答：“给什么？”

位置工程回答：“为什么这个节点应该拿这些？它拿到后有权做什么？完成后交给谁？”

---

## 当前架构已经完成的部分

### 1. 节点激活上下文

`pkg/runtime/activation.go` 中已有 `NodeActivation`：

```go
type NodeActivation struct {
    NodeID          string
    HierarchyPath   []NodeBrief
    ParentGoal      string
    SiblingHandoffs []memory.HandoffBrief
    ArtifactIndex   []memory.ArtifactBrief
    OpenQuestions   []string
    WorkingContext  string
}
```

这已经能让节点看到：

- 自己在层级路径中的位置
- 父节点目标
- 兄弟节点 handoff
- 可用 artifact 索引
- open questions

这是位置工程的第一步。

### 2. SQLite 结构化记忆

`pkg/memory/memory.go` 已经索引：

- `nodes`
- `node_handoffs`
- `scoped_variables`
- `artifacts`
- `artifact_fts`

这说明系统已经从“聊天历史记忆”走向“结构化运行时记忆”。

### 3. LLM 可主动查询记忆

`query_memory` action 已经支持：

- `sibling_handoffs`
- `ancestor_chain`
- `pinned_artifacts`
- `recent_artifacts`
- `fts_artifacts`

这让模型不必一次性吃下全部历史，而是可以按需查询。

### 4. 节点完成时有结构化交接

`mark_complete` 已经支持：

- `goal`
- `summary`
- `key_facts`
- `decisions`
- `assumptions`
- `artifact_refs`
- `outputs`
- `open_questions`
- `handoff`
- `confidence`

这是树式协作最重要的基础。

---

## 当前架构的不足

### 不足 1：Position 不是一等对象

当前节点的位置是散落在多个地方的：

- `TaskNode.Parent`
- `Cursor.GetPath()`
- `depth`
- `Index`
- `HierarchyPath`
- `SiblingHandoffs`

这些信息被用来组装 prompt，但 runtime 没有一个明确的 `Position` 对象。

建议新增：

```go
type Position struct {
    RootID       string
    NodeID       string
    ParentID     string
    Depth        int
    Path         []string
    SiblingIndex int
    NodeType     tasknode.TaskType

    Role         PositionRole
    Scope        ScopeDescriptor
    Authority    AuthorityDescriptor
    Constraints  []string
}
```

`Position` 应该回答：

- 当前节点在哪里？
- 它是规划者、执行者、循环控制者，还是恢复节点？
- 它能创建子节点吗？
- 它能追加同级节点吗？
- 它能写文件吗？
- 它能请求人类输入吗？
- 它应该默认读取哪些范围的记忆？

### 不足 2：上下文获取策略是固定模板

当前 `buildNodeActivation` 固定获取：

- hierarchy path
- parent goal
- sibling handoffs
- artifact index
- tree index

这适合作为默认上下文，但还不够。

真正的位置工程需要：

```text
Position -> Context Needs -> Retrieval Plan -> Context Pack
```

也就是说，节点不是被动接收固定上下文，而是由 runtime 根据它的位置生成上下文获取计划。

例如：

- Normal 节点更需要子节点 handoff、open questions、失败分支摘要。
- Leaf 节点更需要 artifact 片段、文件上下文、命令结果。
- Loop 节点更需要循环变量、上轮失败原因、退出条件。
- Error handler 节点更需要失败节点的尝试历史和无效路径。

### 不足 3：Memory Store 是检索索引，不是上下文系统

SQLite 当前已经能查节点、handoff、artifact，但还缺少几类能力：

- 按 role 查询
- 按 scope 查询
- 按 task intent 查询
- 按 failure mode 查询
- 按 confidence / freshness 查询
- 查询结果的 provenance 统一表达
- 查询结果的预算裁剪和排序策略

因此下一步不只是给 `memory.Store` 加更多表，而是要在 runtime 层增加 `ContextProvider` 抽象。

### 不足 4：缺少 Context Acquisition Loop

现在流程大致是：

```text
buildNodeActivation
buildPrompt
LLM call
execute actions
decideNextStep
```

但位置工程需要把“获取上下文”变成明确阶段：

```text
build Position
plan Context Acquisition
retrieve Context
assemble ContextPack
LLM decide
execute
write handoff
```

其中 `plan Context Acquisition` 可以先是确定性规则，后续再考虑让 LLM 参与。

第一版不建议一开始就让 LLM 自由规划检索，否则会引入不可控成本和不稳定性。

### 不足 5：节点权限和职责边界还不够硬

目前 prompt 会告诉模型 Normal / Loop / Leaf 的语义，但 runtime 对职责边界的强制还不够。

位置工程应该让不同位置拥有不同动作权限：

| 节点类型 / 位置 | 默认职责 | 默认权限 |
|---|---|---|
| Root | 总目标、最终验收、顶层拆分 | 创建子节点、整合结果、请求人类确认 |
| Normal | 局部规划、协调、整合 | 创建子节点、append sibling、query memory、mark complete |
| Loop | 迭代控制、退出条件判断 | 重置子节点、更新循环变量、创建下一轮子节点 |
| Leaf | 原子执行、取证、验证 | 工具调用、读写 artifact、query memory、mark complete |
| Error Handler | 失败恢复、复盘、绕开无效路径 | 读取失败上下文、创建修复节点、请求人类输入 |

这张表应该逐步进入 runtime policy，而不是只存在于 prompt。

---

## 可以借鉴 Codex 的地方

Codex 的价值不在于它也有一个任务树，而在于它的 agent harness 设计非常成熟。

公开资料中，Codex 的 agent loop 可以概括为：

```text
user input
build prompt/input items
model inference
tool call
observe tool result
append observation
repeat
assistant final message
```

同时，Codex 非常重视：

- 工具调用和观察结果的循环
- sandbox / approval 的开发者指令
- workspace environment context
- AGENTS.md / skills 等多来源指令聚合
- context window management
- conversation compaction
- prompt cache 稳定性

这些思想可以迁移到 LLMVM，但不能照搬。

### 应该借鉴

1. **Harness 比 prompt 更重要**

   模型不是自己运行任务，runtime/harness 才是负责调度、权限、上下文、工具和终止条件的主体。

2. **工具观察是上下文获取的一部分**

   `read_file`、`search`、`execute_command`、`query_memory` 都不只是 action，而是节点补齐上下文的手段。

3. **权限上下文应独立注入**

   Codex 会把 sandbox / approval 作为高优先级环境约束注入。LLMVM 也应把节点权限、scope、允许动作作为单独的 position policy 注入。

4. **输入结构要稳定**

   Codex 重视输入 item 的稳定顺序，以降低 cache miss 和上下文噪声。LLMVM 的 `ContextPack` 也应该有稳定排序和稳定格式。

5. **上下文压缩不是失败兜底，而是常规机制**

   LLMVM 现在已有压缩等级，但后续应该让压缩变成 `ContextPack` 的常规预算阶段。

### 不应照搬

1. **不要把 LLMVM 退化成单线程聊天 agent**

   LLMVM 的核心优势是 task tree、DFS、结构化交接和可恢复状态。

2. **不要默认依赖完整会话历史**

   LLMVM 应继续坚持无状态节点调用，历史只能通过结构化状态和检索进入 prompt。

3. **不要让模型自由访问所有工具和记忆**

   位置工程要求按位置收窄权限和上下文范围。

---

## 目标架构

建议把 runtime 拆成以下概念：

```text
TaskNode
  任务树状态和交接结果

Position
  当前节点在树中的位置、角色、scope、权限和约束

Activation
  一次节点启动生命周期

ContextNeed
  当前位置推导出的上下文需求

ContextPlan
  本次要访问哪些 provider、用什么查询、预算是多少

ContextProvider
  SQLite / Artifact / VFS / KV / Vector / Runtime State

ContextPack
  经过排序、去重、压缩、预算裁剪后的最终上下文

Decision
  LLM 输出的结构化动作

Handoff
  节点完成后的结构化交接
```

建议执行路径：

```text
cursor selects current node
  -> BuildPosition(current)
  -> BuildActivation(position)
  -> PlanContext(position, activation)
  -> RetrieveContext(contextPlan)
  -> AssembleContextPack(retrievedContext)
  -> BuildPrompt(position, contextPack)
  -> LLM Call
  -> Parse Decision
  -> Execute Actions under Position Policy
  -> Persist Artifacts / Variables / Handoff / Memory
  -> Decide Cursor Movement
```

---

## 新增核心结构建议

### Position

```go
type PositionRole string

const (
    RoleRoot         PositionRole = "root"
    RolePlanner      PositionRole = "planner"
    RoleExecutor     PositionRole = "executor"
    RoleLoopControl  PositionRole = "loop_control"
    RoleErrorHandler PositionRole = "error_handler"
)

type Position struct {
    RootID       string
    NodeID       string
    ParentID     string
    Depth        int
    Path         []string
    SiblingIndex int
    NodeType     tasknode.TaskType
    Role         PositionRole
    Scope        ScopeDescriptor
    Authority    AuthorityDescriptor
    Constraints  []string
}
```

### AuthorityDescriptor

```go
type AuthorityDescriptor struct {
    CanCreateChild       bool
    CanAppendSibling     bool
    CanWriteFile         bool
    CanExecuteCommand    bool
    CanQueryMemory       bool
    CanRequestHumanInput bool
    CanMarkComplete      bool
}
```

第一版可以先只做 runtime 校验，不必做复杂 policy engine。

### ContextNeed

```go
type ContextNeed struct {
    Kind      string // ancestors, siblings, artifacts, variables, failures, files
    Query     string
    Scope     string
    Limit     int
    Budget    int
    Required  bool
    Rationale string
}
```

### ContextPlan

```go
type ContextPlan struct {
    Position Position
    Needs    []ContextNeed
    Budget   BudgetConfig
}
```

### ContextPack

```go
type ContextPack struct {
    PositionSummary string
    Required        []ContextItem
    Retrieved       []ContextItem
    ArtifactIndex   []memory.ArtifactBrief
    OpenQuestions   []string
    Omissions       []string
    BudgetUsed      int
}
```

### ContextProvider

```go
type ContextProvider interface {
    Name() string
    Retrieve(need ContextNeed) ([]ContextItem, error)
}
```

第一批 provider：

- `MemoryProvider`
- `ArtifactProvider`
- `TreeProvider`
- `RuntimeStateProvider`
- `VFSProvider`

后续再加：

- `VectorProvider`
- `KVProvider`
- `ExternalDocProvider`

---

## Context Acquisition 第一版规则

先用确定性规则，不引入额外 LLM 调用。

### Root

默认获取：

- 顶层目标
- 一级子节点状态
- 最近完成节点摘要
- open questions
- pinned artifacts

不默认获取：

- 深层叶节点命令输出
- 大量 sibling handoff

### Normal

默认获取：

- 祖先链
- 父节点目标
- 当前子节点状态
- 已完成子节点 handoff
- 失败子节点摘要
- 与当前任务相关 artifact 摘要

### Leaf

默认获取：

- 祖先链摘要
- 父节点目标
- 当前节点局部变量
- 相关 sibling handoff
- 相关 artifact 索引
- 上一次工具调用结果引用

如果 Leaf 进入 agentic loop：

- 获取最近工具结果
- 获取失败原因
- 获取无效尝试摘要

### Loop

默认获取：

- 循环目标
- 循环变量
- 上轮子节点结果
- 退出条件
- 失败原因
- 是否需要生成下一轮子节点

### Error Handler

默认获取：

- 失败节点位置
- 失败动作
- last_error
- command_output_history 摘要
- 已尝试过的修复路径
- 可选人类输入

---

## Prompt 结构建议

位置工程后的 prompt 不应该只是 “global context + current node”。

建议结构：

```text
## Position
node id, role, path, depth, scope, authority

## Objective
root goal, parent goal, current goal

## Context Pack
required context
retrieved context
artifact index
open questions
omissions

## Runtime State
loop state, retry state, human response, last error

## Current Node
node JSON

## Allowed Actions
actions allowed by this position

## Required Handoff Contract
fields required if mark_complete is used
```

这样模型看到的不只是信息，还能明确知道自己在组织中的职责和边界。

---

## 分阶段实施路线

### Phase 1：把 Position 做成一等对象

目标：

- 新增 `pkg/runtime/position.go`
- 实现 `BuildPosition(current *tasknode.TaskNode) Position`
- 根据节点类型和父子关系推导 role、scope、authority
- 在 prompt 中加入 `## Position` 和 `## Allowed Actions`
- action 执行前根据 authority 做基本校验

验收：

- 每次节点调用都有明确 Position 摘要
- Leaf 默认不能 create child，除非 policy 放开
- Root 默认不能 append sibling
- Loop 内 sibling mutation 继续受限

### Phase 2：重构 NodeActivation 为 Activation Pipeline

目标：

- 保留 `NodeActivation`，但拆出：
  - `BuildActivation`
  - `PlanContext`
  - `RetrieveContext`
  - `AssembleContextPack`
- `buildGlobalContext` 只作为兼容 wrapper
- 将现有 hierarchy / sibling / artifact 逻辑迁移到 provider

验收：

- 现有测试通过
- prompt 内容与旧版功能等价或更稳定
- context pack 中记录 omissions 和 budget used

### Phase 3：增强 Memory Provider

目标：

- 增加按作用域变量查询
- 增加 failed node / recent failure 查询
- 增加 artifact by producer / by refs 查询
- 增加 handoff by ancestor / descendant 查询
- 所有查询结果统一转成 `ContextItem`

验收：

- Normal 节点能看到已完成子节点 handoff
- Error handler 能看到失败节点上下文
- Leaf 能按 artifact ref 获取相关材料摘要

### Phase 4：Context Acquisition Loop

目标：

- 让节点可以显式输出 `request_context` 或扩展 `query_memory`
- Runtime 执行查询后，节点继续当前 activation
- 限制最大本地上下文补齐轮数，例如 3 次
- 避免每次上下文补齐都推进 DFS step

建议 action：

```json
{
  "action_type": "request_context",
  "needs": [
    {
      "kind": "artifact",
      "query": "art_12 lines 40-120",
      "rationale": "Need the failing test output before deciding the fix"
    }
  ]
}
```

验收：

- Leaf 可以在一个节点内完成“查索引 -> 读 artifact 片段 -> 决策”
- Runtime 有硬上限，不能无限查询
- 每次 request_context 都进入 artifact / memory 日志

### Phase 5：可选 Vector / KV Provider

目标：

- SQLite 继续做结构化事实层
- FTS 继续做全文关键词检索
- Vector provider 用于语义召回历史 handoff、文档、错误模式
- KV provider 用于轻量运行时配置和短期状态

原则：

- Vector 不能替代 SQLite
- Vector 结果必须带 provenance
- Vector 结果默认低置信度，需要 artifact 或 handoff 证据支撑

验收：

- 可关闭 vector provider
- 无 vector 时系统仍完整运行
- vector 只影响召回质量，不影响控制流正确性

---

## 明天优先做什么

建议第一天不要直接上 vector，也不要重写整个 memory。

最优先顺序：

1. 新建 `Position` 抽象。
2. 把 position summary 注入 prompt。
3. 给 action 执行加 position authority 校验。
4. 把 `NodeActivation` 的输出改名或包装成 `ContextPack`。
5. 增加 `ContextNeed` / `ContextPlan` 的空架子，先用确定性规则填充。

这样改动最小，但架构方向会立刻变清楚。

---

## 风险和约束

### 风险 1：抽象过早膨胀

不要一开始就引入复杂 policy engine、vector DB、multi-agent 调度。

第一版只需要：

- Position
- Authority
- ContextPlan
- ContextPack
- Provider 接口

### 风险 2：上下文获取变成新一轮上下文爆炸

每个 ContextNeed 必须有：

- scope
- limit
- budget
- rationale

没有预算的检索等于隐性全文回灌。

### 风险 3：LLM 自由规划检索导致不稳定

第一版 ContextPlan 必须由 runtime 确定性生成。

LLM 只能请求补充上下文，不能绕过 runtime policy。

### 风险 4：Codex 经验被误用

Codex 的 agent loop 值得借鉴，但 LLMVM 不能变成单线程 conversation agent。

LLMVM 的护城河是：

- 显式 task tree
- DFS cursor
- 节点 handoff
- artifact 外部记忆
- SQLite 可重建索引
- 无状态节点调用

所有改造都必须增强这些特点，而不是削弱它们。

---

## 最终目标

改造完成后，每个节点的执行语义应该变成：

```text
我知道我是谁。
我知道我在树里的位置。
我知道上级目标是什么。
我知道我本次职责边界是什么。
我知道我能访问哪些记忆和工具。
我知道我缺什么上下文。
我能按规则获取最小充分上下文。
我能基于证据做决策。
我完成后能留下结构化交接。
```

这才是 LLMVM 区别于线性 agent 的核心。

---

## 参考

- OpenAI, "Unrolling the Codex agent loop": https://openai.com/index/unrolling-the-codex-agent-loop/
- OpenAI Codex open-source repository: https://github.com/openai/codex
