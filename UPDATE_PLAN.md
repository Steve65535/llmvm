# 更新计划

## 目标

在保持当前“无状态 prompt + 显式任务树”架构的前提下，引入四个运行时级能力，用来提升可控性、执行规划能力，以及长任务中的上下文质量：

1. Human-in-the-loop 检查点：用于歧义澄清、风险审批和失败恢复。
2. Append sibling node：允许执行过程中追加同级任务节点。
3. Node activation context assembly：每个 DFS 节点做决策之前，先装配该节点所需的工作上下文。
4. SQLite-backed retrieval：用 SQLite 结构化索引取代传统零散检索，让 agent 更容易查询历史状态、artifact、handoff 和变量链。

## 为什么需要这些改动

当前任务树运行时已经具备任务分解、状态持久化和上下文预算控制能力，但还缺少四个实际工程控制面：

- 缺少一等的暂停机制，用来请求结构化的人类输入。
- 缺少一等的同级节点追加机制。新发现的工作有时属于当前节点的兄弟任务，而不是当前节点的子任务。
- 缺少一等的节点激活协议。模型在决定当前节点动作前，需要先重建“最小充分工作上下文”，而不是只依赖当前节点描述或原始对话历史。
- 缺少统一的结构化检索层。当前检索主要依赖内存索引、文本摘要和 artifact 引用，agent 很难用稳定查询条件检索“某个节点、某类 handoff、某个变量作用域、某个 artifact 元数据”。

前两个能力应该建模成显式 runtime action，而不是靠 prompt 暗示。第三个能力应该建模成每次节点级 LLM 调用前的确定性准备阶段。第四个能力应该作为 runtime 的结构化记忆和检索后端，为节点激活提供稳定查询能力。四者共同目标是：保持系统可恢复、可检查、可复盘，并继续兼容无状态 prompt 架构。

## 功能 1：Human In The Loop

### 目标

允许模型显式暂停执行，请求人类给出指导，而不是把人类回复混入自由文本聊天历史。

### 建议 Action

```json
{
  "action_type": "request_human_input",
  "question": "Should the runtime continue with destructive cleanup?",
  "context": "37 duplicate files were detected after analysis.",
  "options": ["continue", "report_only", "abort"],
  "blocking": true
}
```

### 运行时语义

- 当前节点进入等待状态。
- Runtime 将请求 payload 作为结构化状态持久化。
- 人类输入保存为结构化数据，不追加为原始 transcript。
- 恢复执行时，下一个 prompt 只接收相关决策 payload，例如：
  - response value
  - optional note
  - related node id
  - timestamp

### 建议节点/状态改动

- 增加新的节点状态，例如 `WaitingHuman`。
- 增加可选字段：
  - pending human request
  - latest human response
  - whether the request is blocking

### 触发策略

以下情况应该允许请求人类审批或输入：

- 操作具有破坏性。
- 任务存在歧义。
- 多次重试说明模型卡住。
- 需要最终确认门。
- 成本较高，或存在外部副作用。

后续可以引入可配置 policy：

- `never`
- `on_risk`
- `on_ambiguity`
- `always`

### 实现区域

- `pkg/llm/parser.go`
  - 解析 `request_human_input`
- `pkg/tasknode/tasknode.go`
  - 增加等待状态和 request/response 字段
- `pkg/runtime/runtime.go`
  - 持久化请求
  - 暂停执行
  - 将结构化人类回复注入后续 prompt
- visualizer
  - 展示等待节点和待审批上下文

## 功能 2：Append Sibling Node

### 目标

允许模型在当前节点后追加新的同级任务。当新发现的工作属于同一个父级范围，而不是当前节点的子树时，应使用同级追加。

### 建议 Action

```json
{
  "action_type": "append_sibling_node",
  "node": {
    "id": "inspect_frontend_calls",
    "name": "Inspect Frontend Call Sites",
    "type": "Leaf",
    "information": "Analyze where the frontend calls the API and summarize mismatches."
  }
}
```

### 运行时语义

- 仅当当前节点存在父节点时有效。
- 新节点插入到同一父节点下，并位于当前节点之后。
- 父节点的 children 顺序必须保持确定性。
- 禁止给 root 追加同级节点。

### 预期收益

- 执行时规划更灵活。
- 当新工作是并行关系而非嵌套关系时，任务树更干净。
- 降低当前节点承载无关后续工作的风险。

