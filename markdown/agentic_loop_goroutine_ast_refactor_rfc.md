# Agentic Loop Goroutine 化与 AST 架构适配 RFC

> 日期：2026-05-29
> 状态：准备编码前设计稿
> 目标：参考成熟 agent runtime 的可用工程模式，用 Go 重构 LLMVM 的 agentic loop，同时保留 AST-first 架构，不推翻现有 runtime / tasknode / cursor / artifact / memory 边界。

---

## 1. 结论

LLMVM 不应该把 agentic loop 升级成新的架构核心。相反，应该把 agentic loop 降级为 runtime 管理下的 goroutine worker。

新的架构原则：

```text
AST task tree      = 控制流与持久化权威源
Runtime controller = 唯一状态 mutation 入口
Agent loop worker  = 临时执行协程，只处理一轮或多轮 LLM/tool 交互
Event log          = 可观察、可恢复、可调试的事实流
Artifact store     = 工具输出、证据、观察结果的默认归宿
SQLite memory      = 可重建索引，不是权威状态
```

agent loop goroutine 不能直接推进 cursor，不能直接改变全局 AST 生命周期状态，不能私自认定节点完成。它只能接收 `AgentJob`，返回 `AgentEvent` / `ActionObservation`。runtime controller 统一执行：

```text
validate -> apply to AST -> write artifact/memory -> persist -> advance cursor
```

这能同时吸收成熟 agent 架构的稳定做法，又保留 LLMVM 最有价值的 AST 架构。

---

## 2. 参考架构抽象

本文不照搬任何框架，只吸收已经被验证的工程原则。

### 2.1 LangGraph 类状态图

可借鉴点：

- 显式状态和节点转移。
- human-in-the-loop 是图中的中断/恢复点。
- memory / context 是 runtime 管理的状态，不只靠 prompt 历史。

落到 LLMVM：

- AST 已经是更强的控制流图，不需要再引入 LangGraph 式 Python graph。
- 应该把 leaf agent loop 的状态也显式化，例如 `Ready -> Running -> Observed -> RepairMode -> Completed/Failed/Blocked`。
- human wait 继续由 `TaskStatus=WaitingHuman` 表达，goroutine 退出，恢复时重新派发 job。

### 2.2 Temporal 类 durable workflow

可借鉴点：

- workflow 负责编排，activity 负责非确定性副作用。
- worker 是可丢弃的，权威进度来自持久化状态。
- crash 后不恢复 goroutine，而是从 durable state 重新调度。

落到 LLMVM：

- AST + saved JSON 相当于 workflow state。
- LLM call、shell command、file tool、memory query 都是 activity。
- agent loop goroutine 是 worker，不是 durable state。
- 每次 activity 结果必须落 artifact/event，不能只存在内存或 stdout。

### 2.3 Microsoft Agent Framework / AutoGen 类 agent session

可借鉴点：

- agent、tool、session、middleware 分层。
- tool 调用需要统一拦截、校验、遥测。
- 多 agent 协作不应共享不可控的隐式状态。

落到 LLMVM：

- `pkg/llm` 继续负责模型边界、parser 和 action schema。
- `pkg/runtime` 继续负责 tool policy、sandbox、acceptance。
- 新增 `pkg/agentloop` 只做 job loop，不引入多 agent 聊天抽象。
- 所有 action 经过 runtime dispatcher 或 executor registry，不能让 worker 直接执行任意副作用。

### 2.4 Pi / OpenClaw 类 run-event-flow

可借鉴点：

- 每轮 run / flow / tool event 可 inspect。
- loop 有 max iterations、continue condition、abort/timeout。
- 工具结果持久化 hook 是 agent loop 的核心路径。

落到 LLMVM：

- 每个 leaf node 拥有一串 `LoopAttempt`。
- 每个 action 产生 `ActionTrace`。
- 每个 command/file/tool 结果产生 `ActionObservation` artifact。
- visualizer 后续展示 per-node attempt timeline。

---

## 3. 当前系统边界

现有架构中重要边界必须保留：

| 模块 | 保留职责 |
|---|---|
| `pkg/tasknode` | AST 节点、状态、handoff、acceptance、human payload |
| `pkg/cursor` | AST 遍历位置 |
| `pkg/runtime` | controller、prompt/context、action dispatch、sandbox、persistence、memory sync |
| `pkg/llm` | provider API、system prompt、response parser、action validation |
| `pkg/artifact` | 大输出、证据、command/file/tool 观察结果 |
| `pkg/memory` | SQLite 结构化索引和查询 |

