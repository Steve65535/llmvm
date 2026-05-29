# LLMVM Agentic Loop 深度改进方案

> 时间：2026-05-29
> 背景：`state1.json` 这次真实运行暴露出 Leaf agentic loop 能多轮执行，但无法可靠从编译错误中收敛。
> 参考：Pi `pi-agents` / OpenClaw embedded Pi agent loop 的公开设计，但本文按 LLMVM 的 AST runtime、artifact store、SQLite memory 和 acceptance 协议重新落地。

---

## 1. 这次运行暴露的问题

用户任务：

```bash
go run ./cmd --save state1.json "根据严格的软件工程规范，写一个用go写的 vector db for agents"
```

执行结果摘要：

- `project_structure` 完成。
- `core_types` 失败，`IterationCount=20`，`Result="Error: Maximum Agentic Loop retries reached"`。
- `vector_index` 失败，`IterationCount=20`，同样耗尽 agentic loop。
- `collection_store` 已开始执行 3 步，但仍是 Pending。
- `rest_api`、`agent_interface`、`integration_test` 未开始。
- 关键编译错误是 `ErrDimensionMismatch` 在 `pkg/core/types.go` 与 `pkg/core/distance.go` 重复定义。
- JSON 状态与 SQLite 状态不一致：JSON 中两个节点 Failed，SQLite `nodes` 仍显示 Pending。

这说明当前 loop 具备"继续尝试"能力，但缺少"工程化收敛"能力。它会把失败暴露给下一轮模型，但 runtime 不会把同一失败模式升级成强约束，也不会强制执行修复-验证闭环。

---

## 2. Pi Agent 可借鉴的点

Pi `pi-agents` 的公开包说明把能力拆成三个概念：

- **Agent**：一个 markdown 定义的隔离子进程，包含 frontmatter 和 system prompt。
- **Workflow**：JSON 树，节点类型包括 `spawn`、`sequence`、`fork`、`join`、`loop`。
- **Run / Flow**：每次委派和 workflow 执行都会持久化，支持 reload 后检查。

其中 workflow loop 明确有：

- `maxIterations`
- `continueWhen`
- 可嵌套的 body，例如 reviewer -> engineer 的 review loop
- runtime 负责 normalization、validation、budget，而不是只靠 prompt 约束

OpenClaw embedded Pi agent loop 的公开文档还强调：

- intake -> context assembly -> model inference -> tool execution -> persistence 是权威执行路径
- per-session 串行队列防止 session/tool race
- lifecycle / assistant / tool 事件流
- timeout / abort
- tool result persistence hook
- compaction retry 时重置 in-memory buffers，避免重复输出

LLMVM 不应照搬 Pi 的多 agent subprocess 设计。LLMVM 的优势是 AST task tree 是控制流权威源。但 Pi 的几个工程原则值得吸收：

1. loop 必须是 runtime 管理的协议，不只是 prompt 里写"不要重复"。
2. loop 的继续条件必须结构化，不能只靠模型主观判断。
3. 每轮 tool result 必须持久化、可观察、可恢复。
4. 终止必须区分 success、failed、aborted、timeout、blocked。
5. workflow/run/flow 状态必须能被独立 inspect。

参考链接：

- Pi `pi-agents`: https://pi.dev/packages/pi-agents
- OpenClaw Agent Loop: https://molty.finna.ai/docs/concepts/agent-loop

---

## 3. 目标状态

把当前的 Leaf loop：

```text
execute action -> observe roughly -> repeat until mark_complete or MaxRetries
```

升级为工程闭环：

```text
Plan
  -> Act
  -> Persist Observation
  -> Classify Outcome
  -> Evaluate Progress
  -> Decide Continue / Repair / Escalate / Complete
  -> Verify Acceptance
  -> Handoff
```

目标不是让模型"更努力"，而是让 runtime 提供以下硬能力：

- 失败输出一等化：失败命令也是 observation 和 artifact。
- 进展检测：判断是否真的改变了文件、artifact、tests、acceptance 状态。
- 失败模式聚类：同一错误重复出现时改变策略。
- 结构化 loop 状态：保存每轮 attempt、action fingerprint、observation、outcome。
- 明确终止语义：Completed、Failed、Blocked、Aborted、WaitingHuman 分开。
- 父节点调度感知：下游节点不能无视上游必需依赖失败。
- 可视化可观察：每一轮 loop 都能在 visualizer/TUI 中看到。

