# JSON 解析与 Action 校验

上级：[[../LLM接口与Action协议|LLM 接口与 Action 协议]]

## `ParseResponse(jsonStr string) (*Response, error)`

- 源码：[pkg/llm/parser.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/llm/parser.go:132)
- 参数：
  - `jsonStr`: `string`，LLM 原始输出。
- 返回：`*Response, error`，解析后的 action 列表或具体错误。
- 作用：清理 code fence、反序列化 JSON，并校验 action 类型、必填字段和枚举值。
- 重要错误：非法 node type、缺失 file path/content、非法 `query_type`、缺失 artifact 字段、非法 acceptance check type。

## `CleanJSONString(str string) string`

- 源码：[pkg/llm/parser.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/llm/parser.go:288)
- 参数：
  - `str`: `string`，可能带 Markdown code block 的模型输出。
- 返回：`string`，去掉外层 code fence 和空白后的 JSON 文本。
- 作用：容忍模型输出 ```json fenced block。

## `NodeDTO.ToTaskNode() *tasknode.TaskNode`

- 源码：[pkg/llm/parser.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/llm/parser.go:302)
- 参数：无显式参数，接收者为 `*NodeDTO`。
- 返回：`*tasknode.TaskNode`。
- 作用：把 LLM DTO 转换为内部 TaskNode，并转换 acceptance criteria。
- 注意：空 criterion ID 会被自动补成 `<nodeID>_ac_<n>`。