重构的核心不是替换它们，而是把当前 `Execute` 中混在一起的循环拆成：

```text
Runtime controller
  -> schedule AgentJob
  -> receive AgentEvent
  -> apply event transactionally
  -> decide cursor transition
```

---

## 4. 新架构总览

### 4.1 组件图

```text
cmd/main.go
  |
  v
pkg/runtime.Controller
  | owns
  |-- AST task tree
  |-- cursor
  |-- artifact store
  |-- memory index
  |-- persistence hooks
  |
  | schedules
  v
pkg/agentloop.Worker goroutine
  | receives AgentJob snapshot
  | calls llm.Engine through injected interface
  | proposes actions / observations
  v
AgentEvent channel
  |
  v
runtime applies event and advances AST
```

### 4.2 轻量 Workflow 语义

这里的 workflow 仍然采用 agentic loop 思想，但它不是重量级 workflow engine，也不是新的 AST 替代品。它只是 leaf/node execution 的轻量运行协议。

核心流程：

```text
AST node selected by cursor
  -> ContextInitializer 收集初始上下文
  -> AgentMainLoop worker 启动
  -> LLM 产生 action/cmd/context request
  -> Runtime 执行 action 并产生 observation
  -> 如果 observation 超长，ArtifactGetter worker 精确取上下文
  -> MainLoop 追加压缩后的 observation/context
  -> 直到 completion / repair / blocked / failed
  -> Handoff writer 异步写 SQLite + vector DB
```

这更接近 Claude Code 一类工程 agent 的实际工作方式：主循环不断执行、观察、检索、压缩和追加上下文，但每一步都被 runtime 记录成 artifact/event，而不是让所有历史直接污染 prompt。

### 4.3 Context Initialization

AST 节点进入 agentic loop 前，runtime 必须先做一次确定性的上下文初始化。

输入：

- 当前 node goal / information / acceptance criteria。
- ancestor chain 的 handoff / decisions / assumptions。
- sibling outputs 和 failed dependency 摘要。
- SQLite 中结构化查询结果。
- vector DB 中语义召回结果。
- 当前 node 已有 artifact refs 和 pinned artifacts。

输出：

```go
type InitialContextPack struct {
    NodeID          string
    Goal            string
    Acceptance      []AcceptanceCriterionView
    AncestorContext []ContextItem
    SiblingContext  []ContextItem
    MemoryContext   []ContextItem
    VectorContext   []ContextItem
    ArtifactContext []ContextItem
    Budget          ContextBudget
}
```

规则：

- ContextInitializer 在 runtime controller 内执行，不在 worker 内直接读 AST 可变指针。
- SQLite/vector 查询可以并行，但结果进入 prompt 前必须稳定排序。
- 初始上下文只放摘要、证据 ID、必要切片，不直接塞入大 artifact。
- 每个 context item 必须带 provenance：`source=node|sqlite|vector|artifact`、`node_id`、`artifact_id`、score 或 timestamp。

### 4.4 AgentMainLoop Worker

AgentMainLoop 是主 agentic loop goroutine。它持有本轮 snapshot 和一个局部 context buffer。

职责：

- 构建本轮 prompt。
- 调用 LLM。
- 解析 action。
- 把 action proposal 交给 runtime。
- 接收 runtime observation。
- 把短 observation 追加到局部 context buffer。
- 对超长 observation 发起精确 artifact/context 获取请求。

它不负责：

- AST 状态修改。
- cursor 推进。
- SQLite/vector DB 权威写入。
- 判断节点最终完成。

主循环形态：

```text
while budget and not terminal:
  prompt = render(initial_context + local_context + loop_state)
  response = call_model(prompt)
  actions = parse(response)
  emit actions to runtime
  observations = receive observations
  for obs in observations:
      if obs.fits_budget:
          local_context.append(obs.summary)
      else:
          precise = artifact_getter.fetch(obs, current_need)
          local_context.append(precise)
```

这个 loop 是轻量、可取消、可丢弃的。恢复时重新从 AST + artifact + memory 构造 context，不恢复 goroutine 内存。

### 4.5 ArtifactGetter Worker

ArtifactGetter worker 专门解决一个问题：命令、文件、搜索或 tool 返回结果超长时，主 loop 不应该直接吞下全文，也不应该只靠粗暴截断丢失关键信息。

