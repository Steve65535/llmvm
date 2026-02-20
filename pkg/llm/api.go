package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/joho/godotenv"
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

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type ChatResponse struct {
	Choices []struct {
		Message ChatMessage `json:"message"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
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

// NewLLMEngine 创建通用的 LLM 引擎（默认使用 DeepSeek）
func NewLLMEngine() (*APIEngine, error) {
	// 静默加载本目录或项目根目录下的 .env 文件
	_ = godotenv.Load(".env")

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
    - **CRITICAL**: All file operations (write, read, create) MUST be performed inside the directory 'test/sandbox'. Create it if it does not exist. Do not touch project root files.
    - **INCREMENTAL WRITES**: Use '>>' to append to files. Examples:
        * Append text: echo "new line" >> file.md
        * Merge files: cat part.md >> total.md
    - Use 'create_node' for task decomposition.
    - Use 'mark_complete' or 'update_variables' for state transition. **CRITICAL**: You MUST write important results (like filenames, summaries) into 'variables' so they persist for future nodes.
5. **Node Types**:
    - Normal: Task decomposition.
    - Loop: Cyclic/iterative tasks.
    - Leaf: Atomic tasks that fit in one context window. If a task feels complex, decompose it!

## Response Format (STRICT)
You must output a SINGLE, VALID JSON object.
- **NO Markdown**: Do not use markdown code block wrappers (e.g. triple backtick json). Just raw JSON.
- **NO Preamble/Postscript**: Do not write "Here is the JSON" or explanations.
- **Strict Keys**: Use only the keys defined below. Parsing will fail otherwise.
- **CRITICAL**: Failure to provide perfectly formatted JSON with correct fields will result in **SYSTEM FAILURE**. Your response is the ONLY way the VM functions.

Example:
{
  "actions": [
    {
      "action_type": "create_node",
      "node": { 
        "id": "node_v1", 
        "name": "Node With Handler", 
        "type": "Normal", 
        "information": "Description",
        "error_handler_id": "optional_id",
        "max_retries": 3
      },
      "error_handler_node": {
         "id": "recovery_node",
         "name": "Recovery Handler",
         "type": "Leaf",
         "information": "Executed if node_v1 fails"
      }
    }
  ]
}
` + "`" + `

## Important Notes


- Actions are executed sequentially. 
- Results of execute_command will appear in your variables as last_command_result in the NEXT step.
- **ERROR HANDLING**: 
    - If you receive 'last_error', your previous attempt failed. Fix it in this turn.
    - Risk-prone nodes SHOULD have an ` + "`" + `error_handler_node` + "`" + `.
- **SANDBOX**: All file operations must happen in 'test/sandbox/'.
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

	respContent := chatResp.Choices[0].Message.Content

	// 记录日志 (Input & Output & Usage)
	logLLMConversation(prompt, respContent, chatResp.Usage)

	return &Output{Response: respContent}, nil
}

// logLLMConversation 将对话记录到 test/llm_logs.txt
func logLLMConversation(input, output string, usage Usage) {
	logDir := "test"
	logFile := filepath.Join(logDir, "llm_logs.txt")

	// 确保目录存在
	if _, err := os.Stat(logDir); os.IsNotExist(err) {
		_ = os.MkdirAll(logDir, 0755)
	}

	// 准备日志内容
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	separator := "================================================================================"
	logEntry := fmt.Sprintf("%s\n[%s]\n[TOKEN USAGE] Input: %d | Output: %d | Total: %d\n\n[INPUT]\n%s\n\n[OUTPUT]\n%s\n%s\n\n",
		separator, timestamp, usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens, input, output, separator)

	// 以追加模式打开文件
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("⚠️ Failed to open log file: %v\n", err)
		return
	}
	defer f.Close()

	if _, err := f.WriteString(logEntry); err != nil {
		fmt.Printf("⚠️ Failed to write to log file: %v\n", err)
	}
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
