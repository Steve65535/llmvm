# Cursor 遍历

上级：[[../任务树与游标|任务树与游标]]

## `Cursor`

- 源码：[pkg/cursor/cursor.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/cursor/cursor.go:9)
- 作用：保存任务树 root 和当前执行节点。

## `MoveDown() bool`

- 源码：[pkg/cursor/cursor.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/cursor/cursor.go:26)
- 返回：`bool`，是否成功移动到未遍历子节点。
- 作用：进入当前节点的下一个 untraveled child。

## `MoveFirstUnfinishedChild() bool`

- 源码：[pkg/cursor/cursor.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/cursor/cursor.go:38)
- 返回：`bool`，是否找到未完成 child。
- 作用：用于需要重新进入 unfinished child 的场景。

## `MoveUp() bool`

- 源码：[pkg/cursor/cursor.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/cursor/cursor.go:51)
- 返回：`bool`，是否移动到父节点；root 上移会把 current 置为 nil。
- 作用：完成当前分支后回到父节点或结束执行。
