# LLMVM RAG Artifact 位置工程实验设计

## 目标

本实验的目标是把 LLMVM 从“任务树 + prompt 上下文拼装”推进到“任务树 + artifact 交接 + 可检索记忆 + 可验收节点”的运行时框架。

核心变化包括：

- 引入 RAG、向量数据库、SQLite 结构化索引和混合检索。
- 把 artifact 变成 DSL 的一等公民，而不是附属工具结果。
- 增强 LLM JSON 输出协议、解析容错和错误分类能力。
- 取消固定比例式上下文分配，改成只受全局上下文上限约束。
- 取消 Loop 节点，改成每个节点都有明确验收标准。
- 每个节点像组织里的岗位一样，先检索需要的资料，再执行 ReAct。
- 大 artifact 不直接塞进主上下文，而是通过异步检索/压缩子流程返回最小充分信息。

这是一项高风险架构实验，第一阶段只写设计文档，不直接改 runtime。

---

## 核心判断

LLMVM 的长期方向不应该是让单个 agent 背更多历史，而是让每个节点拥有更清晰的位置、职责、资料获取方式和交付标准。

节点之间真正的交接对象应该是 artifact。handoff 可以保留为摘要和指令，但它不应该承载全部工作成果。更合理的模型是：

```text
上游节点
  -> 验收标准
  -> 任务说明
  -> artifact 引用
  -> 下游节点

下游节点
  -> 检索相关 artifact / memory
  -> ReAct 执行
  -> 产出细粒度 artifact
  -> 按验收标准自检
  -> 通过后 mark_complete
```

这意味着 runtime 的中心对象要从“节点上下文”扩展为：

```text
Position + Acceptance Criteria + Retrieval + Artifact + Action
```

---

## 一等 Artifact DSL

### 当前问题

当前 artifact 更像工具调用产生的外置数据，粒度和语义不够稳定。节点交接时通常依赖 summary、handoff 和 artifact refs，但 artifact 本身还不是模型显式规划、命名、切分、写入和检索的主要对象。

### 目标状态

在 DSL 中加入 `add_artifact`，作为与 `create_node`、`mark_complete` 同级的一等 action。

建议 DSL 形态：

```json
{
  "action": "add_artifact",
  "name": "parser_error_cases",
  "artifact_type": "evidence",
  "scope": "node",
  "summary": "Parser tests that define invalid action handling.",
  "content": "...",
  "source": {
    "kind": "file",
    "path": "pkg/llm/parser_new_test.go"
  },
  "tags": ["parser", "tests", "failure-handling"],
  "granularity": "case-level",
  "importance": "high"
}
```

### Artifact 粒度要求

artifact 不应该过粗。一个 artifact 应该只表达一个可复用的信息单元。

建议粒度：

- 一个失败窗口。
- 一个测试用例簇。
- 一个接口契约。
- 一个设计决策。
- 一个文件片段及其用途。
- 一个命令结果摘要。
- 一个子节点交付物。

不建议粒度：

- 整个仓库摘要。
- 整个模型回复。
- 整个日志文件。
- 多个无关发现混在一个 artifact。

### Artifact 元数据

建议最小元数据：

```go
type Artifact struct {
    ID          string
    NodeID      string
    Name        string
    Type        ArtifactType
    Scope       ArtifactScope
    Summary     string
    Content     string
    Source      ArtifactSource
    Tags        []string
    Granularity string
    Importance  string
    TokenCount  int
    CreatedAt   time.Time
}
```

artifact 写入时必须同时进入：

- SQLite：结构化索引、FTS、关系查询、审计。
- Vector DB：语义检索。

SQLite 是事实账本，Vector DB 是语义入口。两者不互相替代。

---

## RAG 与混合检索

### 检索目标

节点激活后不应该被动接收大量上下文。它应该先根据自己的 position、目标和验收标准执行资料搜集。

这相当于一个员工接到任务后，先去组织数据库里查资料，再开始推理和行动。

节点流程：

```text
activate node
  -> read position
  -> read acceptance criteria
  -> formulate context needs
  -> hybrid retrieval
  -> rerank
  -> build context pack
  -> ReAct loop
```

### 检索层次