触发条件：

- command stdout/stderr 超过 inline budget。
- read_file/search result 超过 inline budget。
- artifact slice 仍然太大。
- LLM 明确请求某个 artifact 的精确局部信息。

输入：

```go
type ArtifactContextRequest struct {
    NodeID          string
    ArtifactID      string
    Need            string // 当前 agent 要解决的问题
    FailureSignature string
    MaxTokens       int
    Hints           []string // file path, symbol, line, regex, test name
}
```

输出：

```go
type ArtifactContextResult struct {
    ArtifactID string
    Slices     []ArtifactSlice
    Summary    string
    Tokens     int
    Truncated  bool
    Provenance []string
}
```

工作方式：

- 先用结构化切片：line range、symbol、test name、error span。
- 再用关键词/regex 定位。
- 必要时调用 resolver 做二次摘要。
- 保证返回内容低于 `MaxTokens`。
- 返回给 AgentMainLoop 前写入 artifact/event，避免上下文只存在 worker 内存中。

关键原则：

- ArtifactGetter 是上下文净化层，不是新的推理 agent。
- 它只获取“当前精确需要的上下文”，不能把大 artifact 重新灌进 main loop。
- 如果无法压到预算内，返回摘要 + provenance + 建议下一次更窄的 query。

### 4.6 Context 污染控制

主 loop 的 context buffer 必须分层：

```text
InitialContext    // runtime 初始化，稳定、可复现
LoopObservations  // 本轮短 observation
FocusedArtifacts  // ArtifactGetter 精确切片
RepairState       // failure signature / progress signals
HandoffDraft      // 即将交接的结构化摘要
```

污染控制规则：

- command 全量输出永远进 artifact，不直接进 prompt。
- prompt 只接收 summary、failure signature、必要 line slice。
- repeated irrelevant output 不重复追加。
- 同一 artifact 多次读取时按 `artifact_id + slice_range + need_hash` 去重。
- local context 超预算时，优先丢弃低价值 observation，保留 acceptance/failure/handoff 相关内容。

### 4.7 核心原则

1. **Worker 不持久**
   goroutine 可以随时退出。进程恢复时只从 AST JSON、artifact metadata、memory index 和 event log 重建。

2. **Worker 不拥有 AST**
   `AgentJob` 只携带 snapshot。worker 不拿 `*tasknode.TaskNode` 指针。

3. **Runtime 是唯一 mutation owner**
   节点状态、cursor、artifact pin、SQLite sync、save/load 兼容都只能由 runtime controller 处理。

4. **Action 结果 observation 化**
   shell exit 1、parser retry、acceptance failure 都是 observation。只有 runtime 本身无法执行动作才是 infrastructure error。

5. **完成是 transaction**
   `mark_complete` 是模型的完成申请，不是完成事实。runtime acceptance 通过后才 `Completed`。

---

## 5. 包结构建议

第一版尽量少动现有包，但引入清晰新边界。

```text
pkg/agentloop/
  job.go          // AgentJob, AgentSnapshot
  event.go        // AgentEvent, ActionObservation, LoopAttempt
  worker.go       // goroutine worker 主循环
  fingerprint.go  // action fingerprint / failure signature helper

pkg/runtime/
  controller.go   // 逐步从 Execute 抽出的主状态机
  scheduler.go    // node -> AgentJob
  apply_event.go  // AgentEvent -> AST/artifact/memory transaction
  events.go       // runtime event sink
```

短期也可以先把 `pkg/agentloop` 的类型放在 `pkg/runtime/internal` 里，等边界稳定后再迁移为公开包。但因为这个工程已经有清晰包边界，建议直接新建 `pkg/agentloop`，并保持依赖方向：

```text
pkg/agentloop -> pkg/llm
pkg/runtime   -> pkg/agentloop
pkg/agentloop 不依赖 pkg/runtime
pkg/agentloop 不依赖 pkg/tasknode 的可变指针
```

---

## 6. 核心数据结构

### 6.1 AgentJob

```go
type AgentJob struct {
    RunID       string
    NodeID      string
    NodeType    string
    Iteration   int
    MaxAttempts int

    Snapshot    AgentSnapshot
    LoopState   LoopState
    Budget      ContextBudget

    Deadline    time.Time
}

type AgentSnapshot struct {
    InitialRequest string
    NodeGoal       string
    NodeInfo       []string
    ParentChain    []NodeSummary
    Children       []NodeSummary
    Variables      map[string]any
    ArtifactViews  []ArtifactView
    MemoryViews    []MemoryView
    Acceptance     []AcceptanceCriterionView
}
```

