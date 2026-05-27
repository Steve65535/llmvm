# 执行循环与 Prompt

上级：[[../运行时引擎|运行时引擎]]

## `NewRuntime(engine llm.Engine, root *tasknode.TaskNode, dbPath ...string) *Runtime`

- 源码：[pkg/runtime/runtime.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/runtime/runtime.go:93)
- 参数：
  - `engine`: `llm.Engine`，模型调用接口。
  - `root`: `*tasknode.TaskNode`，任务树根节点。
  - `dbPath`: `...string`，可选 SQLite 文件路径；为空时使用 `:memory:`。
- 返回：`*Runtime`。
- 作用：初始化 cursor、VFS、artifact store、上下文预算和 SQLite memory store。
- 副作用：读取 `CONTEXT_BUDGET` 环境变量；可能打开 SQLite 数据库。

## `Execute(initialRequest string) error`

- 源码：[pkg/runtime/runtime.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/runtime/runtime.go:123)
- 参数：`initialRequest` 是根任务文本。
- 返回：`error`，执行失败、prompt 构造失败、解析失败或 action 执行失败时返回。
- 作用：运行主 DFS 循环。每轮选择当前节点，构造上下文，调用 LLM，解析 action，执行 action，然后移动游标。
- 关键逻辑：
  - 节点切换时重置 stagnation 检测和压缩等级。
  - 已完成节点直接进入 `decideNextStep`。
  - `WaitingHuman` 节点跳过，等待外部恢复。
  - prompt 超预算时逐级压缩，最多到 level 4。
  - 捕获 context overflow 后继续压缩重试。

## `buildPromptWithGlobalContext(...)`

- 源码：[pkg/runtime/runtime.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/runtime/runtime.go:372)
- 参数：当前节点、初始请求、global context、上次错误。
- 返回：prompt 字符串和错误。
- 作用：把当前节点状态、结构化上下文、变量、错误反馈和 action schema 组织成一次无状态模型输入。

## `EstimateTokenCount(text string) int`

- 源码：[pkg/runtime/runtime.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/runtime/runtime.go:1723)
- 参数：`text` 为 prompt 或上下文文本。
- 返回：估算 token 数。
- 作用：用字符数粗略估计 token，用于预算预检和压缩判断。

