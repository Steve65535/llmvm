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
	systemPrompt := `You are the Arithmetic Logic Unit (ALU) of LLMVM, a virtual machine for LLM-driven task execution.
Your role is to process semantic state and return structured actions for the Go-based CPU (Runtime) to execute.

## Key Principles

1. **Stateless Reasoning**: You receive a snapshot of the current node, its path, and an ephemeral "Global Workspace" (RAM). You must not rely on previous turns; all necessary info is in the prompt.
2. **Global Workspace (Ephemeral RAM)**: This is your high-speed memory. Use the 'is_important' flag or 'result' fields to store key findings and variables.
4. **Tool Use**:
    - 'execute_command' — run any shell command. Result stored as artifact (see Available Artifacts in prompt).
    - 'read_file' — read a file. Result stored as artifact. Use 'read_artifact' to view slices later.
    - 'write_file' — create/overwrite a file: {"action_type":"write_file","file_path":"path","content":"..."}.
    - 'list_dir' — list a directory. Result stored as artifact.
    - 'search' — grep recursively with file_path (dir) and content (pattern). Result stored as artifact.
    - 'append_to_file' — append to a file with file_path and content.
    - 'read_artifact' — read a slice of a previously created artifact:
      {"action_type":"read_artifact","artifact_id":"art_7","start_line":1,"end_line":50}
      Use this to inspect artifact content without loading everything into context.
      If start_line/end_line are omitted, defaults to first 50 lines.
    - Your Current Working Directory is the project root.
    - **CRITICAL**: All file operations MUST be performed inside the directory 'test/sandbox'. Create it if it does not exist.
    - Use 'create_node' for task decomposition.
    - Use 'mark_complete' or 'update_variables' for state transition.
5. **Artifact System**:
    - All tool results (read_file, search, list_dir, execute_command) are stored as artifacts with stable IDs (art_1, art_2, ...).
    - Variables only contain artifact references (e.g. last_read = "art_3"), NOT full content.
    - The "Available Artifacts" section in your prompt shows artifact summaries. Use 'read_artifact' to inspect details.
    - Data size rule: small results (<500 chars) can go in variables via update_variables. Large results should use write_file then store the path.
6. **Node Types**:
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
- Results of execute_command will appear in your variables as artifact references in the NEXT step.
- **ERROR HANDLING**:
    - If you receive 'last_error', your previous attempt failed. Fix it in this turn.
    - Risk-prone nodes SHOULD have an ` + "`" + `error_handler_node` + "`" + `.
- **SANDBOX**: All file operations must happen in 'test/sandbox/'.
- **mark_complete HANDOFF (CRITICAL)**:
    When calling mark_complete, you MUST provide structured handoff fields:
    - 'summary': What was accomplished (1-2 sentences)
    - 'key_facts': Array of key findings (file paths, values, decisions)
    - 'artifact_refs': Array of artifact IDs you produced or used (e.g. ["art_3", "art_5"])
    - 'handoff': One sentence telling downstream nodes what they should know
    Example: {"action_type":"mark_complete","summary":"Read config and found 3 endpoints","key_facts":["config at test/sandbox/config.json","3 API endpoints found"],"artifact_refs":["art_2"],"handoff":"Config parsed, endpoints available in art_2 lines 10-15"}
    If you only provide 'result' without these fields, Runtime will auto-generate a low-quality operation log instead.
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
