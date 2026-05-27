# SQLite Memory

上级：[[../Artifact与结构化记忆|Artifact 与结构化记忆]]

## `Store`

- 源码：[pkg/memory/memory.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/memory/memory.go:17)
- 作用：SQLite 结构化检索层。它索引任务节点、handoff、作用域变量、artifact 元数据和 FTS 文本。

## `New(path string) (*Store, error)`

- 源码：[pkg/memory/memory.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/memory/memory.go:24)
- 参数：SQLite 路径；`:memory:` 表示内存库。
- 返回：初始化后的 store。
- 副作用：打开数据库并执行 schema migration。

## 写入 API

- `UpsertNode`：[pkg/memory/memory.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/memory/memory.go:125)，写入节点元数据。
- `UpsertHandoff`：[pkg/memory/memory.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/memory/memory.go:148)，写入结构化交接。
- `UpsertScopedVariable`：[pkg/memory/memory.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/memory/memory.go:182)，写入变量索引。
- `UpsertArtifact`：[pkg/memory/memory.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/memory/memory.go:200)，写入 artifact 元数据。
- `IndexArtifactFTS`：[pkg/memory/memory.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/memory/memory.go:232)，写入全文检索表。

## 查询 API

- `QueryAncestorChain`：查当前节点祖先链。
- `QuerySiblingHandoffs`：查同父节点下兄弟交接。
- `QueryPinnedArtifacts` / `QueryRecentArtifacts`：查重要或最近 artifact。
- `SearchArtifactsFTS`：用 FTS 查询 artifact 内容。
- `QueryScopedVariables`：按节点 ID 链查询变量。

## `Rebuild(input RebuildInput) error`

- 源码：[pkg/memory/memory.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/memory/memory.go:518)
- 作用：从 AST 和 artifact store 重建索引。`cmd/main.go` 在 `--load` 后会调用 runtime 的 rebuild 路径，避免 SQLite 文件过期。

