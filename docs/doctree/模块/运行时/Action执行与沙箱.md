# Action 执行与沙箱

上级：[[../运行时引擎|运行时引擎]]

## `ExecuteAction(action llm.Action, parent *tasknode.TaskNode) error`

- 源码：[pkg/runtime/dispatch.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/runtime/dispatch.go:22)
- 参数：
  - `action`: `llm.Action`，parser 已解码的 action。
  - `parent`: `*tasknode.TaskNode`，当前节点。
- 返回：`error`，action 越权、参数非法或执行失败时返回。
- 作用：先根据 `BuildPosition` 做 authority check，再通过 switch 分发到具体 handler。
- 副作用：所有任务树 mutation、工具调用、artifact 读写和 memory 查询都经由此入口。

## `actionMarkComplete(action llm.Action, parent *tasknode.TaskNode) error`

- 源码：[pkg/runtime/dispatch.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/runtime/dispatch.go:88)
- 作用：校验 acceptance results，写入 summary/handoff/key facts/artifact refs，并把节点状态设为 `Completed`。
- 副作用：同步节点到 SQLite；pin 重要 artifact；异步索引节点 artifact 到 vector store。
- 失败条件：required acceptance criterion 缺失时拒绝完成。

## `actionExecuteCommand(action llm.Action, parent *tasknode.TaskNode) error`

- 源码：[pkg/runtime/dispatch.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/runtime/dispatch.go:221)
- 作用：执行 shell 命令，将完整输出存入 artifact，并把摘要写入 `command_output_history`。
- 当前语义：命令 exit 非 0 被当作模型可观察结果，不再直接使 action 失败；只有命令无法启动等基础执行错误才应成为 infrastructure error。
- 副作用：写 artifact store、FTS index、当前节点变量。

## `sandboxPath(p string) (string, error)`

- 源码：[pkg/runtime/shell.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/runtime/shell.go:31)
- 作用：为 file tool 提供路径约束，确保文件操作限制在 `test/sandbox/` 内。
- 注意：shell execution 与 file tool 的权限边界不同；shell 被设计成显式强能力。
