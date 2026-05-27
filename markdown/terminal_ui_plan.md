# LLMVM Terminal UI 计划

## 目标

为 LLMVM 增加一个 Terminal UI，使它从“命令行执行器”升级为“可观察、可介入、可恢复的 agent runtime 驾驶舱”。

这个 TUI 应该借鉴 Claude Code 的终端体验：界面克制、状态明确、键盘优先、适合长任务，并且要突出 LLMVM 自身的能力：

- AST / Task Tree 驱动的执行模型
- Cursor 当前执行位置
- Human-in-the-loop
- Artifact store
- SQLite memory / FTS retrieval
- Save / load / resume
- Context budget、压缩、重试、错误恢复

第一阶段只做计划和设计，不直接进入代码实现。

## 产品定位

TUI 不是替换现有 CLI，而是新增一个更适合长任务和交互式执行的入口。

建议入口：

```bash
go run cmd/tui/main.go
```

未来可以整合为：

```bash
llmvm tui
llmvm tui --save state.json
llmvm tui --load state.json
```

TUI 应支持：

- 输入初始任务
- 启动 runtime
- 实时查看当前执行节点
- 查看 AST 执行树
- 查看 artifact 摘要和内容切片
- 查看 memory 查询结果
- 处理 human-in-the-loop 请求
- 保存 / 加载 / 恢复状态
- 查看错误、retry、context compression 等运行时状态

## 技术栈

项目主体是 Go，runtime 也是 Go，因此 TUI 应优先使用 Go 原生技术栈。

推荐：

- `bubbletea`：TUI 主事件循环、状态更新、键盘交互
- `lipgloss`：样式、颜色、边框、状态标签
- `bubbles`：输入框、textarea、viewport、list、spinner 等组件
- `glamour`：Markdown 渲染，用于 handoff、LLM 输出、artifact 预览

不建议第一版使用 React / Node TUI，因为会引入额外进程边界，反而不利于直接接入当前 Go runtime 的 `HumanInputFunc`、AST root、artifact store 和 save/load 状态。

## 环境变量规划

真实密钥放在本地 `.env`，不要提交到仓库。仓库中只提交 `.env.example` 作为配置模板。

当前已有 / 应保留：

- `DEEPSEEK_API_KEY`：DeepSeek API key。为空时 runtime 走 stub engine 或初始化失败路径。
- `CONTEXT_BUDGET`：runtime prompt/context token budget，默认 `64000`。

TUI 计划新增：

- `LLMVM_SAVE_PATH`：默认保存状态文件路径，例如 `state.json`。
- `LLMVM_LOAD_PATH`：默认加载状态文件路径，空值表示新任务。
- `LLMVM_SQLITE_PATH`：显式指定 SQLite memory 文件路径，空值时从 save/load path 派生。
- `LLMVM_TUI_AUTOSAVE`：是否启用 TUI 自动保存，默认 `true`。
- `LLMVM_TUI_AUTOSAVE_INTERVAL_SECONDS`：TUI 自动保存间隔，默认 `30`。
- `LLMVM_TUI_EVENT_BUFFER_SIZE`：TUI event log 内存保留条数，默认 `1000`。
- `LLMVM_TUI_LOG_LEVEL`：TUI 日志级别，默认 `info`。
- `LLMVM_TUI_RAW_OUTPUT`：是否默认展示 raw LLM/tool 输出，默认 `false`。

## Claude Code 体验借鉴

这里借鉴的是体验原则，不是照搬界面。

### 1. 状态清晰

用户应该一眼看出：

- 当前任务是什么
- runtime 是否正在执行
- 当前 cursor 在哪个 node
- 当前是否等待 human input
- 最近一次 LLM / tool action 是什么
- 是否发生 retry、压缩、错误恢复

### 2. 输出克制

默认展示高价值摘要，而不是刷满原始日志。

默认显示：

- step
- node
- action
- artifact summary
- error summary
- human input request

按需展开：

- raw LLM response
- full prompt
- full artifact content
- full event log

### 3. Human-in-the-loop 一等公民

当模型触发 `request_human_input` 时，TUI 应切换到明确的输入 / 审批状态，展示：

- 问题
- 上下文
- 可选项
- 是否 blocking
- 自定义回复输入框
- note 输入框

用户不应该退出 TUI 后再执行 `--resume <node-id>`。

### 4. 长任务可追踪

LLMVM 的任务可能运行很久。TUI 要让用户看到 runtime 的进展，而不是只有最终树。

需要实时展示：

