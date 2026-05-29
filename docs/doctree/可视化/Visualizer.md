# Visualizer

上级：[[../README|项目总览]]

Visualizer 是读取保存状态的调试工具，由 Go backend 和 Vite/React frontend 组成。

## Backend

### `main()`

- 源码：[visualizer/server.go](/Users/steve/Desktop/llmvm-rag-exp/visualizer/server.go:14)
- 参数：通过 flag 接收 `--port` 和 `--file`。
- 作用：启动 HTTP server，读取 state JSON，并暴露任务树和 artifact API。

### `/api/state`

- 源码：[visualizer/server.go](/Users/steve/Desktop/llmvm-rag-exp/visualizer/server.go:36)
- 作用：返回任务树。兼容新格式 `{root, artifacts}` 和旧格式顶层 TaskNode。

### `/api/artifacts`

- 源码：[visualizer/server.go](/Users/steve/Desktop/llmvm-rag-exp/visualizer/server.go:69)
- 作用：返回 artifact store；旧 state 没有 artifact 时返回空 store。

## Frontend

### `App()`

- 源码：[visualizer/frontend/src/App.jsx](/Users/steve/Desktop/llmvm-rag-exp/visualizer/frontend/src/App.jsx:3)
- 作用：拉取 `/api/state` 和 `/api/artifacts`，渲染任务树、节点状态和 artifact 信息。

这是开发者调试 UI，不是面向用户的产品页面；文档和后续 UI 设计应保持密集、可扫描、便于定位 runtime 状态。
