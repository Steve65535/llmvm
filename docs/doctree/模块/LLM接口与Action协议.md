# LLM 接口与 Action 协议

上级：[[../README|项目总览]]

## 职责

`pkg/llm/` 是模型边界层，负责把 runtime prompt 发给模型，并把模型响应解析为严格的 JSON action 列表。这里是 LLMVM 的协议边界：prompt 可以指导模型，但 parser 和 runtime 必须负责最终校验。

核心文件：

- [pkg/llm/api.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/llm/api.go:45)：DeepSeek/OpenAI-compatible chat API 调用和系统 prompt。
- [pkg/llm/parser.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/llm/parser.go:50)：Action DTO、Response DTO、JSON 清理和校验。
- [pkg/llm/engine.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/llm/engine.go:3)：模型引擎接口。
- [pkg/llm/sub.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/llm/sub.go:9)：StubEngine，用于无 API key 的测试运行。

## 子页面

- [[LLM接口/模型调用与Prompt协议|模型调用与 Prompt 协议]]
- [[LLM接口/JSON解析与Action校验|JSON 解析与 Action 校验]]

## 主要 Action

- `create_node`：创建子节点。
- `mark_complete`：完成当前节点并写入结构化 handoff。
- `execute_command`：执行 shell 命令，结果进入 artifact。
- `read_file` / `write_file` / `append_to_file` / `list_dir` / `search`：文件工具，受 sandbox 限制。
- `read_artifact`：按行读取 artifact 片段。
- `request_human_input`：暂停或同步请求人类输入。
- `append_sibling_node`：在当前节点后追加同级节点。
- `query_memory`：通过受限 query type 查询 SQLite memory。

