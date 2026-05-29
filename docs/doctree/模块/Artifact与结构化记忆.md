# Artifact 与结构化记忆

上级：[[../README|项目总览]]

Artifact 和 memory 是 LLMVM 的证据与检索层。artifact store 保存大输出和可复用证据；SQLite memory 保存可重建索引；retrieval/vector/resolver 负责把相关证据按预算装配回当前节点上下文。

## 子页面

- [[模块/Artifact与记忆/ArtifactStore|ArtifactStore]]
- [[模块/Artifact与记忆/SQLiteMemory|SQLiteMemory]]
- [[模块/Artifact与记忆/检索与Resolver|检索与 Resolver]]

## 关键源码

- [pkg/artifact/store.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/artifact/store.go:1)：artifact 生命周期。
- [pkg/memory/memory.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/memory/memory.go:1)：SQLite schema 和查询。
- [pkg/retrieval/retrieval.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/retrieval/retrieval.go:1)：混合召回和 rerank。
- [pkg/vector/vector.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/vector/vector.go:1)：chromem-go 向量索引。
- [pkg/resolver/resolver.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/resolver/resolver.go:1)：大 artifact 切片解析。
