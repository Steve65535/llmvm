# ArtifactStore

上级：[[../Artifact与结构化记忆|Artifact 与结构化记忆]]

## `Artifact`

- 源码：[pkg/artifact/store.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/artifact/store.go:37)
- 作用：保存工具输出、证据、设计合同等可引用信息单元。
- 关键字段：`ID`、`Type`、`Source`、`Summary`、`Content`、`SpillPath`、`Pinned`、`Name`、`Scope`、`Tags`、`Version`、`Supersedes`。

## `Store.Add(typ, source, content, createdBy string) *Artifact`

- 源码：[pkg/artifact/store.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/artifact/store.go:129)
- 作用：兼容旧工具产物入口，内部转为 `AddStructured`。
- 副作用：可能触发大内容 spill-to-disk 和 store eviction。

## `Store.AddStructured(req AddRequest) *Artifact`

- 源码：[pkg/artifact/store.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/artifact/store.go:139)
- 参数：
  - `req`: `AddRequest`，包含类型、来源、内容、作用域、tag、版本关系等。
- 返回：`*Artifact`。
- 作用：创建一等 artifact。超过 `MaxContentSize` 的内容写入 `test/sandbox/.artifacts`。
- 副作用：当 `Supersedes` 非空时，旧 artifact 会记录 `SupersededBy`。

## `Store.Read(id string) (string, error)`

- 源码：[pkg/artifact/store.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/artifact/store.go:263)
- 作用：按 ID 读取 artifact 内容，自动处理 inline content 或 spill file。
