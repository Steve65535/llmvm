# 模型调用与 Prompt 协议

上级：[[../LLM接口与Action协议|LLM 接口与 Action 协议]]

## `Engine`

- 源码：[pkg/llm/engine.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/llm/engine.go:3)
- 形状：`Call(prompt string) (*Output, error)` 与 `CallAsync(prompt string) <-chan *Output`。
- 作用：抽象同步和异步 LLM 调用。runtime 只依赖这个接口。

## `NewLLMEngine() (*APIEngine, error)`

- 源码：[pkg/llm/api.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/llm/api.go:61)
- 返回：配置好的 `APIEngine`。
- 作用：读取环境变量并创建真实模型适配器。
- 失败场景：缺少 API key 或配置不完整时返回错误；CLI 会退回 `StubEngine`。

## `APIEngine.Call(prompt string) (*Output, error)`

- 源码：[pkg/llm/api.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/llm/api.go:80)
- 参数：runtime 构造出的完整 prompt。
- 返回：模型输出文本和错误。
- 副作用：发起 HTTP 请求；调用 `logLLMConversation` 记录输入、输出和 token usage。
- 关键约束：系统 prompt 要求模型只返回单个合法 JSON object，且不得使用 Markdown code fence。

## `StubEngine.Call(prompt string) (*Output, error)`

- 源码：[pkg/llm/sub.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/llm/sub.go:11)
- 作用：在无真实 API 时返回固定或简单的测试响应，方便本地启动和单元测试。

