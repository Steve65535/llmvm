package llm

// AsyncEngine 将已有同步 Engine 封装为异步
type AsyncEngine struct {
	inner Engine
}

func NewAsyncEngine(inner Engine) *AsyncEngine {
	return &AsyncEngine{inner: inner}
}

func (e *AsyncEngine) Call(prompt string) (*Output, error) {
	return e.inner.Call(prompt)
}

func (e *AsyncEngine) CallAsync(prompt string) <-chan *Output {
	ch := make(chan *Output, 1)
	go func() {
		out, _ := e.inner.Call(prompt)
		ch <- out
	}()
	return ch
}
