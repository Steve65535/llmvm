# 项目总览

## 这个仓库是做什么的

LLMVM 是一个用 Go 实现的 LLM 任务运行时。它把长任务表示为可恢复的任务树，由 runtime 负责控制流、状态、权限、artifact、检索索引和验收；LLM 只在当前节点上下文中产出结构化 action。

核心思想是：聊天历史不是持久状态。任务树、artifact store 和 SQLite/vector 索引才是可恢复状态。每次模型调用前，runtime 根据当前节点位置重新构造 context pack，调用模型，解析 JSON action，执行副作用，然后推进 cursor。

## 文档树

- [[模块/运行时引擎|运行时引擎]]
- [[模块/LLM接口与Action协议|LLM 接口与 Action 协议]]
- [[模块/任务树与游标|任务树与游标]]
- [[模块/Artifact与结构化记忆|Artifact 与结构化记忆]]
- [[运行与集成/CLI与状态持久化|CLI 与状态持久化]]
- [[可视化/Visualizer|Visualizer]]

## 主要数据流

```text
cmd/main.go
  -> runtime.NewRuntime
  -> Runtime.Execute
  -> BuildPosition / buildNodeActivation / retrieval.Query
  -> prompt
  -> llm.Engine.Call
  -> llm.ParseResponse
  -> Runtime.ExecuteAction
  -> TaskNode / ArtifactStore / SQLite / Vector
  -> cursor.MoveDown / MoveUp
```

## 架构原则

- AST/task tree 是控制流权威源。
- SQLite memory 和 vector store 是可重建检索镜像。
- 大工具输出优先进入 artifact store，变量和 handoff 引用 artifact ID。
- parser 和 runtime validation 是安全边界，不能只依赖 prompt。
- file tool 受 `test/sandbox/` 限制；shell execution 是更强的显式能力。

## 代码索引状态

索引文件位于 `docs/doctree/.index/`：

- `paths.json`：当前仓库路径索引。
- `symbols.json`：启发式函数/方法/组件符号索引。
- `coverage.json`：本轮 doctree 覆盖范围与已知缺口。