---

## 4. 当前实现评估

### 4.1 优点

- `HandleLeafAgenticLoop` 很轻，复用 cursor，易理解。
- `mark_complete` 已接 acceptance，required criteria 未满足会被拒。
- `testable` acceptance 会由 runtime 实际执行命令验证，不只信模型自评。
- artifact store 已记录 command/file/context/evidence。
- SQLite 已有 retrieval/rerank trace，说明可观测性方向正确。

### 4.2 缺口

#### 缺口 A：失败命令不是一等 observation

`actionExecuteCommand` 中命令非零退出会直接返回 error，不会稳定写入 `command_output_history`。模型下一轮能从 `lastErr` 看到错误，但这个错误没有变成可检索、可聚类、可被父节点消费的 artifact。

改造原则：

- shell exit != runtime action failure。
- command 执行成功且 exit code 非零，应该返回 `CommandObservation{ExitCode, Stdout, Stderr, ErrorSummary}`。
- 只有 runtime 无法执行命令，例如权限、超时、工具崩溃，才算 action failure。

#### 缺口 B：loop 只有次数上限，没有继续条件

当前 Leaf loop 的继续条件是：

- `SingleFinished` 为 false
- `IterationCount < MaxRetries`

这太粗。真实工程 loop 应该看：

- 上一轮是否产生新 artifact
- 工作目录是否发生文件变化
- 测试错误是否变化
- acceptance 是否有新增 passed
- 是否重复相同 action fingerprint
- 是否重复相同 failure signature

#### 缺口 C：Failed leaf 会被 MarkFinished

当前 `HandleLeafAgenticLoop` 对 Failed leaf 会清理 scratchpad、`MarkFinished()`、`MoveUp()`。这是为了防止死循环，但语义上会把"失败终止"和"完成"混到一起。

需要引入明确状态：

- `Completed`
- `Failed`
- `Blocked`
- `Aborted`
- `WaitingHuman`

并把 `WetherFinished` 改成更中性的 `Terminal` 或派生判断：

```go
func (n *TaskNode) IsTerminal() bool {
    return n.Status == Completed || n.Status == Failed || n.Status == Blocked || n.Status == Aborted
}
```

#### 缺口 D：父节点不理解失败依赖

`core_types` 和 `vector_index` 失败后，runtime 仍进入 `collection_store`。这可以作为探索策略，但对"写一个严格软件工程规范的 vector db"来说，下游应默认阻塞，除非父节点显式重规划。

需要把节点之间的依赖显式化：

```json
{
  "id": "vector_index",
  "depends_on": ["core_types"],
  "on_dependency_failed": "block|replan|continue"
}
```

短期可以不用改 schema，先在 Normal parent 的调度策略中加默认规则：

- 如果前序 sibling Failed 且当前 sibling 信息中没有声明可独立执行，则当前 sibling 进入 Blocked。
- 激活父节点，让父节点选择修复失败 sibling、重规划、或显式继续。

#### 缺口 E：SQLite 同步不完整

失败状态没有同步到 SQLite。原因是 `syncNodeToMemory` 只在部分 mutation 路径调用，Failed 状态设置后没有统一事件。

应该引入统一状态变更入口：

```go
func (r *Runtime) transitionNode(node *TaskNode, status TaskStatus, reason string)
```

所有 Completed / Failed / Blocked / WaitingHuman 都通过它：

- 更新 AST
- 写 node result / failure reason
- 写 SQLite nodes
- 写 event log
- 触发 autosave

---

## 5. 新 Agentic Loop 协议

### 5.1 LoopAttempt

新增结构，持久化到 TaskNode 或单独 runtime state：

```go
type LoopAttempt struct {
    Iteration          int
    StartedAt          int64
    EndedAt            int64
    Actions            []ActionTrace
    ObservationRefs    []string
    ActionFingerprint  string
    FailureSignature   string
    ProgressSignals    ProgressSignals
    Outcome            AttemptOutcome
}

type AttemptOutcome string

const (
    AttemptProgressed AttemptOutcome = "progressed"
    AttemptNoProgress AttemptOutcome = "no_progress"
    AttemptFailed     AttemptOutcome = "failed"
    AttemptBlocked    AttemptOutcome = "blocked"
    AttemptCompleted  AttemptOutcome = "completed"
)
```

