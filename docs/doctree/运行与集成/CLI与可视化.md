# CLI 与可视化

上级：[[../README|项目总览]]

## CLI 入口

主入口是 [cmd/main.go](/Users/steve/Desktop/llmvm-rag-exp/cmd/main.go:29)。它负责解析命令行参数、初始化 LLM engine、创建或加载任务树、构造 runtime、执行任务并打印最终树。

常用命令：

```bash
go run cmd/main.go "Analyze this repository"
go run cmd/main.go --save state.json "Run a task"
go run cmd/main.go --load state.json
go run cmd/main.go --load state.json --resume node_id
```

## `SaveState`

- 源码：[cmd/main.go](/Users/steve/Desktop/llmvm-rag-exp/cmd/main.go:24)
- 字段：
  - `Root`: `*tasknode.TaskNode`。
  - `Artifacts`: `*artifact.Store`。
- 作用：保存任务树和 artifact 元数据。artifact 大内容本身不一定内联进 JSON，可能位于 spill 文件。

## `findWaitingHumanNodes(node *tasknode.TaskNode) []string`

- 源码：[cmd/main.go](/Users/steve/Desktop/llmvm-rag-exp/cmd/main.go:240)
- 作用：扫描任务树，返回所有 `WaitingHuman` 节点 ID。`--load` 时用于提示用户通过 `--resume` 注入回复。

## Visualizer 后端

后端入口是 [visualizer/server.go](/Users/steve/Desktop/llmvm-rag-exp/visualizer/server.go:14)。

```bash
cd visualizer
go run server.go -file ../deep_compiler.json
```

接口：

- `/api/state`：读取状态文件，兼容新格式 `{root, artifacts}` 和旧格式纯 TaskNode。
- `/api/artifacts`：返回 artifact store；旧状态没有 artifacts 时返回空列表。

## Visualizer 前端

前端入口是 [visualizer/frontend/src/App.jsx](/Users/steve/Desktop/llmvm-rag-exp/visualizer/frontend/src/App.jsx:3)。

它每秒轮询后端：

- `http://localhost:8081/api/state`
- `http://localhost:8081/api/artifacts`

主要视图：

- 树节点画布：按 status 着色。
- hover tooltip：展示节点信息、result、key facts、handoff、variables。
- artifact panel：展示 artifact ID、类型、摘要、pin/evicted 标记。

