# Artifact Store

上级：[[../Artifact与结构化记忆|Artifact 与结构化记忆]]

## `Artifact`

- 源码：[pkg/artifact/store.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/artifact/store.go:21)
- 作用：保存一个带稳定 ID 的信息对象。
- 关键字段：`ID`、`Type`、`Source`、`Summary`、`OriginalLen`、`TotalLines`、`CreatedBy`、`Pinned`、`SpillPath`、`Evicted`。

## `Store.Add(typ, source, content, createdBy string) *Artifact`

- 源码：[pkg/artifact/store.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/artifact/store.go:101)
- 参数：类型、来源、内容、创建节点 ID。
- 返回：新 artifact。
- 作用：生成 `art_N` ID，摘要化内容，必要时 spill 到 `test/sandbox/.artifacts`，并触发淘汰。
- 副作用：可能写入磁盘。

## `ReadSlice(id string, startLine, endLine int) (string, error)`

- 源码：[pkg/artifact/store.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/artifact/store.go:174)
- 作用：按行读取 artifact 内容；`endLine=0` 时默认读取 50 行。
- 错误：artifact 不存在、已淘汰、spill 文件缺失、起始行越界。

## `Index(maxEntries int, maxChars int) string`

- 源码：[pkg/artifact/store.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/artifact/store.go:235)
- 作用：生成紧凑 artifact 索引供 prompt 使用，避免把完整内容塞进上下文。