### 5.2 ActionTrace

每个 action 都应产生 trace，不论成功失败：

```go
type ActionTrace struct {
    ActionType   string
    ArgsHash     string
    StartedAt    int64
    EndedAt      int64
    Status       string // ok, nonzero_exit, runtime_error, rejected
    ArtifactRef  string
    ErrorSummary string
}
```

### 5.3 ProgressSignals

runtime 不应让模型自己判断是否有进展。它至少可以确定性计算：

```go
type ProgressSignals struct {
    NewArtifacts          int
    ModifiedFiles         []string
    NewAcceptanceResults  int
    PassedAcceptanceDelta int
    FailedAcceptanceDelta int
    LastCommandExitCode   *int
    RepeatedAction        bool
    RepeatedFailure       bool
}
```

### 5.4 FailureSignature

对失败输出做轻量归一化：

- Go 编译错误：包名 + 文件行号去行号归一 + 错误主语
- test failure：测试名 + assertion summary
- command not found：命令名
- permission denied：路径类别
- parser error：action type + field

示例：

```text
go/build:pkg/core:ErrDimensionMismatch redeclared
```

同一 `FailureSignature` 连续出现 2 次后，runtime 改 prompt 策略：

```text
## REPAIR MODE
The last two attempts failed with the same signature:
go/build:pkg/core:ErrDimensionMismatch redeclared

You must modify the source of this failure before running unrelated commands.
Allowed next actions: read_file, write_file, append_to_file, execute_command(go test/go build only), request_human_input.
Do not continue downstream work.
```

---

## 6. Loop 状态机

建议状态机：

```text
Ready
  -> RunningAttempt
  -> Observed
  -> Evaluating
  -> Continue
  -> RepairMode
  -> CompleteCandidate
  -> Completed
  -> Blocked
  -> Failed
  -> Escalated
```

关键规则：

1. `execute_command` 非零退出进入 `Observed`，不是直接 action error。
2. `mark_complete` 进入 `CompleteCandidate`，然后 runtime 跑 acceptance。
3. acceptance 失败回到 `RepairMode`，并附带失败 artifact。
4. 同一 failure signature 连续 N 次进入 `RepairMode` 强约束。
5. `RepairMode` 仍无进展超过 M 次，进入 `Escalated`。
6. `Escalated` 要求生成结构化 failure handoff。
7. 父节点看到 child `Escalated/Failed/Blocked` 后必须重规划或显式放弃。

---

## 7. Action 执行语义调整

### 7.1 execute_command

当前：

```text
exit 0 -> artifact + history
exit != 0 -> error -> retry
```

目标：

```text
shell launched -> always artifact + history
exit 0 -> observation ok
exit != 0 -> observation nonzero_exit + failure_signature
launch failed / timeout -> runtime_error
```

建议 artifact summary：

```text
$ go test ./pkg/core -count=1 -> exit=1, stderr contains "ErrDimensionMismatch redeclared"
```

并在变量中保留：

```json
{
  "last_command": "art_23",
  "last_command_exit_code": 1,
  "last_failure_signature": "go/build:pkg/core:ErrDimensionMismatch redeclared"
}
```

### 7.2 write_file / append_to_file

写文件后记录 file delta：

- path
- bytes_before / bytes_after
- hash_before / hash_after
- action id

这让 progress detection 不依赖模型自述。

### 7.3 mark_complete

`mark_complete` 不应只是模型动作，而是一个 runtime transaction：

```text
model proposes completion
runtime checks required acceptance
runtime runs testable criteria
runtime records acceptance evidence
runtime marks Completed only if required criteria pass
```

如果 required criteria 失败：

- 不消耗普通 retry
- 进入 RepairMode
- 把 acceptance failure 作为第一等 artifact

---

## 8. 父节点调度策略

Normal 节点不应只看 `AllChildrenTraveled()` 和 `AllChildrenFinished()`。它需要区分：

