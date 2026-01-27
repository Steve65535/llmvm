package llm

type Output struct {
	Response string
}

type Engine interface {
	Call(prompt string) (*Output, error)
	CallAsync(prompt string) <-chan *Output
}
