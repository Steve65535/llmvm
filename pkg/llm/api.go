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

// Call 同步调用。
//
// system prompt 从 prompts/system.md（embed）加载，方便迭代时只改 markdown。
// 调用 LLM API → 解析返回 → logLLMConversation 落盘到 test/llm_logs.txt 供事后回放。
func (e *APIEngine) Call(prompt string) (*Output, error) {
	systemPrompt := SystemPrompt()

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
	logLLMConversation(prompt, respContent, chatResp.Usage)
	return &Output{Response: respContent}, nil
}

// logLLMConversation 将对话记录到 test/llm_logs.txt（事后回放与调试用）。
func logLLMConversation(input, output string, usage Usage) {
	logDir := "test"
	logFile := filepath.Join(logDir, "llm_logs.txt")

	if _, err := os.Stat(logDir); os.IsNotExist(err) {
		_ = os.MkdirAll(logDir, 0755)
	}

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	separator := "================================================================================"
	logEntry := fmt.Sprintf("%s\n[%s]\n[TOKEN USAGE] Input: %d | Output: %d | Total: %d\n\n[INPUT]\n%s\n\n[OUTPUT]\n%s\n%s\n\n",
		separator, timestamp, usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens, input, output, separator)

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

// CallAsync 异步调用。
func (e *APIEngine) CallAsync(prompt string) <-chan *Output {
	ch := make(chan *Output, 1)
	go func() {
		out, err := e.Call(prompt)
		if err != nil {
			fmt.Printf("Async Call Error: %v\n", err)
		}
		if out != nil {
			ch <- out
		}
		close(ch)
	}()
	return ch
}