- AST 树增长
- 当前节点变化
- leaf agentic loop 迭代次数
- LLM 调用状态
- tool 执行状态
- artifact 增加
- memory index rebuild / query
- context compression level
- stagnation detection 状态

### 5. 键盘优先

常用操作必须能通过快捷键完成：

- 切换面板
- 展开 / 折叠节点
- 搜索 node
- 打开 artifact
- 保存状态
- 暂停 / 继续
- 回答 human input
- 打开 command palette

## 推荐界面布局

### 桌面终端布局

```text
┌─────────────────────────────────────────────────────────────┐
│ LLMVM  running  task: Analyze project...  save: state.json   │
├─────────────────┬─────────────────────────┬─────────────────┤
│ AST Tree        │ Current Node            │ Artifacts       │
│                 │                         │                 │
│ root            │ node_4: Implement plan  │ art_1 read_file │
│ ├─ node_1 ✓     │ status: Running         │ art_2 search    │
│ ├─ node_2 ✓     │ type: Leaf              │ art_3 shell     │
│ └─ node_4 ●     │ confidence: medium      │                 │
│                 │                         │                 │
├─────────────────┴─────────────────────────┴─────────────────┤
│ Events / LLM / Tool Calls                                    │
│ Step 7: Calling LLM                                          │
│ Tool: search pkg/runtime                                     │
│ Compression: level 1                                         │
├─────────────────────────────────────────────────────────────┤
│ > user input / human response / command palette              │
└─────────────────────────────────────────────────────────────┘
```

### 小屏终端布局

小屏时使用 tab 模式：

- `1` Tree
- `2` Current Node
- `3` Artifacts
- `4` Events
- `5` Memory
- `6` Prompt / Raw

## 面板设计

### Tree 面板

展示 AST / Task Tree。

需要展示：

- node name
- node ID
- node type: `Normal` / `Loop` / `Leaf`
- node status: `Pending` / `Running` / `Completed` / `Failed` / `WaitingHuman`
- 当前 cursor 高亮
- loop / leaf 特殊标记
- failed / waiting human 强提示

操作：

- 展开 / 折叠
- 跳转到节点详情
- 搜索 node name / ID
- 只看 failed / waiting human 节点

### Current Node 面板

展示当前 cursor 所在节点的结构化状态。

字段：

- ID
- name
- type
- status
- traveled / finished
- iteration count / max retries
- result
- summary
- key facts
- decisions
- assumptions
- outputs
- open questions
- handoff
- artifact refs
- confidence

这个面板应该是 LLMVM 区别于普通 agent CLI 的核心视图。

### Artifact 面板

展示 artifact store。

字段：

- artifact ID
- type / source
- summary
- size
- pinned
- spilled
- evicted / tombstone
- created time

操作：

- 打开 artifact
- 按 slice 查看内容
- 搜索 artifact summary
- pin / unpin
- 复制 artifact ID 到输入区

### Events 面板

展示 runtime event stream。

事件应包括：

- execution started
- step started
- node changed
- LLM call started / finished
- response parsed
- action started / finished
- tool started / finished
- artifact created
- memory indexed
- human input requested / resolved
- state saved
- context compression changed
- stagnation warning
- runtime error

默认只显示摘要，可切换 raw mode。

### Memory 面板

展示 SQLite memory / FTS 能力。

功能：

- 查询 memory
- 查看 node handoff
- 查看 indexed variables
- 查看 artifact refs
- 查看 sibling handoffs
- 查看当前节点相关 memory

第一版可以先只做查询和结果展示，不需要复杂浏览器。

### Human Input 面板

当 runtime 请求 human input 时进入此模式。

展示：

- node ID
- question
- context
- options
- blocking
- response 输入框
- optional note 输入框

提交后生成：

```go
tasknode.HumanResponse{
    Value:     value,
    Note:      note,
    Timestamp: time.Now().Unix(),
}
```

并交给 runtime 继续执行。

## Runtime 架构改造建议

当前 runtime 主要通过 `fmt.Printf` 输出运行状态。TUI 不应该解析 stdout，而应该通过结构化事件订阅 runtime。

建议新增：

