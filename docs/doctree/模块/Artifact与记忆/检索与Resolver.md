# 检索与 Resolver

上级：[[../Artifact与结构化记忆|Artifact 与结构化记忆]]

## `retrieval.Service.Query(ctx context.Context, q Query) (Result, error)`

- 源码：[pkg/retrieval/retrieval.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/retrieval/retrieval.go:64)
- 参数：
  - `ctx`: `context.Context`。
  - `q`: `retrieval.Query`，包含 node id、path、goal、acceptance、自然语言 need、scope/tag 过滤和预算。
- 返回：`Result, error`。
- 作用：合并 SQLite metadata、SQLite FTS、vector search，进行确定性 rerank 并按 token 预算裁剪。
- 副作用：当 memory store 可用时记录 retrieval/rerank trace。

## `computeFinalScore(it RetrievedItem, q Query, now time.Time) float64`

- 源码：[pkg/retrieval/retrieval.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/retrieval/retrieval.go:179)
- 作用：用固定权重融合 vector similarity、FTS rank、acceptance relevance、pinned、importance 和 recency。
- 设计目的：可复现、可调试，不依赖额外 LLM rerank。

## `resolver.Resolver.Resolve(ctx context.Context, req Request) (Result, error)`

- 源码：[pkg/resolver/resolver.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/resolver/resolver.go:49)
- 作用：针对大 artifact 提取相关 evidence spans，而不是把整份内容塞入 prompt。
- 使用位置：runtime 在命令输出过大、artifact 需要精读时调用。
