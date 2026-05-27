# LLMVM 项目总览

## 这个仓库是做什么的

LLMVM 是一个用 Go 编写的 LLM Agent Runtime。它把复杂任务表示为显式任务树（AST），由 runtime 使用 DFS 调度节点，并在每个节点调用 LLM 产生结构化 action。系统强调无状态 prompt、可恢复执行、artifact 外置存储、SQLite 结构化记忆、节点交接和人类介入。

主程序入口是 [cmd/main.go](/Users/steve/Desktop/llmvm-rag-exp/cmd/main.go:29)。核心执行器是 [pkg/runtime/runtime.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/runtime/runtime.go:66) 中的 `Runtime`。

## 核心运行模型

```text
CLI / saved state
  -> TaskNode AST
  -> Cursor DFS
  -> NodeActivation + Global Context
  -> LLM structured JSON actions
  -> Runtime action execution
  -> Artifact Store / SQLite Memory / TaskNode handoff
  -> Cursor moves to next node
```

AST 是控制流权威来源；SQLite memory 是可重建的检索索引；artifact store 是大文本、命令输出和文件读取结果的主要载体。

## 文档树

- [[模块/运行时引擎|运行时引擎]]
- [[模块/LLM接口与Action协议|LLM 接口与 Action 协议]]
- [[模块/任务树与游标|任务树与游标]]
- [[模块/Artifact与结构化记忆|Artifact 与结构化记忆]]
- [[运行与集成/CLI与可视化|CLI 与可视化]]
- [[运行与集成/测试与配置|测试与配置]]

## 关键源码路径

- `cmd/`：CLI、保存/恢复、人类回复注入。
- `pkg/runtime/`：执行循环、prompt、action、sandbox、activation、memory 同步。
- `pkg/llm/`：模型适配器、系统 prompt、JSON action 解析。
- `pkg/tasknode/`：任务树节点、状态、handoff、人类输入结构。
- `pkg/cursor/`：DFS 游标和循环栈。
- `pkg/artifact/`：artifact 生命周期、切片读取、淘汰和 spill。
- `pkg/memory/`：SQLite schema、索引写入、受限查询。
- `visualizer/`：保存状态的树形可视化。

## 当前覆盖范围

本 doctree 使用 architecture mode：覆盖主要模块、关键执行路径和代表性函数，不追求每个函数的全量文档。完整路径与符号索引见 `docs/doctree/.index/paths.json` 和 `docs/doctree/.index/symbols.json`。