```go
type RuntimeEventType string

const (
    EventExecutionStarted     RuntimeEventType = "execution_started"
    EventStepStarted          RuntimeEventType = "step_started"
    EventNodeChanged          RuntimeEventType = "node_changed"
    EventLLMCallStarted       RuntimeEventType = "llm_call_started"
    EventLLMCallFinished      RuntimeEventType = "llm_call_finished"
    EventActionParsed         RuntimeEventType = "action_parsed"
    EventToolStarted          RuntimeEventType = "tool_started"
    EventToolFinished         RuntimeEventType = "tool_finished"
    EventArtifactCreated      RuntimeEventType = "artifact_created"
    EventMemoryIndexed        RuntimeEventType = "memory_indexed"
    EventHumanInputRequested  RuntimeEventType = "human_input_requested"
    EventHumanInputResolved   RuntimeEventType = "human_input_resolved"
    EventNodeCompleted        RuntimeEventType = "node_completed"
    EventNodeFailed           RuntimeEventType = "node_failed"
    EventStateSaved           RuntimeEventType = "state_saved"
    EventRuntimeError         RuntimeEventType = "runtime_error"
)

type RuntimeEvent struct {
    Type      RuntimeEventType `json:"type"`
    NodeID    string           `json:"node_id,omitempty"`
    Message   string           `json:"message,omitempty"`
    Payload   any              `json:"payload,omitempty"`
    Timestamp int64            `json:"timestamp"`
}
```

Runtime 增加：

```go
OnEvent func(RuntimeEvent)
```

现有 `OnStepComplete` 可以保留，用于 save/autosave。TUI 主要依赖 `OnEvent`。

这样未来 CLI、TUI、web visualizer 都能共用一套事件模型。

## Human-in-the-loop 集成方式

当前项目已经具备基础设施：

- `tasknode.HumanRequest`
- `tasknode.HumanResponse`
- `tasknode.WaitingHuman`
- `runtime.HumanInputFunc`
- `runtime.ResumeWithHumanResponse`

TUI 中应优先使用同步注入：

```go
rt.HumanInputFunc = func(req *tasknode.HumanRequest) (*tasknode.HumanResponse, error) {
    return app.RequestHumanInput(req)
}
```

流程：

1. LLM 返回 `request_human_input`
2. runtime 将 node 设置为 `WaitingHuman`
3. runtime 触发 TUI human input 模式
4. 用户在 TUI 中选择 option 或输入回复
5. TUI 返回 `HumanResponse`
6. runtime 调用内部 apply 流程恢复 node
7. execution 继续

对于从文件恢复的状态：

1. TUI `--load state.json`
2. 扫描 WaitingHuman 节点
3. 如果存在，直接进入 human input 面板
4. 用户回答后调用 `ResumeWithHumanResponse`
5. 再继续 `Execute`

## Save / Load / Resume 设计

TUI 应复用当前 `SaveState` 格式：

```go
type SaveState struct {
    Root      *tasknode.TaskNode `json:"root"`
    Artifacts *artifact.Store    `json:"artifacts,omitempty"`
}
```

需要支持：

- 启动时选择新任务或加载 state
- 运行中定时 / 每步 autosave
- Ctrl+C emergency save
- 保存路径派生 SQLite path
- load 后 rebuild SQLite index
- 检测 WaitingHuman 节点并进入恢复流程

## 快捷键建议

全局：

- `q`：退出，若正在运行则提示保存
- `ctrl+c`：保存并退出
- `s`：保存
- `p`：暂停 / 继续
- `?`：帮助
- `:`：command palette

面板：

- `tab`：切换面板
- `shift+tab`：反向切换
- `1`：Tree
- `2`：Current Node
- `3`：Artifacts
- `4`：Events
- `5`：Memory
- `enter`：打开选中项
- `esc`：返回
- `/`：搜索

Human input：

- `enter`：提交
- `tab`：切换输入项
- `esc`：取消 / 返回
- `1..9`：选择 option

## 视觉风格

整体应克制、专业、低噪音。

建议颜色语义：

- Running：cyan / blue
- Completed：green
- WaitingHuman：yellow
- Failed：red
- Artifact：magenta / purple
- Memory：dim blue
- Retry / compression：amber
- 普通日志：dim

避免：

- 大量 emoji
- 高饱和渐变
- 花哨 ASCII art
- 过度边框
- 原始日志刷屏

终端宽度有限，中文和 emoji 可能引发宽度计算问题。第一版应优先使用 ASCII 状态符号和短标签。

## 建议目录结构

```text
cmd/tui/
  main.go

pkg/tui/
  app.go
  model.go
  update.go
  view.go
  styles.go
  keymap.go
  components/
    tree.go
    node_detail.go
    artifacts.go
    events.go
    memory.go
    human_input.go
    command_palette.go

pkg/runtime/
  events.go
```

职责划分：

- `cmd/tui`：参数解析、engine 初始化、save/load 初始化、启动 Bubble Tea
- `pkg/tui`：所有 UI 状态和渲染
- `pkg/runtime/events.go`：结构化 runtime event 类型
- `pkg/runtime`：继续负责执行逻辑，不依赖 TUI

