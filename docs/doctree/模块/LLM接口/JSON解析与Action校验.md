# JSON 解析与 Action 校验

上级：[[../LLM接口与Action协议|LLM 接口与 Action 协议]]

## `Action`

- 源码：[pkg/llm/parser.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/llm/parser.go:50)
- 作用：统一承载所有模型 action 的 DTO。不同 `action_type` 使用不同字段，例如 `Node`、`Command`、`FilePath`、`ArtifactID`、`Question`、`QueryType`。

## `Response`

- 源码：[pkg/llm/parser.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/llm/parser.go:97)
- 形状：`{"actions": [...]}`。
- 作用：模型响应的顶层结构。

## `ParseResponse(jsonStr string) (*Response, error)`

- 源码：[pkg/llm/parser.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/llm/parser.go:103)
- 参数：模型原始输出。
- 返回：解析后的 `Response`。
- 作用：
  - 调用 `CleanJSONString` 清理常见 Markdown 包裹。
  - 使用 `json.Unmarshal` 解码。
  - 按 action 类型校验必填字段和枚举值。
- 重要校验：
  - `create_node` 和 `append_sibling_node` 的 node type 必须是 `Normal`、`Loop` 或 `Leaf`。
  - 文件动作必须带 `file_path`，写入动作还必须带 `content`。
  - `read_artifact` 必须带 `artifact_id`。
  - `request_human_input` 必须带 `question`。
  - `query_memory` 只能使用受限 query type。

## `NodeDTO.ToTaskNode() *tasknode.TaskNode`

- 源码：[pkg/llm/parser.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/llm/parser.go:210)
- 作用：把模型返回的节点 DTO 转换为 runtime 内部 `TaskNode`，并映射节点类型、变量、重要性和错误处理字段。

