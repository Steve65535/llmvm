# 位置工程与 ContextPack

上级：[[../运行时引擎|运行时引擎]]

## `BuildPosition(node *tasknode.TaskNode) Position`

- 源码：[pkg/runtime/position.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/runtime/position.go:54)
- 参数：
  - `node`: `*tasknode.TaskNode`，当前节点。
- 返回：`Position`，包含 node id、parent id、depth、path、role、scope、authority 和 constraints。
- 作用：从任务树位置推导当前节点的角色和权限。
- 重要角色：`root`、`planner`、`executor`、`error_handler`。

## `CheckAuthority(pos Position, actionType string) error`

- 源码：[pkg/runtime/position.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/runtime/position.go:205)
- 参数：
  - `pos`: `Position`，当前节点位置。
  - `actionType`: `string`，候选 action 类型。
- 返回：`error`，越权时返回。
- 作用：把 prompt 中的允许动作约束落到 runtime validation，防止只依赖模型自觉。

## `buildNodeActivation(current *tasknode.TaskNode) NodeActivation`

- 源码：[pkg/runtime/activation.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/runtime/activation.go:40)
- 作用：为当前节点装配层级路径、父目标、兄弟 handoff、artifact index、open questions 和 working context。
- 数据源：优先 SQLite memory；不可用时回退 AST 和 artifact store。

## `formatActivationContext(act NodeActivation, current *tasknode.TaskNode) string`

- 源码：[pkg/runtime/activation.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/runtime/activation.go:182)
- 作用：把激活上下文格式化为 prompt 片段。
- 设计要点：位置上下文是确定性构造的，不依赖隐式聊天历史。