| 子节点状态 | 父节点默认行为 |
|---|---|
| Completed | 继续下一个 sibling |
| Failed | 暂停 sibling 推进，激活父节点重规划 |
| Blocked | 激活父节点决定是否请求人类输入或降级目标 |
| Escalated | 父节点必须消费 escalation handoff |
| Aborted | 父节点终止或请求人类确认 |

父节点可选动作：

- `retry_child`
- `replace_child`
- `append_repair_sibling`
- `continue_despite_failure`
- `request_human_input`
- `mark_failed`

这部分可以先不新增 action，第一版用现有 `append_sibling_node` + `create_node` + `request_human_input` 表达。但 runtime prompt 必须明确告诉父节点："你正在处理失败依赖，不是继续常规分解。"

---

## 9. 持久化与可观测性

Pi / OpenClaw 的重要启发是 flow/run/event 都可 inspect。LLMVM 应该有自己的 runtime event stream，不再依赖 stdout。

新增 SQLite 表：

```sql
CREATE TABLE loop_attempts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id TEXT NOT NULL,
    iteration INTEGER NOT NULL,
    outcome TEXT NOT NULL,
    action_fingerprint TEXT,
    failure_signature TEXT,
    progress_json TEXT,
    observation_refs_json TEXT,
    started_at TEXT,
    ended_at TEXT
);

CREATE TABLE action_traces (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    attempt_id INTEGER NOT NULL,
    node_id TEXT NOT NULL,
    action_type TEXT NOT NULL,
    args_hash TEXT,
    status TEXT NOT NULL,
    artifact_ref TEXT,
    error_summary TEXT,
    started_at TEXT,
    ended_at TEXT
);

CREATE TABLE runtime_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id TEXT,
    node_id TEXT,
    event_type TEXT NOT NULL,
    payload_json TEXT,
    created_at TEXT
);
```

Visualizer / TUI 应展示：

- 当前节点 loop iteration
- 最近 10 次 attempts
- repeated action / repeated failure 标记
- acceptance check 结果
- failure handoff
- SQLite 与 AST 状态是否一致

---

## 10. 和现有文件的落点

### Phase 1：修复可恢复性基础

涉及文件：

- `pkg/runtime/dispatch.go`
- `pkg/runtime/shell.go`
- `pkg/runtime/agentic_loop.go`
- `pkg/runtime/index.go`
- `pkg/tasknode/tasknode.go`
- `pkg/memory/memory.go`

改动：

1. `execute_command` 非零退出也写 artifact/history。
2. 新增 command observation 结构。
3. 节点 Failed 时立即 `syncNodeToMemory`。
4. Failed leaf 不再 `MarkFinished()`，改用 terminal 状态判断。
5. 添加 `FailureSignature` 归一化 helper。

测试：

- 命令 exit 1 仍产生 artifact。
- Go 编译错误能生成稳定 failure signature。
- Failed 节点 JSON 与 SQLite 状态一致。
- Failed child 不被当作 Completed。

### Phase 2：Progress Detector

涉及文件：

- `pkg/runtime/agentic_loop.go`
- `pkg/runtime/execute.go`
- `pkg/runtime/dispatch.go`
- `pkg/artifact/`
- `pkg/memory/`

改动：

1. 增加 `LoopAttempt`。
2. 每轮记录 artifact count 前后差异。
3. 写文件工具记录 hash delta。
4. action fingerprint 取 action type + normalized args。
5. repeated action / repeated failure 触发 RepairMode prompt。

测试：

- 同一 failing command 连续两次后进入 RepairMode。
- 写文件 hash 变化计为 progress。
- 只读同一 artifact 不计为 progress。

### Phase 3：Escalation Handoff

涉及文件：

- `pkg/llm/parser.go`
- `pkg/llm/api.go`
- `pkg/runtime/execute.go`
- `pkg/runtime/dispatch.go`
- `pkg/tasknode/tasknode.go`
- `pkg/memory/memory.go`

新增 action：

```json
{
  "action_type": "escalation_handoff",
  "failure_mode": "go build failure",
  "attempts_summary": "Tried building pkg/core and pkg/index; duplicate ErrDimensionMismatch remains.",
  "blocking_dependencies": ["core_types"],
  "suggested_next_steps": ["Remove duplicate declaration from distance.go", "Re-run go test ./pkg/core"]
}
```

规则：

