# LLM 接口与 Action 协议

上级：[[../README|项目总览]]

`pkg/llm` 定义模型调用接口、action DTO、JSON parser 和 prompt/API 文档。它是 LLM 输出进入 runtime 的第一道结构化边界。

## 子页面

- [[模块/LLM接口/JSON解析与Action校验|JSON 解析与 Action 校验]]
- [[模块/LLM接口/模型调用与Prompt协议|模型调用与 Prompt 协议]]

## 关键源码

- [parser.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/llm/parser.go:1)：Action/Response DTO 和 parser validation。
- [api.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/llm/api.go:1)：DeepSeek/OpenAI-compatible API 请求封装和系统 prompt。
- [engine.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/llm/engine.go:1)：LLM engine 接口。
- [async.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/llm/async.go:1)：异步 engine 包装。
- [prompts/system.md](/Users/steve/Desktop/llmvm-rag-exp/pkg/llm/prompts/system.md:1)：嵌入式系统 prompt 文档。

## 协议边界

模型输出必须是一个 JSON object，顶层包含 `actions` 数组。parser 负责把常见 Markdown code fence 清理掉，再执行字段级校验。runtime 仍会再次进行 authority check 和 action-specific 执行校验。
