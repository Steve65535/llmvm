# Action 执行与沙箱

上级：[[../运行时引擎|运行时引擎]]

## `ExecuteAction(action llm.Action, parent *tasknode.TaskNode) error`

- 源码：[pkg/runtime/runtime.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/runtime/runtime.go:1248)
- 参数：
  - `action`: LLM 返回的结构化 action。
  - `parent`: 当前 action 所属节点。
- 返回：执行错误。
- 作用：根据 `action_type` 分发创建节点、更新变量、执行命令、读写文件、读取 artifact、mark complete、请求人类输入、追加 sibling、查询 memory 等行为。
- 副作用：会修改任务树、节点变量、artifact store、SQLite memory、文件系统或 shell 状态。

## `handleRequestHumanInput(action llm.Action, node *tasknode.TaskNode) error`

- 源码：[pkg/runtime/actions.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/runtime/actions.go:27)
- 作用：把模型的人类输入请求保存到节点，设置 `WaitingHuman`，并根据是否注入 `HumanInputFunc` 决定同步恢复还是返回 `ErrWaitingHuman`。

## `ResumeWithHumanResponse(nodeID string, resp *tasknode.HumanResponse) error`

- 源码：[pkg/runtime/actions.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/runtime/actions.go:99)
- 作用：找到等待人类输入的节点，写入结构化回复并恢复为 `Pending`。

## `sandboxPath(filePath string) (string, error)`

- 源码：[pkg/runtime/runtime.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/runtime/runtime.go:2015)
- 作用：把文件工具限制在 `test/sandbox/` 下，避免路径逃逸。
- 注意：shell command 本身不是这个文件工具 sandbox 的一部分。

