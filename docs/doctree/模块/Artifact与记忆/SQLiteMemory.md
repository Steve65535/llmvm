# SQLiteMemory

上级：[[../Artifact与结构化记忆|Artifact 与结构化记忆]]

## `Store`

- 源码：[pkg/memory/memory.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/memory/memory.go:17)
- 作用：SQLite 结构化检索层。它不是控制流权威源，而是从任务树和 artifact 重建出来的查询索引。

## `New(path string) (*Store, error)`

- 源码：[pkg/memory/memory.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/memory/memory.go:22)
- 参数：
  - `path`: `string`，SQLite 文件路径或 `:memory:`。
- 返回：`*Store, error`。
- 作用：打开数据库并执行幂等 migration。

## `migrate() error`

- 源码：[pkg/memory/memory.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/memory/memory.go:38)
- 作用：创建节点、handoff、scoped variables、artifact、acceptance、retrieval event、FTS5 等表。
- 注意：`schemaVersion = 2`，但当前 migration 是建表式幂等迁移，不是完整版本化迁移器。

## 重要写入接口

- `UpsertNode`：写入节点元信息。
- `UpsertHandoff`：写入结构化 handoff。
- `UpsertScopedVariable`：写入变量摘要。
- `UpsertArtifact` / `UpsertArtifactStructured`：写入 artifact 元数据。
- `IndexArtifactFTS`：写入 FTS5 内容。

这些接口通常由 runtime action handler 或 index rebuild 流程调用。