`AgentSnapshot` 是只读数据。里面可以包含 artifact ID、summary、slice，但不应该复制大内容。

### 6.2 AgentEvent

```go
type AgentEvent struct {
    RunID     string
    NodeID    string
    AttemptID string
    Kind      AgentEventKind
    Payload   any
    CreatedAt time.Time
}

type AgentEventKind string

const (
    EventAttemptStarted   AgentEventKind = "attempt_started"
    EventModelReturned    AgentEventKind = "model_returned"
    EventActionProposed   AgentEventKind = "action_proposed"
    EventActionObserved   AgentEventKind = "action_observed"
    EventCompletionAsked  AgentEventKind = "completion_asked"
    EventAttemptCompleted AgentEventKind = "attempt_completed"
    EventWorkerFailed     AgentEventKind = "worker_failed"
)
```

### 6.3 ActionObservation

```go
type ActionObservation struct {
    ActionType       string
    Status           ObservationStatus
    ArtifactRef      string
    Summary          string
    ExitCode         *int
    FailureSignature string
    Error            string
    StartedAt        time.Time
    EndedAt          time.Time
}

type ObservationStatus string

const (
    ObservationOK          ObservationStatus = "ok"
    ObservationNonZeroExit ObservationStatus = "nonzero_exit"
    ObservationRejected    ObservationStatus = "rejected"
    ObservationRuntimeErr  ObservationStatus = "runtime_error"
)
```

关键语义：

- `exit code != 0` 是 `ObservationNonZeroExit`，不等于 worker 崩溃。
- `ObservationRejected` 表示 parser/action validation/sandbox/authority 拒绝。
- `ObservationRuntimeErr` 表示工具无法执行，例如 timeout、进程启动失败、artifact store 写失败。

### 6.4 LoopAttempt

```go
type LoopAttempt struct {
    ID                 string
    NodeID             string
    Iteration          int
    StartedAt          time.Time
    EndedAt            time.Time
    ActionFingerprints []string
    ObservationRefs    []string
    FailureSignature   string
    Progress           ProgressSignals
    Outcome            AttemptOutcome
}

type AttemptOutcome string

const (
    AttemptProgressed AttemptOutcome = "progressed"
    AttemptNoProgress AttemptOutcome = "no_progress"
    AttemptRepair     AttemptOutcome = "repair"
    AttemptCompleted  AttemptOutcome = "completed"
    AttemptBlocked    AttemptOutcome = "blocked"
    AttemptFailed     AttemptOutcome = "failed"
)
```

---

## 7. Runtime 状态机

### 7.1 Controller 主循环

目标结构：

```text
for !cursor.Done():
  current := cursor.Current

  if current.IsTerminal():
      decideNextStep(current)
      continue

  if current.Status == WaitingHuman:
      return ErrWaitingHuman

  if current.Type == Normal:
      schedule or replan children
      continue

  if current.Type == Leaf:
      job := scheduler.BuildAgentJob(current)
      events := agentloop.RunAttempt(ctx, job)
      controller.Apply(events)
      controller.DecideLeafTransition(current)
      continue
```

第一版可以不做长期驻留 worker pool，先做“一次 leaf attempt 一个 goroutine”：

```go
events, err := r.agentRunner.RunAttempt(ctx, job)
```

内部仍然用 goroutine/channel 实现。这样更容易测试，也不会一次性改变 `Execute` 的外部行为。

### 7.2 Leaf 状态机

```text
Pending/Running
  -> AttemptRunning
  -> Observed
  -> Continue
  -> RepairMode
  -> CompletionCandidate
  -> Completed
  -> Failed
  -> Blocked
```

规则：

- 模型普通 action 成功，但未申请完成：继续 leaf loop。
- command 非零退出：记录 observation，更新 failure signature，继续或进入 repair mode。
- parser/action validation 错误：记录 rejected observation，反馈给下一轮。
- `mark_complete`：进入 completion candidate，runtime 跑 acceptance。
- acceptance 通过：`Status=Completed`。
- acceptance 失败：记录 failure artifact，进入 repair mode。
- 同一 failure signature 连续 N 次：repair mode prompt。
- repair mode 仍无进展超过 M 次：`Status=Blocked` 或 `Failed`，由策略决定。