### 约束

- v1 默认只支持 append-after-current，不支持任意位置插入。
- Loop 上下文需要额外谨慎。
- 第一版可以拒绝敏感 loop 结构里的 sibling insertion，直到语义经过验证。

### 实现区域

- `pkg/llm/parser.go`
  - 解析 `append_sibling_node`
- `pkg/runtime/runtime.go`
  - 将节点插入父节点 children slice
  - 保持 traversal correctness 和 index 正确
- `pkg/tasknode/tasknode.go`
  - 如有需要，验证 parent linkage 和 ordering metadata
- visualizer
  - 准确展示插入后的 sibling 顺序

## 功能 3：节点激活上下文装配

### 目标

每个节点在做决策前，Runtime 应该从结构化状态中装配一个“角色感知、任务感知”的工作上下文，而不是依赖原始对话历史。

DFS 继续负责执行顺序，但每个节点应该拥有一个激活生命周期：

1. 识别当前节点在任务层级中的位置。
2. 加载相关父节点和祖先节点的作用域变量链。
3. 在相关时检查兄弟节点的 handoff 和 artifact。
4. 从 artifact store 和已保存状态中检索当前任务需要的上下文。
5. 构建有预算上限的 working context。
6. 让模型基于该 working context 决定当前节点动作，并产出新的结构化交接文档。

这个模型把节点从“无状态函数调用”提升为“分层组织中的岗位员工”：它知道自己在哪一层、上级目标是什么、本次职责是什么、附近团队做过什么、公司知识库里有什么，然后再开始行动。

### 第一性原理依据

LLM 本质上是无状态推理器。长任务系统因此必须把状态、记忆、决策和产物外置，并且要有纪律地加载它们。

目标不是让每个节点读取所有历史，而是让每个节点获得完成当前职责所需的最小充分上下文。

这来自三个基本约束：

- 上下文太少会导致节点失忆，产生局部正确但全局错误的动作。
- 上下文太多会增加成本、噪声和错误传播。
- 非结构化交接会让后续节点依赖脆弱散文，而不是可检查状态。

### 建议运行时阶段

在 `buildPromptWithGlobalContext` 前增加节点激活阶段，或者把当前 `buildGlobalContext` 重构成更清晰的 activation pipeline：

```go
type NodeActivation struct {
    NodeID              string
    HierarchyPath       []NodeBrief
    ParentGoal          string
    AncestorConstraints []ScopedFact
    ScopeVariables      []ScopedVariable
    SiblingHandoffs     []HandoffBrief
    ArtifactIndex       []ArtifactBrief
    SelectedArtifacts   []ArtifactSlice
    OpenQuestions       []string
    WorkingContext      string
}
```

第一版可以只在 `runtime` 内部使用这个结构，但形状应该尽量可序列化，方便后续调试和可视化。

### 上下文来源

- 当前节点字段：
  - id、name、type、information、status、retry/error context
- 父节点和祖先链：
  - goals
  - constraints
  - scoped variables
  - completed handoffs
- 兄弟节点：
  - completed sibling summaries
  - sibling handoffs
  - sibling artifact references
  - 相关的 failed sibling attempts
- Artifact store：
  - stable artifact ids
  - summaries
  - pinned artifacts
  - 从 `read_artifact` 选出的切片
- Runtime state：
  - loop context
  - pending human response payloads
  - recent tool errors
- SQLite retrieval index：
  - node metadata
  - hierarchy edges
  - scoped variables
  - handoff records
  - artifact metadata
  - artifact full-text/FTS index

### 选择策略

节点激活阶段必须同时做到 relevance-driven 和 budget-aware：

- 总是包含当前节点和直接父节点。
- 包含祖先链摘要，不包含完整祖先 transcript。
- 变量按作用域加载，近作用域优先。
- 只在兄弟节点与当前节点共享父节点，并且已完成、失败或被显式引用时加载。
- 先加载 artifact summary，只有需要时才加载 artifact slice。
- 把兄弟输出视为带 provenance 的证据，而不是绝对事实。
- pinned artifact 和显式引用 artifact 的优先级高于单纯最近使用的 artifact。

### 结构化交接 Schema

每个完成节点都应该产出未来节点可消费的结构化 handoff：