- 只有 runtime 进入 EscalationRequired prompt 时允许该 action。
- 该 action 不代表成功，只代表失败状态结构化完成。
- 父节点下次激活必须优先看到 escalation handoff。

### Phase 4：父节点依赖感知调度

涉及文件：

- `pkg/runtime/execute.go`
- `pkg/runtime/activation.go`
- `pkg/runtime/prompt.go`
- `pkg/tasknode/tasknode.go`

改动：

1. Normal 节点发现 child Failed/Escalated 时，不直接推进后续 sibling。
2. 重新激活 parent，让它重规划。
3. prompt 中加入 `## Failed Dependencies`。
4. parent 可以 append repair sibling 或 request human input。

测试：

- sibling A failed 后 sibling B 不自动开始。
- parent 收到 failed dependency context。
- parent append repair sibling 后 cursor 进入 repair sibling。

### Phase 5：Runtime Event Stream

涉及文件：

- `pkg/runtime/events.go`
- `visualizer/server.go`
- `visualizer/frontend/src/`
- `cmd/`

改动：

1. Runtime 增加 event sink。
2. 所有 node transition、action start/end、attempt outcome、acceptance check 写 event。
3. visualizer 展示 per-node attempt timeline。

---

## 11. Prompt 改造

不要继续只加"不要重复"这类软提示。prompt 应该由 runtime 状态驱动。

### 普通 Loop Prompt

```text
## Loop State
- Iteration: 7/20
- Last outcome: nonzero_exit
- Last command artifact: art_23
- Last failure signature: go/build:pkg/core:ErrDimensionMismatch redeclared
- Progress since previous attempt: no source files changed, no acceptance passed
```

### RepairMode Prompt

```text
## Repair Mode
The runtime detected repeated failure:
go/build:pkg/core:ErrDimensionMismatch redeclared

You must directly address this failure before unrelated work.
Required next step:
1. inspect the files named in the error
2. modify the duplicated declaration
3. rerun the failing build/test command

Do not continue downstream modules.
Do not mark_complete until acceptance checks pass.
```

### Escalation Prompt

```text
## Escalation Required
The node exhausted its repair budget.
You must output only escalation_handoff.
Summarize:
- failure mode
- attempts made
- evidence artifact refs
- blocking dependencies
- suggested next steps
```

---

## 12. 验收标准

### P0 验收

- `execute_command` exit 1 会保存 artifact，且下一轮 prompt 能看到 exit code 和 stderr 摘要。
- Failed node 状态在 JSON 和 SQLite 中一致。
- Failed leaf 不会被当成 Completed。
- 相同 Go 编译错误重复两次后，runtime 进入 RepairMode。

### P1 验收

- acceptance failure 自动生成 evidence artifact。
- 同一 action fingerprint 重复 N 次后触发 no-progress 警告。
- 父节点能收到 child failure handoff。
- 下游 sibling 默认不会在 required dependency Failed 后继续执行。

### P2 验收

- visualizer 能展示 node attempt timeline。
- `--load state.json` 后 loop attempts、action traces、failure handoffs 可恢复。
- 支持手动 `--resume` 处理 Blocked / WaitingHuman。

---

## 13. 推荐实施顺序

1. **先改 `execute_command` observation 语义**。这是最高收益，因为当前失败输出没有成为稳定上下文。
2. **修 JSON/SQLite 状态同步**。否则 memory 查询会误导模型。
3. **引入 failure signature + RepairMode**。这直接解决本次 `ErrDimensionMismatch` 卡死问题。
4. **修 Failed leaf terminal 语义**。避免失败被当作 finished。
5. **父节点依赖感知调度**。防止在基础模块失败时继续做下游模块。
6. **loop_attempts/action_traces 表**。把可观测性从日志升级成数据。
7. **visualizer 接入**。最后做 UI，因为前面数据结构稳定后 UI 才有意义。

---

## 14. 设计边界

不建议做：

- 不建议把 Pi 的 `spawn` subprocess agent 机制直接搬进 LLMVM。LLMVM 的核心是 AST + Runtime，不是多进程 delegation。
- 不建议恢复旧的 Loop 节点类型。loop 应是 Leaf 执行协议，不是任务树结构。
- 不建议用更长 prompt 代替 runtime policy。重复失败、依赖失败、验收失败都应该由 runtime 判定。
- 不建议把所有失败交给 human-in-the-loop。只有 Blocked / unsafe / ambiguous 状态需要请求人类。