### 7.3 Normal 父节点调度

Normal 节点不应该只看 children 是否 traveled。它需要理解 child terminal 状态。

默认策略：

| 子节点状态 | 父节点行为 |
|---|---|
| Completed | 可继续下一个 sibling |
| Failed | 暂停下游 sibling，重新激活父节点 |
| Blocked | 重新激活父节点，允许请求人类输入或降级目标 |
| WaitingHuman | 父节点不推进，cmd 持久化后退出 |
| Aborted | 父节点终止或请求确认 |

父节点重新激活时，prompt 加入：

```text
## Failed Dependencies
- child_id: core_types
- status: Failed
- failure_signature: go/build:pkg/core:ErrDimensionMismatch redeclared
- evidence: art_23

You must decide whether to repair, replan, continue despite failure, or ask for human input.
```

---

## 8. 与 AST 架构的适配

### 8.1 AST 仍是权威源

新增 loop state 可以挂在节点上，也可以单独存在 runtime state 中。原则：

- 影响恢复执行的状态必须可序列化。
- 影响检索和可视化但不影响控制流的状态可以进入 SQLite/event log。
- SQLite 可以 rebuild，AST JSON 不应该依赖 SQLite 才能继续执行。

建议短期挂到 `TaskNode`：

```go
LoopAttempts []agentloop.LoopAttempt `json:"loop_attempts,omitempty"`
LoopMode     string                  `json:"loop_mode,omitempty"` // normal/repair/escalation
```

长期如果 event log 成熟，可以只在 AST 保存摘要：

```go
LastFailureSignature string `json:"last_failure_signature,omitempty"`
LoopMode             string `json:"loop_mode,omitempty"`
```

### 8.2 TaskStatus 收敛

需要区分 terminal 和 successful。

```go
const (
    Pending TaskStatus = iota
    Running
    Completed
    Failed
    Blocked
    Aborted
    WaitingHuman
)

func (t *TaskNode) IsTerminal() bool
func (t *TaskNode) IsSuccessful() bool
```

短期保留 `WetherTraveled`，但 `WetherFinished` / `SingleFinished` 不再作为新逻辑的权威。

### 8.3 Completion transaction

`mark_complete` 的新语义：

```text
model proposes mark_complete
  -> runtime records EventCompletionAsked
  -> runtime applies structured handoff fields
  -> runtime checks required acceptance
  -> runtime runs testable acceptance commands
  -> if pass: MarkCompleted
  -> if fail: create observation artifact and continue/repair
```

---

## 9. Action 执行边界

有两种实现路线。

### 路线 A：worker 只负责 LLM，runtime 执行 action

```text
worker:
  build prompt
  call model
  parse response
  emit proposed actions

runtime:
  validate authority
  execute action
  persist observation
  decide next loop state
```

优点：

- 最保守，AST mutation 更安全。
- 复用现有 `ExecuteAction`。
- 更容易保证 sandbox 和 memory sync。

缺点：

- 一次 attempt 内 action 执行和模型反馈需要回到 runtime。
- 如果未来要让 worker 管理多轮，需要更多 channel 协议。

### 路线 B：worker 调用 runtime 提供的 ActionExecutor 接口

```go
type ActionExecutor interface {
    Execute(ctx context.Context, nodeID string, action llm.Action) (ActionObservation, error)
}
```

优点：

- worker 可以完成一整个 leaf attempt。
- channel 协议更少。

缺点：

- 必须非常小心，避免 executor 泄漏可变 AST 指针。

建议 P0 采用路线 A。等 event/apply 边界稳定后，再评估路线 B。

---

## 10. 并发模型

第一版不要追求多节点并发。只把 leaf loop goroutine 化。

```text
single runtime controller
single active AST cursor
one worker goroutine per active leaf attempt
bounded timeout
event channel drained by controller
```

这样可以避免 sibling 并发修改 AST、artifact、memory 造成复杂一致性问题。

后续再考虑：

- sibling leaf 并发。
- background retrieval / embedding 并发。
- tool execution worker pool。
- cancellable human wait。

P0 只要求：

- goroutine 可取消。
- worker timeout 后 runtime 收到 `EventWorkerFailed`。
- worker panic recover 后变成 observation/event。
- controller 不因 worker 泄漏而卡死。

---

## 11. 持久化与事件

