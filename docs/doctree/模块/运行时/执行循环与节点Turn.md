# 执行循环与节点 Turn

上级：[[../运行时引擎|运行时引擎]]

## `Runtime.Execute(initialRequest string) error`

- 源码：[pkg/runtime/execute.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/runtime/execute.go:17)
- 参数：
  - `initialRequest`: `string`，根任务的原始用户请求。
- 返回：`error`，执行循环或 cursor 推进失败时返回。
- 作用：执行 DFS 风格任务树循环，处理节点状态、构造上下文、运行单次 node turn，并在每步后推进 cursor。
- 副作用：更新 `TaskNode` 状态、调用 LLM engine、执行 action、触发 `OnStepComplete` 自动保存回调。

执行循环会跳过已完成节点和 WaitingHuman 节点；Normal 节点按未遍历子节点向下走；Leaf 节点交给 agentic loop 判断是否继续留在本节点。

## `runNodeTurn(ctx, current, initialRequest, globalContext) turnResult`

- 源码：[pkg/runtime/execute.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/runtime/execute.go:158)
- 参数：
  - `ctx`: `context.Context`，支持单 turn timeout。
  - `current`: `*tasknode.TaskNode`，当前执行节点。
  - `initialRequest`: `string`，原始任务。
  - `globalContext`: `string`，runtime 预先装配的全局上下文。
- 返回：`turnResult`，包含 `turnOK`、`turnFailed`、`turnWaitingHuman`、`turnShutdown`。
- 作用：完成一次 prompt 构造、预算压缩、LLM 调用、JSON 解析、停滞检测和 action 执行。
- 副作用：可能更新当前节点变量、artifact、memory index，并执行 shell/file 等工具动作。

## `decideNextStep(current *tasknode.TaskNode) error`

- 源码：[pkg/runtime/execute.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/runtime/execute.go:120)
- 参数：
  - `current`: `*tasknode.TaskNode`，刚完成一轮处理的节点。
- 返回：`error`，当节点尚未标记 traveled 却需要推进时返回。
- 作用：根据 Leaf/Normal、子节点遍历状态和完成状态移动 cursor。
- 副作用：可能调用 `MarkFinished()` 或 `cursor.MoveUp()/MoveDown()`。

## `HandleLeafAgenticLoop(current *tasknode.TaskNode) bool`

- 源码：[pkg/runtime/agentic_loop.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/runtime/agentic_loop.go:10)
- 参数：
  - `current`: `*tasknode.TaskNode`，候选 Leaf 节点。
- 返回：`bool`，表示是否由 leaf loop 处理了 cursor 决策。
- 作用：让 Leaf 节点在未达成 completion 时继续停留并增加迭代计数。
- 重要语义：Failed 节点会被标记为 terminal 并上移，但不会被改写成 Completed。