应该坚持：

- AST 是控制流权威源。
- SQLite 是可重建索引和观测表，不是权威源。
- Artifact 是工具结果和证据的默认归宿。
- Acceptance 是完成协议。
- Loop 是 runtime 管理的执行协议。

---

## 15. 一句话总结

当前 LLMVM 的 agentic loop 已经能让 Leaf 节点多轮行动，但它还是"次数驱动的 ReAct 循环"。参考 Pi agent 后，下一步应把它升级成"状态驱动的软件工程闭环"：每轮行动都有 observation，每个失败都有 signature，每次重复失败都会改变策略，每个完成都必须被 acceptance 验证，每个终止状态都能被父节点和 visualizer 正确理解。

---

## 16. TaskNode 状态字段收敛

当前 `TaskNode` 同时有以下字段参与生命周期判断：

```go
Status         TaskStatus // Pending / Running / Completed / Failed / WaitingHuman
WetherTraveled bool       // 是否已遍历过
WetherFinished bool       // 是否已完成
SingleFinished bool       // Agentic Loop: LLM explicitly called mark_complete
```

这几个字段已经出现职责重叠：

- `Status == Completed` 和 `WetherFinished == true` 都在表达"完成"。
- `SingleFinished == true` 表达模型调用过 `mark_complete`，但也常被当成 handoff/完成信号。
- `WetherFinished` 同时被 cursor、prompt、handoff、memory rebuild 使用，导致失败终止和成功完成容易混淆。
- `WetherTraveled` 是 cursor 遍历状态，不应和业务完成状态混在一起讨论。

这次 `state1.json` 暴露的关键问题是：Failed leaf 为了防止 cursor 卡住，会被 `MarkFinished()`，而 `MarkFinished()` 又会设置 `Status = Completed`。这让"失败终止"被写成"成功完成"，会误导父节点、SQLite index、visualizer 和后续 context pack。

### 16.1 目标语义

生命周期状态应该由 `Status` 统一表达。

建议扩展：

```go
type TaskStatus int

const (
    Pending TaskStatus = iota
    Running
    Completed
    Failed
    Blocked
    Aborted
    WaitingHuman
)
```

并增加派生判断：

```go
func (t *TaskNode) IsTerminal() bool {
    return t.Status == Completed ||
        t.Status == Failed ||
        t.Status == Blocked ||
        t.Status == Aborted
}

func (t *TaskNode) IsSuccessful() bool {
    return t.Status == Completed
}
```

核心区别：

- **Terminal**：节点不会继续执行了，可能成功、失败、阻塞或被中止。
- **Successful**：节点真的完成了任务，满足完成协议。

父节点调度、cursor 回弹、visualizer 状态展示应基于 `IsTerminal()`。

handoff 传播、acceptance 完成、下游依赖默认可继续应基于 `IsSuccessful()`。

### 16.2 字段去留建议

| 字段 | 建议 | 理由 |
|---|---|---|
| `Status` | 保留并升格为唯一生命周期源 | 已经表达 Pending/Running/Completed/Failed/WaitingHuman，应继续扩展 |
| `WetherFinished` | 逐步删除 | 与 `Status == Completed` 重复，并混淆 failed terminal 与 successful completion |
| `SingleFinished` | 重命名或删除 | 它是 `mark_complete` 请求事实，不应作为完成状态；可改为 `CompletionRequested` 或只作为 action trace |
| `WetherTraveled` | 暂时保留但改名 | 它是 cursor 状态，不是生命周期状态；建议改成 `CursorVisited` 或 `Visited` |

`WetherFinished` 的拼写本身也有问题，但这不是主要原因。主要原因是语义错误：finished 到底是 terminal 还是 successful，目前不清楚。

### 16.3 API 替换

当前：

```go
func (t *TaskNode) MarkFinished() {
    t.WetherFinished = true
    t.Status = Completed
}

func (t *TaskNode) AllChildrenFinished() bool
```

建议替换为：