新增 runtime event stream，先写 SQLite，后续接 visualizer。

```sql
CREATE TABLE runtime_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id TEXT,
    node_id TEXT,
    attempt_id TEXT,
    event_type TEXT NOT NULL,
    payload_json TEXT,
    created_at TEXT
);

CREATE TABLE loop_attempts (
    id TEXT PRIMARY KEY,
    node_id TEXT NOT NULL,
    iteration INTEGER NOT NULL,
    outcome TEXT NOT NULL,
    failure_signature TEXT,
    progress_json TEXT,
    observation_refs_json TEXT,
    started_at TEXT,
    ended_at TEXT
);

CREATE TABLE action_traces (
    id TEXT PRIMARY KEY,
    attempt_id TEXT NOT NULL,
    node_id TEXT NOT NULL,
    action_type TEXT NOT NULL,
    args_hash TEXT,
    status TEXT NOT NULL,
    artifact_ref TEXT,
    error_summary TEXT,
    started_at TEXT,
    ended_at TEXT
);
```

注意：

- SQLite event log 是观测和索引，不是 AST 权威源。
- 每次状态 mutation 后仍然要保证 `--save` JSON 能恢复。
- `--load` 后如果 SQLite 缺失，可以从 AST + artifact metadata rebuild 基础索引；attempt 详细历史可降级缺失。

---

## 12. Failure Signature 与 Repair Mode

runtime 要把重复失败变成强约束，而不是只在 prompt 里提醒。

### 12.1 FailureSignature

第一版支持 Go 工程常见错误：

```text
go/build:<package>:<symbol> redeclared
go/test:<test_name>:<assertion summary>
go/mod:<missing module>
shell:not_found:<command>
shell:permission_denied:<path class>
parser:<action_type>:<field>
sandbox:<operation>:<path class>
```

归一化要去掉不稳定行号、临时路径、绝对路径前缀。

### 12.2 RepairMode 触发

触发条件：

- 同一 failure signature 连续出现 2 次。
- 同一 action fingerprint 连续出现 3 次且没有新 artifact / file delta / acceptance delta。
- required acceptance 连续失败。

RepairMode prompt 由 runtime 注入：

```text
## Repair Mode
The runtime detected repeated failure:
go/build:pkg/core:ErrDimensionMismatch redeclared

You must address this failure before unrelated work.
Allowed next actions: read_file, write_file, append_to_file, execute_command, request_human_input.
Do not mark_complete until acceptance checks pass.
```

---

## 13. 编码阶段

### Phase 0：类型和测试支架

目标：不改变行为，只建立新边界。

改动：

- 新增 `pkg/agentloop` 包。
- 定义 `AgentJob`、`AgentEvent`、`ActionObservation`、`LoopAttempt`。
- 增加 action fingerprint / failure signature helper。
- 增加最小单测。

验收：

- `go test ./pkg/agentloop ./pkg/runtime ./pkg/llm` 通过。
- 不改变现有 `Execute` 行为。

### Phase 1：command observation 化

目标：失败命令也变成 artifact 和 observation。

改动：

- 调整 `actionExecuteCommand`。
- exit code 非 0 不直接作为 action infrastructure error。
- 写入 artifact、变量、memory index。
- 生成 failure signature。

验收：

- `execute_command("go test ./badpkg")` exit 1 仍产生 artifact。
- 下一轮 prompt 能看到 artifact ref、exit code、stderr summary。

### Phase 2：runtime controller 接入 worker

目标：leaf attempt 通过 goroutine runner 执行，但 cursor 行为保持一致。

改动：

- 新增 `AgentRunner` 接口。
- runtime scheduler 从 current node 构造 `AgentJob`。
- worker 返回 parsed response / proposed actions。
- runtime 仍执行 action 和 apply event。

验收：

- 现有 runtime 测试通过。
- leaf 多轮行为与旧实现兼容。
- worker timeout / panic 能转成 node failure 或 retry observation。

### Phase 3：LoopAttempt 与 RepairMode

目标：从次数驱动变成状态驱动。

改动：

- 每轮记录 `LoopAttempt`。
- 计算 progress signals。
- repeated failure/action 进入 RepairMode。
- prompt 注入 loop state。

验收：

- 同一 Go 编译错误连续两次触发 RepairMode。
- 重复只读同一文件不算 progress。
- 写文件 hash 变化算 progress。

### Phase 4：TaskStatus 收敛

