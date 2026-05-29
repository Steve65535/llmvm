# CLI 与状态持久化

上级：[[../README|项目总览]]

`cmd/` 是 LLMVM 的命令行入口。它负责 flag、engine 初始化、保存/加载状态、human resume、信号处理和最终树打印。业务执行由 `pkg/runtime` 完成。

## `main()`

- 源码：[cmd/main.go](/Users/steve/Desktop/llmvm-rag-exp/cmd/main.go:26)
- 作用：解析 `--save`、`--load`、`--resume`，初始化 runtime，并启动执行循环。
- 副作用：可能重建 SQLite index、注册 autosave、安装紧急保存信号 handler。

## `obtainRoot(loadPath string) (*tasknode.TaskNode, *artifact.Store, string)`

- 源码：[cmd/main.go](/Users/steve/Desktop/llmvm-rag-exp/cmd/main.go:86)
- 作用：根据 `--load` 或命令行参数创建/恢复 root。

## `persistState(path string, root *tasknode.TaskNode, arts *artifact.Store)`

- 源码：[cmd/state.go](/Users/steve/Desktop/llmvm-rag-exp/cmd/state.go:25)
- 作用：把 root task tree、artifact store 和 initial request 写入 JSON。

## `loadState(path string) (*tasknode.TaskNode, *artifact.Store, string)`

- 源码：[cmd/state.go](/Users/steve/Desktop/llmvm-rag-exp/cmd/state.go:34)
- 作用：读取保存状态，恢复 task tree parent 指针和 artifact store。

## `handleResume(...) bool`

- 源码：[cmd/main.go](/Users/steve/Desktop/llmvm-rag-exp/cmd/main.go:135)
- 作用：处理 WaitingHuman 节点的恢复流程。显式 `--resume <node-id>` 时读取人类回复并注入 runtime。