```json
{
  "goal": "What this node was responsible for",
  "summary": "What was completed",
  "key_facts": ["facts future nodes may rely on"],
  "decisions": ["important choices made"],
  "assumptions": ["unverified assumptions"],
  "artifact_refs": ["art_2", "art_5"],
  "outputs": ["files, values, or state produced"],
  "open_questions": ["known unresolved issues"],
  "handoff": "Concise downstream guidance",
  "confidence": "high"
}
```

如果模型没有提供完整 handoff，Runtime 继续生成自动兜底 handoff。但兜底内容应该标记为较低 confidence，并尽量保留 artifact references。

### 预期收益

- 节点不用读取完整历史，也能保留全局意图。
- 长 DFS 运行更稳定、更容易恢复。
- 兄弟节点成果可以影响同级工作，而不需要被错误地嵌套成子任务。
- Artifact 从不透明工具输出升级为可复用的组织记忆。
- Visualizer 可以展示节点为什么做出某个决策，因为它能显示 activation context。

### 需要防范的失败模式

- Context hoarding：每个节点读取太多。
- Sibling contamination：错误兄弟结论横向传播。
- Stale artifacts：旧状态在新证据出现后仍被当作权威。
- Handoff drift：自由文本总结在不同节点间逐渐变形。
- Hidden cost：activation 阶段引入额外 LLM 调用，导致成本失控。
- Retrieval drift：SQLite index 与 AST/artifact store 出现不一致。

### 实现区域

- `pkg/runtime/runtime.go`
  - 将 `buildGlobalContext` 拆分或重命名为 activation/context assembly 路径
  - 纳入 hierarchy path、scoped variables、sibling handoffs 和 artifact briefs
  - 保持装配过程确定性和预算感知
- `pkg/tasknode/tasknode.go`
  - 如有需要，扩展结构化 handoff 字段
  - 区分 facts、assumptions、decisions 和 open questions
- `pkg/artifact/store.go`
  - 支持 relevance metadata、pinned priority 和 selected artifact slices
  - 将 artifact metadata 和可检索文本写入 SQLite index
- `pkg/memory` 或 `pkg/retrieval`
  - 提供 SQLite schema、migration、rebuild 和受限查询 API
- `pkg/llm/parser.go`
  - 校验模型返回的 richer handoff payload
  - 如开放 `query_memory`，校验 query type 和 filters
- visualizer
  - 展示选中节点的 activation context sources
  - 展示实际加载了哪些 artifact 和 sibling handoff

## 功能 4：SQLite 结构化检索系统

### 目标

用 SQLite 作为 runtime 的本地结构化检索层，取代传统的零散文本检索和内存扫描，让 agent 可以用稳定、低成本、可复盘的方式查询任务树状态、artifact、handoff、变量和执行记录。

SQLite 不应该取代 AST 本身。AST 仍然是控制流和权威状态结构；SQLite 是面向检索、索引、调试和节点激活的查询层。

### 第一性原理依据

Agent 做长任务时，真正需要的不是“更多上下文”，而是“可查询的外部记忆”。

传统检索方式的问题是：

- 文本 grep 很难表达结构化条件，例如“同一父节点下已完成兄弟的 handoff”。
- 内存索引不利于恢复、调试和跨运行查询。
- artifact 引用只解决了存储问题，没有解决按任务、作用域、时间、可信度和类型查询的问题。
- 让 LLM 自己在长文本里找信息，会浪费上下文并增加误读概率。

SQLite 的价值在于把检索从“读一堆文本再让模型判断”变成“runtime 先用结构化查询取出候选上下文，再让模型做语义决策”。

### 建议数据表

第一版可以保持 schema 简洁，先覆盖节点激活需要的查询：

