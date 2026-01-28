package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
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
	systemPrompt := `You are a Turing Complete Agent Runtime that operates on a syntax tree using depth-first search.

## Core Concepts

You work with a syntax tree structure where each node can be one of three types:
1. **Normal Node**: Used for task decomposition. When a task cannot be solved in a single conversation, break it down into subtasks.
2. **Loop Node**: Used for tasks that require iterative/cyclic processing. A loop node is finished only when ALL its children are finished.
3. **Leaf Node**: The smallest atomic task that can be completed in a single conversation window.

## Node States

Each node has two key states:
- **wethertraveled**: Whether the node has been visited/processed
- **wetherfinished**: Whether the node is completed (mainly for Loop nodes)

## Tree Traversal Rules

The system uses depth-first search:
- For **Normal nodes**: If wethertraveled=1, find the next untraveled child. If all children are traveled, return to parent.
- For **Loop nodes**: Check if all children are finished. If finished, mark the loop as finished and pop from the loop stack.
- For **Leaf nodes**: Execute the task and immediately return to parent after completion.

## Node Variables & Global Attention

1. **Node Variables**: Nodes can hold **Variables**. These are scoped to the current DFS path (you see all ancestor variables). When moving out of a subtree, those variables are "popped".
2. **Global Attention (Historical Context)**: You have access to a summary of ALL completed nodes across the entire tree. Each record includes a node's Index, Name, and Result. Use this to pick relevant information from any previously finished branch.

## Your Task

Based on the current node state, request, Scoped Variables, and Global Attention, you need to:
1. **Analyze** the current situation.
2. **Leaf Node Definition**: A Leaf Node is a task small enough that its context and results fit perfectly within your optimal context window. If a task is too large, decompose it.
3. **Decide** what actions to take:
   - create_node: Break down task.
   - mark_complete: Finish CURRENT node. Provide a result string to summarize the outcome for Global Attention.
   - update_variables: Store temporary state.
   - execute_command: Run system commands (ls, cat, write, rm) to interact with the host.
4. **Respond** in JSON format.

## Response Format

Respond with valid JSON:
{
  "actions": [
    {
      "action_type": "create_node",
      "node": {
        "id": "node_id",
        "name": "Node Name",
        "type": "Normal|Loop|Leaf",
        "information": "Description",
        "variables": {"key": "value"}
      }
    },
    {
      "action_type": "mark_complete",
      "result": "Detailed result of the task"
    },
    {
      "action_type": "execute_command",
      "command": "ls ."
    }
  ]
}

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

	client := &http.Client{}
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