```go
func (t *TaskNode) MarkCompleted(summary string)
func (t *TaskNode) MarkFailed(reason string)
func (t *TaskNode) MarkBlocked(reason string)
func (t *TaskNode) MarkAborted(reason string)

func (t *TaskNode) AllChildrenTerminal() bool
func (t *TaskNode) AllChildrenSuccessful() bool
func (t *TaskNode) HasFailedChildren() bool
```

`MarkCompleted` 只在 runtime acceptance 通过后调用。

`MarkFailed` 只表示节点失败终止，不得设置任何 successful/handoff-complete 标志。

`AllChildrenTerminal` 用于 cursor 判断"是否还需要继续执行子节点"。

`AllChildrenSuccessful` 用于父节点判断"任务目标是否整体完成"。

### 16.4 SingleFinished 的替代

`SingleFinished` 当前用于表示模型已经调用 `mark_complete`，然后 `HandleLeafAgenticLoop` 下一轮把 leaf `MarkFinished()`。

这可以改成更直接的 runtime transaction：

```text
model emits mark_complete
  -> runtime validates required acceptance
  -> runtime runs testable checks
  -> if pass: MarkCompleted
  -> if fail: enter RepairMode
```

也就是说，`mark_complete` 成功时应当立即转成 `Status=Completed`，不需要额外持久化 `SingleFinished`。

如果为了兼容旧状态需要保留字段，建议：

- 重命名为 `CompletionRequested`。
- 只用于旧 JSON load 的迁移。
- 新写入状态不再依赖它。

### 16.5 WetherTraveled 的边界

`WetherTraveled` 不建议和 `WetherFinished` 一起删除。

它虽然名字不好，但表达的是 cursor 是否访问过该节点。当前 agentic loop 通过把 `WetherTraveled=false` 留在同一 Leaf 上继续下一轮，这是一种轻量实现。

短期建议：

- 保留行为。
- 改名为 `CursorVisited`。
- 在 prompt 中不要把它解释成业务进展，只解释成 traversal metadata。

长期如果引入显式 loop attempt 状态，可以再把 cursor 的"stay"能力做成一等操作：

```go
cursor.Stay()
```

但这不是 P0。P0 是先消除 finished/successful 混淆。

### 16.6 迁移顺序

第一阶段：新增 helper，不破坏旧 JSON。

- 新增 `Blocked`、`Aborted`。
- 新增 `IsTerminal()`、`IsSuccessful()`。
- 新增 `MarkCompleted()`、`MarkFailed()`、`MarkBlocked()`。
- `MarkFinished()` 标记 deprecated，内部先调用 `MarkCompleted()`。

第二阶段：修 runtime 调度。

- `HandleLeafAgenticLoop` 中 Failed leaf 不再调用 `MarkFinished()`。
- `decideNextStep` 使用 `IsTerminal()` 判断是否上移。
- Normal parent 使用 `AllChildrenTerminal()` 判断是否还有子节点要执行。
- Parent completion 使用 `AllChildrenSuccessful()`，失败子节点触发重规划/blocked，而不是直接完成。

第三阶段：修 prompt / memory / visualizer。

- prompt 中删除 `WetherFinished` 或改成 `Terminal/Successful`。
- SQLite rebuild 的 `HasHandoff` 改为 `IsSuccessful() || has explicit escalation handoff`。
- visualizer 不再用 `WetherFinished` 给绿色完成态，而用 `Status`。

第四阶段：兼容旧状态。

- load JSON 时如果旧状态存在 `WetherFinished=true` 且 `Status` 为空或 Pending，则迁移为 `Completed`。
- 如果 `Status=Failed` 且 `WetherFinished=true`，以 `Status=Failed` 为准，忽略旧 finished。
- 新保存状态不再写 `WetherFinished`，或保留 `omitempty` 兼容一段时间。

第五阶段：删除字段。

- 删除 `WetherFinished`。
- 删除或迁移 `SingleFinished`。
- `WetherTraveled` 只改名，不改变语义。

### 16.7 最小 P0 改动

如果只做最小修复，优先改三处：

1. `MarkFinished()` 不再用于 Failed path。
2. Failed leaf 上移时保持 `Status=Failed`，并设置 terminal 判断，而不是转 Completed。
3. SQLite sync 在 Failed 状态设置后立即执行，保证 JSON/DB 一致。

这三步能直接修复 `state1.json` 暴露的问题，并为后续完整状态机改造铺路。