建议 retrieval pipeline：

```text
Query Planner
  -> SQLite filters
  -> SQLite FTS
  -> Vector search
  -> Merge
  -> Rerank
  -> Context Pack Builder
```

SQLite 负责：

- 节点 ID、祖先链、兄弟节点、子节点。
- artifact 类型、scope、tag、importance。
- 创建时间、来源文件、变量名。
- FTS 关键词检索。

向量数据库负责：

- 语义相似 artifact。
- 近似问题、近似决策、近似失败模式。
- 不是关键词相同但意图相近的上下文。

Rerank 负责：

- 当前节点目标相关性。
- 验收标准相关性。
- 位置相关性，优先祖先、同级、直接依赖。
- artifact 粒度和可信度。
- 是否过大、是否需要异步缩小。
- 做并行检索 并发模型

### 检索 API 草案

```go
type RetrievalQuery struct {
    NodeID             string
    Position           Position
    Goal               string
    AcceptanceCriteria []AcceptanceCriterion
    Need               string
    Filters            RetrievalFilters
    MaxTokens          int
}

type RetrievalResult struct {
    Items       []RetrievedArtifact
    RerankTrace []RerankDecision
    TokenCount  int
}
```

---

## 上下文上限策略

### 取消比例限制

当前不应该再用固定比例限制某类上下文，例如 artifact 只能占多少、handoff 只能占多少。比例限制容易导致关键资料被排除，也会让 runtime 变成僵硬的模板拼装。

新策略：

- `.env` 配置全局上下文上限。
- 每次节点激活只要总 token 不超过上限即可。
- Context Pack Builder 根据相关性、验收标准和位置决定内容比例。

建议 `.env`：

```env
LLMVM_CONTEXT_TOKEN_LIMIT=120000
LLMVM_RETRIEVAL_TOKEN_LIMIT=60000
LLMVM_ARTIFACT_INLINE_TOKEN_LIMIT=6000
LLMVM_ARTIFACT_ASYNC_TOKEN_THRESHOLD=12000
```

含义：

- `LLMVM_CONTEXT_TOKEN_LIMIT`：最终发给模型的上下文总上限。
- `LLMVM_RETRIEVAL_TOKEN_LIMIT`：检索候选内容预算。
- `LLMVM_ARTIFACT_INLINE_TOKEN_LIMIT`：artifact 可以直接进入主上下文的上限。
- `LLMVM_ARTIFACT_ASYNC_TOKEN_THRESHOLD`：超过后触发异步 artifact 缩小流程。

---

## 大 Artifact 异步缩小模型

### 问题

大 artifact 不能直接进入主上下文，否则会压缩当前节点真正需要的推理空间。直接截断也不可靠，因为关键内容可能在后半段。

### 方案

当 artifact 超过阈值时，runtime 启动一个 goroutine 执行 artifact-specific retrieval / summarization。

主流程可以阻塞等待结果，但阻塞对象不是全量 artifact，而是一个“足够小、足够精确”的 context slice。

```text
main node retrieval
  -> finds large artifact
  -> spawn artifact resolver goroutine
  -> resolver reads full artifact / chunks
  -> resolver answers current context need
  -> returns compact evidence slice
  -> main context pack receives slice
```

这类似 subagent，但职责更窄：不是替主节点完成任务，而是替主节点从大 artifact 中取出当前需要的信息。

### Resolver 输入输出

```go
type ArtifactResolveRequest struct {
    ArtifactID          string
    NodeID              string
    CurrentGoal         string
    AcceptanceCriteria  []AcceptanceCriterion
    ContextNeed         string
    MaxTokens           int
}

type ArtifactResolveResult struct {
    ArtifactID string
    Summary    string
    Evidence   []EvidenceSpan
    Confidence float64
}
```

resolver 可以使用：

- chunk-level vector search。
- SQLite FTS。
- LLM 精读压缩。
- 文件片段定位。

第一阶段可以先实现同步接口，内部预留 goroutine 模型。等行为稳定后再并发化。

---

## 取消 Loop 节点

### 原因

Loop 节点容易把“重复尝试”做成结构类型，而不是验收协议。真正需要的是：