目标：解决 finished/successful/terminal 混淆。

改动：

- 新增 `Blocked`、`Aborted`。
- 新增 `IsTerminal()`、`IsSuccessful()`。
- 新增 `MarkCompleted()`、`MarkFailed()`、`MarkBlocked()`。
- runtime 上移逻辑用 `IsTerminal()`。
- 父节点完成判断用 `IsSuccessful()`。

验收：

- Failed leaf 不会被写成 Completed。
- JSON 与 SQLite 状态一致。
- Failed child 不会默认放行下游 sibling。

### Phase 5：父节点失败依赖感知

目标：AST 调度理解失败依赖。

改动：

- Normal parent 发现 child Failed/Blocked 后重新激活。
- prompt 注入 failed dependency context。
- parent 可 append repair sibling、request human input、或显式继续。

验收：

- sibling A failed 后 sibling B 默认不启动。
- parent 能看到 failed child 的 signature 和 evidence artifact。

### Phase 6：event log 与 visualizer

目标：可观察性上线。

改动：

- SQLite event tables。
- runtime event sink。
- visualizer 展示 node attempts、action traces、failure signatures。

验收：

- visualizer 能按 node 展示 attempt timeline。
- `--load state.json` 后不会依赖旧 goroutine 状态。

---

## 14. P0 最小实现范围

如果我们马上开始写代码，建议只做 P0：

1. 新建 `pkg/agentloop` 类型包。
2. 把 `execute_command` 改成 observation 语义。
3. 增加 failure signature helper。
4. Failed 状态设置后统一 sync 到 memory。
5. Leaf loop 引入 `IsTerminal()`，不再用 `MarkFinished()` 处理 failed。
6. 添加 RepairMode 的最小 prompt 注入。

暂不做：

- sibling 并发。
- worker pool。
- 多 agent 对话。
- Temporal 外部服务。
- 完整 visualizer UI。
- 删除旧字段。

---

## 15. 主要风险

### 风险 A：goroutine 引入竞态

控制：

- P0 保持单 active worker。
- worker 不拿 AST 指针。
- runtime controller 单线程 apply event。
- 单测跑 `go test -race ./pkg/runtime ./pkg/agentloop`。

### 风险 B：状态源混乱

控制：

- AST JSON 是恢复权威。
- SQLite/event log 是索引和观察。
- completion/failure 只通过 runtime transition helper。

### 风险 C：重构范围过大

控制：

- Phase 0/1 不改 cursor。
- Phase 2 只替换 leaf attempt 调用边界。
- 父节点调度放到 Phase 5。

### 风险 D：prompt 继续膨胀

控制：

- loop state 只注入摘要。
- artifact 大内容仍走 artifact slice/read。
- failure signature 和 progress signals 取代长日志。

---

## 16. 第一批代码任务拆分

建议按以下 PR / commit 切：

1. `feat: add agentloop event types`
   - 新包类型、fingerprint、signature helper、单测。

2. `fix: persist nonzero command observations`
   - exit 1 写 artifact/history，runtime error 才返回 error。

3. `fix: separate terminal and successful node states`
   - `IsTerminal` / `IsSuccessful` / failed leaf 不转 completed。

4. `feat: run leaf attempts through agent runner`
   - worker goroutine 接入，但 runtime 仍 apply action。

5. `feat: enter repair mode on repeated failures`
   - loop attempt、progress、repair prompt。

---

## 17. 参考链接

- LangGraph agent orchestration and human-in-the-loop: https://www.langchain.com/agents
- Microsoft Agent Framework overview: https://learn.microsoft.com/en-us/agent-framework/overview/
- Microsoft Agent Framework group chat orchestration: https://learn.microsoft.com/en-us/agent-framework/workflows/orchestrations/group-chat
- AutoGen multi-agent conversation framework: https://microsoft.github.io/autogen/docs/Use-Cases/agent_chat/
- Temporal durable execution docs: https://docs.temporal.io/
- Temporal durable execution overview: https://temporal.io/
- Pi agents package: https://pi.dev/packages/pi-agents
- OpenClaw agent loop concept: https://molty.finna.ai/docs/concepts/agent-loop

---

## 18. 一句话原则

LLMVM 的下一代 agentic loop 应该是一个可取消、可观察、可恢复的 Go goroutine worker；它负责提出行动和产生观察，但 AST runtime 仍然负责控制流、状态落盘、验收、依赖调度和最终事实。