```sql
CREATE TABLE nodes (
  id TEXT PRIMARY KEY,
  parent_id TEXT,
  name TEXT,
  type TEXT,
  status TEXT,
  information TEXT,
  depth INTEGER,
  traversal_index INTEGER,
  created_at TEXT,
  updated_at TEXT
);

CREATE TABLE node_handoffs (
  node_id TEXT PRIMARY KEY,
  goal TEXT,
  summary TEXT,
  key_facts_json TEXT,
  decisions_json TEXT,
  assumptions_json TEXT,
  artifact_refs_json TEXT,
  outputs_json TEXT,
  open_questions_json TEXT,
  handoff TEXT,
  confidence TEXT,
  updated_at TEXT
);

CREATE TABLE scoped_variables (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  node_id TEXT,
  scope_node_id TEXT,
  name TEXT,
  value_ref TEXT,
  value_summary TEXT,
  updated_at TEXT
);

CREATE TABLE artifacts (
  id TEXT PRIMARY KEY,
  producer_node_id TEXT,
  kind TEXT,
  title TEXT,
  summary TEXT,
  content_ref TEXT,
  pinned INTEGER DEFAULT 0,
  confidence TEXT,
  created_at TEXT,
  last_used_at TEXT
);

CREATE VIRTUAL TABLE artifact_fts USING fts5(
  artifact_id,
  title,
  summary,
  content
);
```

后续可以再增加 `human_requests`、`runtime_events`、`tool_calls`、`errors` 等表，但 v1 不需要一次性做大。

### 查询能力

节点激活阶段应该优先通过 SQLite 完成候选上下文收集：

- 查当前节点的父链和层级路径。
- 查最近作用域变量，支持 nearest scope wins。
- 查同父节点下已完成或失败的 siblings。
- 查 siblings 的结构化 handoff 和 artifact refs。
- 查 pinned artifacts、显式引用 artifacts、最近相关 artifacts。
- 用 FTS 查询 artifact summary/content，返回候选 artifact ids 和片段。
- 按 producer node、artifact kind、confidence、created_at 做过滤。

### Runtime 语义

- AST 和 artifact store 仍然是写入权威来源。
- 每次节点状态、handoff、artifact、变量更新后，同步写入 SQLite index。
- 保存状态时，可以同时保存 SQLite 文件路径或将其视为可重建索引。
- 加载状态时，如果 SQLite index 缺失或 schema 版本不匹配，Runtime 应能从 AST + artifact store 重建索引。
- SQLite 查询结果必须带 provenance，例如 source table、node_id、artifact_id、timestamp。

### Agent 可用接口

第一版不一定要让模型直接写 SQL。更安全的方式是提供受限查询 action 或 runtime 内部查询 API：

```json
{
  "action_type": "query_memory",
  "query_type": "sibling_handoffs",
  "filters": {
    "parent_id": "node_12",
    "status": ["completed", "failed"]
  },
  "limit": 5
}
```

推荐 v1 先不开放任意 SQL，避免 prompt 注入、越权查询和 schema 耦合。Runtime 可以内部使用 SQL，但对模型暴露稳定的 query types。

### 与节点激活的关系

SQLite retrieval 是 node activation 的数据底座：

- Node activation 负责决定“当前节点需要什么上下文”。
- SQLite retrieval 负责高效找到候选上下文。
- Budget selector 负责裁剪候选结果。
- Prompt builder 负责把最终 working context 注入模型。

### 实现区域

- 新增 `pkg/memory` 或 `pkg/retrieval`
  - 管理 SQLite schema、migration、index rebuild 和查询 API
- `pkg/runtime/runtime.go`
  - 在节点、handoff、artifact、变量更新时同步写入 SQLite
  - node activation 通过 memory/retrieval 查询候选上下文
- `pkg/artifact/store.go`
  - 将 artifact metadata 和可检索文本同步到 SQLite/FTS
- `pkg/tasknode/tasknode.go`
  - 提供节点状态和 handoff 的索引输入结构
- `pkg/llm/parser.go`
  - 如开放 `query_memory`，解析并校验受限查询 action
- visualizer
  - 展示 SQLite 检索结果和命中的 provenance

### 需要防范的失败模式

- SQLite index 与 AST 权威状态不一致。
- 任意 SQL 暴露给模型导致越权查询或 schema 泄漏。
- FTS 命中结果相关性不稳定，污染 node activation。
- schema 过早复杂化，拖慢核心 runtime 演进。
- 保存/恢复时只保存 SQLite，忘记 AST 才是控制流权威来源。

## 设计原则