- 节点知道什么算完成。
- 节点执行后能自检。
- 不通过时能修正或 compact。
- 超过限制时能把失败状态结构化交接。

因此 loop 不应是一种节点类型，而应是节点执行协议的一部分。

### 新模型

每个节点都有验收标准：

```go
type AcceptanceCriterion struct {
    ID          string
    Description string
    SourceNodeID string
    Required    bool
    CheckType   AcceptanceCheckType
}
```

验收标准由上游节点给出，通常是父节点，也可以来自更高祖先节点的继承约束。

节点结束条件：

```text
all required acceptance criteria pass
  -> mark_complete
else if retry budget remains
  -> continue ReAct with failure reason
else
  -> compact failure state / escalate
```

这把原来的 loop 能力下沉为：

- retry budget
- acceptance check
- compact on limit
- escalation handoff

---

## 节点 ReAct 协议

建议节点执行协议：

```text
1. Activation
   - 加载 position
   - 加载目标
   - 加载继承验收标准

2. Context Gathering
   - 生成 context needs
   - 混合检索
   - rerank
   - 处理大 artifact
   - 构建 context pack

3. ReAct Execution
   - reasoning
   - action
   - observation
   - add_artifact when useful

4. Artifact Commit
   - 细粒度命名
   - SQLite 写入
   - Vector 写入
   - 大内容截断展示，完整内容外置

5. Acceptance
   - 对照验收条件
   - 生成 evidence
   - 通过则 mark_complete
   - 不通过则继续或 compact
```

`mark_complete` 不应只表示“模型认为完成”，而应表示“验收标准已满足，并带有证据”。

---

## DSL 变化草案

### create_node

`create_node` 需要新增验收标准字段：

```json
{
  "action": "create_node",
  "goal": "Implement parser support for add_artifact.",
  "acceptance_criteria": [
    {
      "description": "Parser accepts add_artifact with required name, type, summary, and content fields.",
      "required": true,
      "check_type": "testable"
    },
    {
      "description": "Invalid add_artifact payload returns a structured parser error.",
      "required": true,
      "check_type": "testable"
    }
  ]
}
```

### add_artifact

新增 action：

```json
{
  "action": "add_artifact",
  "name": "retrieval_pipeline_contract",
  "artifact_type": "design_contract",
  "summary": "Contract for hybrid retrieval and rerank pipeline.",
  "content": "...",
  "tags": ["retrieval", "rerank", "runtime"],
  "granularity": "contract-level"
}
```

### mark_complete

`mark_complete` 需要携带验收结果：

```json
{
  "action": "mark_complete",
  "summary": "...",
  "artifact_refs": ["artifact_123"],
  "acceptance_results": [
    {
      "criterion_id": "ac_1",
      "passed": true,
      "evidence_artifact_refs": ["artifact_123"],
      "notes": "Parser test covers valid add_artifact payload."
    }
  ]
}
```

---

## JSON 解析增强

当前 LLMVM 的运行时高度依赖模型返回结构化 action。只要模型输出被 Markdown、解释文本、半截 JSON 或字段错误污染，runtime 就会进入重试或失败路径。

因此 JSON 解析不能只依赖普通 `json.Unmarshal`，而应该做成一条分层防线：

```text
Prompt contract
  -> model output format constraint
  -> JSON extraction
  -> schema validation
  -> typed error diagnosis
  -> repair / retry guidance
```

### 1. Prompt 层面的改造

系统 prompt 需要继续强化“只能输出单个 JSON 对象”的协议，但表达方式要更操作化：

- 明确禁止 Markdown code block。
- 明确禁止前置解释和结尾说明。
- 明确要求顶层必须是对象，且必须包含 `actions` 数组。
- 明确说明 action 字段只能使用 DSL 中定义的 key。
- 在失败重试时，把上一次解析错误分类注入 prompt，而不是只给一段模糊错误文本。

重试 prompt 应该告诉模型：

```text
Your previous response failed JSON parsing.
Error type: missing_required_field
Path: actions[0].artifact_type
Fix only the JSON output. Return one raw JSON object.
```

这样模型知道应该修字段，而不是重新解释任务。

### 2. 直接约束模型输出格式

