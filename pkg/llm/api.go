package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"time"
)

// OpenAI Compatible Structures
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type ChatResponse struct {
	Choices []struct {
		Message ChatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// APIEngine 调用通用 HTTP LLM API (OpenAI Compatible)
type APIEngine struct {
	URL    string            // API 地址
	Model  string            // 模型名称
	Header map[string]string // Header，比如 API Key
}

// NewAPIEngine 构造函数
func NewAPIEngine(url string, model string, header map[string]string) *APIEngine {
	return &APIEngine{
		URL:    url,
		Model:  model,
		Header: header,
	}
}

// NewDeepSeekEngine 创建 DeepSeek 专用引擎
func NewDeepSeekEngine() (*APIEngine, error) {
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("DEEPSEEK_API_KEY environment variable is not set")
	}

	return NewAPIEngine(
		"https://api.deepseek.com/chat/completions",
		"deepseek-chat", // or deepseek-reasoner
		map[string]string{
			"Authorization": "Bearer " + apiKey,
		},
	), nil
}

// 同步调用
func (e *APIEngine) Call(prompt string) (*Output, error) {
	systemPrompt := `You are the Arithmetic Logic Unit (ALU) of LLMVM, a Turing-complete virtual machine.
Your role is to process semantic state and return structured actions for the Go-based CPU (Runtime) to execute.

## Key Principles

1. **Stateless Reasoning**: You receive a snapshot of the current node, its path, and an ephemeral "Global Workspace" (RAM). You must not rely on previous turns; all necessary info is in the prompt.
2. **Global Workspace (Ephemeral RAM)**: This is your high-speed memory. Use the 'is_important' flag or 'result' fields to store key findings and variables.
4. **Tool Use (FULL SHELL POWER)**:
    - Use 'execute_command' for ANY terminal command (ls, cat, grep, find, curl, go test, etc.).
    - **SUPPORTED**: Shell piping (|), redirection (>), background tasks, and standard Linux/Mac utilities.
    - **PERSISTENCE**: Remember, 'mark_complete' results are just memory. To permanently save a file, you must use 'write' or redirection (e.g., 'echo content > file.md').
    - Your Current Working Directory is the project root.
    - Use 'create_node' for task decomposition.
    - Use 'mark_complete' or 'update_variables' for state transition.
5. **Node Types**:
    - Normal: Task decomposition.
    - Loop: Cyclic/iterative tasks.
    - Leaf: Atomic tasks that fit in one context window. If a task feels complex, decompose it!

## Response Format
You must respond with a single valid JSON object containing an array of 'actions'.
` + "`" + `json
{
  "actions": [
    {
      "action_type": "create_node",
      "node": { "id": "...", "name": "...", "type": "Leaf", "information": "..." }
    },
    {
      "action_type": "execute_command",
      "command": "ls ."
    }
  ]
}
` + "`" + `

## Important Notes

- Actions are executed sequentially. 
- Results of execute_command will appear in your variables as last_command_result in the NEXT step.
- Think step by step. Use the global context to find information from previously completed branches.
`

	reqBody := ChatRequest{
		Model: e.Model,
		Messages: []ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: prompt},
		},
		Stream: false,
	}
	data, _ := json.Marshal(reqBody)

	req, err := http.NewRequest("POST", e.URL, bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range e.Header {
		req.Header.Set(k, v)
	}

	client := &http.Client{
		Timeout: 60 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	bodyBytes, _ := ioutil.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(bodyBytes, &chatResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if chatResp.Error != nil {
		return nil, fmt.Errorf("API error: %s", chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices returned")
	}

	return &Output{Response: chatResp.Choices[0].Message.Content}, nil
}

// 异步调用
func (e *APIEngine) CallAsync(prompt string) <-chan *Output {
	ch := make(chan *Output, 1)
	go func() {
		out, err := e.Call(prompt)
		if err != nil {
			// 在实际生产中，Output 可能需要包含 Error 字段来传递异步错误
			// 这里简单打印或作为空响应处理
			fmt.Printf("Async Call Error: %v\n", err)
		}
		if out != nil {
			ch <- out
		}
		close(ch)
	}()
	return ch
}