- `request_human_input` 和 `append_sibling_node` 保持为显式 runtime action。
- 节点激活保持为显式 runtime phase。
- 不把原始对话历史泄漏回 prompt。
- 通过结构化状态持久化保持可恢复性。
- 任务树 mutation 必须确定性，避免隐式 prompt 行为。
- 加载最小充分上下文，而不是最大可用上下文。
- 用 SQLite 做结构化候选检索，用预算选择器决定最终 prompt 内容。
- 兄弟 handoff、artifact slice、人类回复都要保留 provenance。
- 当后续节点需要消费结果时，优先使用结构化字段，而不是纯 prose。
- AST 仍然是控制流权威来源；SQLite 是可重建检索索引。
- v1 不暴露任意 SQL 给模型，只暴露受限 query types 或完全由 Runtime 内部查询。

## 推进计划

### Phase 1：Schema 和 Parser

- 在 LLM parser 中增加新的 action types。
- 校验必填字段，拒绝 malformed payload。
- 定义 richer structured handoff schema。

### Phase 2：Node Activation

- 将 global context assembly 重构为 node activation phase。
- 纳入 hierarchy path、parent goal、ancestor constraints、scoped variables、sibling handoffs 和 artifact index。
- 在 prompt 构造前执行 relevance 和 budget 规则。

### Phase 3：SQLite Retrieval

- 新增 SQLite-backed memory/retrieval 模块。
- 建立 nodes、node_handoffs、scoped_variables、artifacts 和 artifact_fts 基础表。
- 在节点、handoff、artifact、变量更新时维护索引。
- 支持从 AST + artifact store 重建 SQLite index。
- 为 node activation 提供受限查询 API。

### Phase 4：Runtime Execution

- 实现 `WaitingHuman` 处理。
- 实现当前节点后的 sibling insertion。
- 确保 save/load 支持这两个能力。

### Phase 5：Prompt Integration

- 将装配好的 node activation context 注入当前节点 prompt。
- 只把结构化 human response data 注入 prompt。
- 更新 prompt guidance，让模型知道何时使用每个新 action。
- 要求模型响应区分 facts、assumptions、decisions、outputs、open questions 和 handoff。
- 如开放 `query_memory`，明确模型只能使用受限 query types，不能写任意 SQL。

### Phase 6：Visualization 和 Debugging

- 在 visualizer 中展示 waiting states。
- 展示 sibling insertion order 和 pending human requests。
- 展示每个节点的 activation context sources。
- 展示 artifact 和 sibling handoff provenance。
- 展示 SQLite retrieval 命中的来源、过滤条件和结果数量。

### Phase 7：Tests

- 两个新 action 的 parser tests。
- richer handoff payload 的 parser tests。
- Runtime tests：
  - waiting and resume flow
  - state persistence
  - sibling insertion ordering
  - invalid root sibling append
  - loop safety checks
  - activation context budget enforcement
  - ancestor variable precedence
  - sibling handoff inclusion and exclusion
  - artifact slice selection
  - fallback handoff generation
  - SQLite index update after node/handoff/artifact changes
  - SQLite index rebuild from saved state
  - FTS artifact lookup
  - restricted query validation

## 开放问题

- Human input 恢复时，应该继续同一个节点，还是创建专门的 follow-up node？
- `append_sibling_node` v1 是否允许在 loop 内使用？
- Approval policy 应该放在 node metadata、action payload，还是 runtime config？
- Human response 应该是 single-shot，还是支持 threaded clarification？
- Node activation context 应该持久化用于 replay/debugging，还是每次 load 后确定性重建？
- Artifact relevance 应该先使用确定性规则，之后再考虑可选的 LLM-assisted selection 吗？
- SQLite index 应该随 save state 一起持久化，还是默认作为可重建缓存？
- v1 是否暴露 `query_memory` 给模型，还是只让 Runtime 内部使用 SQLite 检索？
- 是否需要引入 embedding/vector search，还是先使用 SQLite FTS5 + 结构化过滤？
- Handoff 的 confidence vocabulary 应该有哪些值？
- Sibling handoff 应该默认加载，还是只有当前节点声明 context need 时才加载？

## 建议

四个能力都值得实现，但 v1 应该保守：

- `request_human_input` 保持 blocking 且结构化。
- `append_sibling_node` 只允许插入到当前节点之后。
- loop-specific 行为先收窄，直到语义经过充分测试。
- node activation 先做成 deterministic 和 budget-aware，再考虑任何额外 LLM 检索步骤。
- SQLite retrieval 先作为 Runtime 内部结构化索引和 FTS 检索层，不开放任意 SQL 给模型。
- richer handoff 可以增量接入，保留当前 auto-handoff 作为兜底。
