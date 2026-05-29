# 模型调用与 Prompt 协议

上级：[[../LLM接口与Action协议|LLM 接口与 Action 协议]]

## `Engine`

- 源码：[pkg/llm/engine.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/llm/engine.go:3)
- 签名：`type Engine interface { Call(prompt string) (string, error) }`
- 作用：隔离 runtime 与具体模型 provider。runtime 只依赖 `Call`。

## `CallLLM(prompt string) (string, error)`

- 源码：[pkg/llm/api.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/llm/api.go:38)
- 参数：
  - `prompt`: `string`，runtime 构造的完整 prompt。
- 返回：`string, error`，模型原始文本输出。
- 副作用：读取 `DEEPSEEK_API_KEY`，向 API endpoint 发起 HTTP 请求。

## `AsyncEngine.Call(prompt string) (string, error)`

- 源码：[pkg/llm/async.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/llm/async.go:15)
- 作用：把底层 engine 调用包在 goroutine/channel 中。
- 注意：runtime 当前的 node turn timeout 主要在 `pkg/runtime/execute.go` 层实现。