只靠 prompt 约束不够。运行时应优先使用模型 API 的结构化输出能力，例如 JSON mode、response format 或 JSON schema。

目标是把“请你输出 JSON”升级成“API 层强制模型只能返回 JSON”。

建议能力分层：

- 如果 provider 支持 JSON schema，使用完整 action schema 约束。
- 如果只支持 JSON object mode，至少强制顶层输出合法 JSON object。
- 如果 provider 不支持结构化输出，退回 prompt-only 模式，但 parser 要进入更强容错路径。

这层约束的价值是把格式错误前移到模型解码阶段，减少 runtime 后处理压力。

### 3. 更强的纠错和错误分类机制

parser 应该返回结构化错误，而不是只有一条字符串。

建议错误类型：

```go
type ParseErrorKind string

const (
    ParseErrorNonJSON             ParseErrorKind = "non_json_output"
    ParseErrorMalformedJSON       ParseErrorKind = "malformed_json"
    ParseErrorTrailingText        ParseErrorKind = "trailing_text"
    ParseErrorMissingRequired     ParseErrorKind = "missing_required_field"
    ParseErrorInvalidActionType   ParseErrorKind = "invalid_action_type"
    ParseErrorInvalidFieldType    ParseErrorKind = "invalid_field_type"
    ParseErrorInvalidEnumValue    ParseErrorKind = "invalid_enum_value"
    ParseErrorUnknownField        ParseErrorKind = "unknown_field"
    ParseErrorSchemaViolation     ParseErrorKind = "schema_violation"
)
```

结构化错误至少包含：

- `kind`：错误类型。
- `path`：错误发生位置，例如 `actions[1].node.type`。
- `message`：人类可读说明。
- `repair_hint`：给下一轮模型的最小修复建议。
- `raw_excerpt`：附近输出片段，注意长度限制。

runtime 可以根据错误类型选择策略：

- 纯格式错误：要求模型只重发 JSON。
- 缺字段或 enum 错误：注入精确 schema 提示。
- 多次同类错误：降低任务复杂度，或请求人类介入。
- 输出混入解释文本：提示只保留 JSON 对象。

### 4. 从第一个大括号开始解析 JSON

很多模型会输出：

```text
Here is the result:
{"actions":[...]}
```

第一版 parser 可以做保守提取：

1. 找到第一个 `{`。
2. 从该位置开始进行括号平衡扫描。
3. 找到与第一个 `{` 匹配的最后一个 `}`。
4. 只把这段候选内容交给 JSON parser。
5. 如果候选 JSON 后面还有非空文本，记录 `trailing_text` 警告或错误。

这比简单 `TrimPrefix("```json")` 更稳，因为它能处理前置解释、非标准 code fence 和结尾噪音。

需要注意：

- 不能用简单正则贪婪匹配 `{.*}`，字符串里的 `{`、`}` 会误伤。
- 扫描时必须识别 JSON 字符串和转义字符。
- 如果第一个 `{` 后无法找到平衡 `}`，返回 `malformed_json`。
- 如果输出中存在多个 JSON 对象，默认只接受第一个完整对象，并把后续内容记录为 `trailing_text`。

### 实现建议

第一阶段先把 parser 拆成四层：

```text
CleanLLMOutput(raw)
ExtractJSONObject(cleaned)
DecodeResponse(jsonObject)
ValidateResponseSchema(response)
```

其中：

- `CleanLLMOutput` 只做空白和 code fence 清理。
- `ExtractJSONObject` 负责从第一个大括号提取完整 JSON object。
- `DecodeResponse` 只负责 JSON decode。
- `ValidateResponseSchema` 负责 action 类型、必填字段、枚举值和 unknown field 检查。

这样 format 错误、schema 错误和 DSL 业务错误可以清晰分开，后续也更容易接入 provider 的 JSON schema 输出能力。

---

## 数据存储设计

### SQLite 表

建议新增或扩展：

```text
artifacts
artifact_chunks
artifact_fts
artifact_links
acceptance_criteria
acceptance_results
retrieval_events
rerank_events
```

SQLite 需要保留：

- 原始 artifact 元数据。
- artifact chunk 的边界。
- artifact 与 node / source / parent artifact 的关系。
- 每次检索选择了什么、丢弃了什么、为什么。
- 每次验收的证据。