## 开发里程碑

### Milestone 1：Runtime event hook

目标：让 runtime 可被 TUI 观察。

任务：

- 新增 `RuntimeEvent`
- 新增 `Runtime.OnEvent`
- 在关键位置 emit event
- 保留现有 CLI 输出
- 添加基础单元测试，确认事件触发顺序

验收：

- CLI 行为不变
- runtime 执行时能收到 step / node / error / human input 事件

### Milestone 2：TUI shell

目标：启动一个可运行的 Bubble Tea UI。

任务：

- 新增 `cmd/tui/main.go`
- 新增基础 layout
- 支持输入任务
- 支持启动 runtime goroutine
- 通过 channel 接收 event
- 展示状态栏和 event log

验收：

- 可以从 TUI 输入任务并运行
- 可以看到 step 和当前 node

### Milestone 3：Human-in-the-loop

目标：在 TUI 内完成 human input。

任务：

- 实现 human input 面板
- 接入 `Runtime.HumanInputFunc`
- 支持 options 和自定义输入
- 支持 note
- 支持 load 后恢复 WaitingHuman 节点

验收：

- LLM 触发 `request_human_input` 后，TUI 内可回答
- 回答后 runtime 继续执行
- 不需要退出 TUI 执行 `--resume`

### Milestone 4：AST 和 node detail

目标：展示 LLMVM 的核心执行结构。

任务：

- 实现 tree component
- 当前 cursor 高亮
- 展示 node status / type
- 实现 node detail 面板
- 支持展开 / 折叠 / 搜索

验收：

- 用户能清楚看到 runtime 当前在树上的位置
- 可以查看任意 node 的 handoff、summary、outputs 等结构化信息

### Milestone 5：Artifacts 和 memory

目标：让 TUI 能浏览 runtime 的结构化信息资产。

任务：

- artifact list
- artifact preview
- artifact slice view
- memory query panel
- 当前 node 相关 memory 展示

验收：

- 可以从 TUI 查看 artifact 摘要和部分内容
- 可以查询 SQLite memory

### Milestone 6：Polish 和稳定性

目标：达到日常可用。

任务：

- resize 适配
- command palette
- raw mode
- error panel
- prompt inspect
- 保存提示
- help screen
- go test
- 手动跑完整任务

验收：

- 长任务执行期间 UI 不阻塞
- Ctrl+C 能保存
- load/resume 流程稳定
- 终端 resize 不破坏布局

## MVP 验收标准

第一版完成后，应至少支持：

- 启动 TUI
- 输入任务
- 执行 runtime
- 显示当前 step、node、event
- 显示 AST 树
- 显示当前 node 详情
- 在 TUI 内处理 human input
- 保存 state
- 加载 state
- 检测并恢复 WaitingHuman 状态
- 执行结束后查看 final syntax tree 和 artifact summaries

## 风险与注意事项

### Runtime 输出与 TUI 事件重复

初期可以保留 `fmt.Printf`，但 TUI 运行时应尽量避免 stdout 干扰 Bubble Tea 画面。

后续可以做输出抽象：

- CLI 使用 text logger
- TUI 使用 event subscriber
- 测试使用 mock subscriber

### 并发与数据竞争

runtime 在 goroutine 中执行，TUI 在 Bubble Tea update loop 中渲染。

需要注意：

- root tree 读取要避免数据竞争
- artifact store 读取要避免数据竞争
- event payload 尽量传 snapshot，而不是直接传可变指针
- 必要时增加 mutex 或 snapshot API

### HumanInputFunc 阻塞

`HumanInputFunc` 会等待用户输入。TUI 需要用 channel 把 request 发给 UI，再等待 response，不能直接在 Bubble Tea update 中阻塞。

### Terminal 宽度

中文、emoji、宽字符可能导致布局错位。第一版建议主要使用 ASCII 标签，中文内容放在正文区域，不放在状态符号和边框关键位置。

### SaveState 兼容性

如果 TUI 引入额外 UI 状态，不应写入 runtime save state。UI 状态可以单独存配置，避免污染现有 `SaveState`。

## 推荐优先级

优先做：

1. Runtime events
2. TUI shell
3. Human-in-the-loop
4. AST tree / current node
5. Save / load / resume
6. Artifacts
7. Memory
8. Prompt/raw/debug views

原因：

LLMVM 最有辨识度的不是普通聊天界面，而是 AST runtime 和 human-in-the-loop。第一版应先把“运行过程可见、可介入、可恢复”做好。