### Vector DB

Vector DB 最小需要存：

```text
chunk_id
artifact_id
node_id
embedding
summary
tags
source metadata
```

第一阶段可选实现：

- 本地嵌入文件 + SQLite 向量扩展。
- 外部向量库适配层。
- 先用接口和 mock，后续再接真实向量库。

关键是先定接口，不要把 runtime 绑死在某个具体向量数据库上。

---

## 粒度控制策略

节点的影响产出要控制粒度。过大节点会导致验收困难，过小节点会导致调度和检索噪音过多。

建议原则：

- 一个节点负责一个可验收目标。
- 一个节点的主要产出最好能用 1 到 5 个核心 artifact 表达。
- 一个 artifact 负责一个可复用事实、证据、设计契约或交付物。
- 如果一个节点需要超过 3 类不同技能，应该拆分。
- 如果验收标准无法写清楚，节点粒度通常太粗。
- 如果 artifact 只能命名成 `misc`、`notes`、`context`，artifact 粒度通常太粗。

节点拆分依据：

- 不同文件或模块边界。
- 不同风险类型。
- 不同验收方式。
- 不同资料来源。
- 不同产出对象。

---

## 实验分阶段

### Phase 0：文档与接口草案

- 完成本设计文档。
- 梳理现有 DSL、parser、runtime、memory、artifact store。
- 确定最小实验边界。

### Phase 1：Artifact DSL 一等化

- 新增 `add_artifact` action。
- parser 支持结构化 artifact payload。
- runtime 将 artifact 写入 SQLite。
- `mark_complete` 强制引用重要 artifact。

### Phase 2：验收标准替代 Loop

- `create_node` 支持 `acceptance_criteria`。
- 节点继承祖先标准。
- `mark_complete` 支持 `acceptance_results`。
- loop 节点进入 deprecated 状态。

### Phase 3：RAG 检索接口

- 新增 retrieval service 接口。
- SQLite FTS + metadata filter。
- Vector store 接口先 mock。
- 实现 merge + rerank 的可观测 trace。

### Phase 4：大 Artifact Resolver

- artifact chunking。
- 大 artifact 阈值判断。
- resolver 同步版本。
- goroutine 异步版本。

### Phase 5：上下文上限重构

- `.env` 读取全局上下文上限。
- 删除固定比例预算。
- Context Pack Builder 根据 rerank 结果动态填充。

---

## 风险

- DSL 变复杂，parser 和测试需要同步扩大。
- artifact 过细会导致检索噪音变大。
- artifact 过粗会导致 resolver 成本上升。
- 向量库引入会增加部署复杂度。
- rerank 如果不可观测，失败时很难调试。
- 取消 Loop 节点会影响已有任务树语义，需要迁移策略。
- 异步 resolver 如果没有超时和预算控制，可能拖慢主流程。

---

## 最小可行实验边界

建议第一轮只做：

1. `add_artifact` DSL。
2. SQLite artifact 细粒度写入。
3. `create_node.acceptance_criteria`。
4. `mark_complete.acceptance_results`。
5. 一个简化 retrieval service：
   - metadata filter
   - SQLite FTS
   - mock vector score
   - deterministic rerank

暂时不做：

- 真实外部向量数据库。
- 多模型 rerank。
- 真正并发 resolver。
- 完整 loop 删除。

这样可以先验证核心协议是否成立，再决定是否继续扩大实验。

---

## 待确认问题

- 向量数据库优先使用本地嵌入方案，还是接外部服务。我的回答 本地方案
- artifact 是否允许跨节点修改，还是只允许追加新版本。 artifact允许跨节点 修改 本质上他们没有一个特定的库 可以修改artifact 增加一个修改artifact指令
- 验收标准是否全部由模型生成，还是 runtime 提供模板。提供一下模板 指令结合模板进行生成
- acceptance check 是否需要自动执行测试命令。可以的
- resolver 是纯检索压缩，还是允许调用工具读取文件。允许工具读取文件
- loop 节点是立即删除，还是先 deprecated 并做兼容层。立即删除 loop语义表示不清 而且问题很大
