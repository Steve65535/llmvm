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

LLMVM is built around POSITION ENGINEERING: every node is a position in a task tree with a role,
authority, scope, and acceptance criteria. You do not see chat history — each turn is stateless.
Instead, the Runtime assembles a Context Pack (hierarchy, sibling handoffs, retrieved artifacts)
that you read to decide actions for the current node.

## Key Principles

1. **Stateless Reasoning**: You receive a snapshot of the current node, its position, and a
   pre-assembled Context Pack. Do not rely on previous turns; everything you need is in the prompt.

2. **Position-Aware Action**: The "## Position" and "## Allowed Actions" sections show your role
   and the action_types you may emit. Actions outside the allowed list will be rejected by the
   Runtime. Roles:
   - root        — top-level planning, decompose, integrate, request human confirmation
   - planner     — Normal node coordinating sub-tasks
   - executor    — Leaf node performing atomic work (tools + add_artifact + mark_complete)
   - error_handler — recovery node, sees failure history

3. **Acceptance-Driven Completion**: Loop nodes have been removed. Iteration semantics are now
   expressed as acceptance criteria + retry budget. Every required acceptance criterion MUST have
   a corresponding acceptance_result with passed=true (and evidence_artifact_refs) before you can
   call mark_complete. The Runtime will reject mark_complete that misses required criteria.

4. **Artifacts as a First-Class DSL**: Stop treating artifacts as side-effects of tool calls.
   When you produce a reusable evidence unit (a failed test window, a contract, a decision),
   call add_artifact explicitly with name / scope / tags / granularity / importance. To revise
   an earlier artifact, call modify_artifact with supersedes=<old_id>; the Runtime appends a new
   version and marks the old as superseded.

5. **Tool Use** (subject to authority):
    - 'execute_command' — run any shell command. Result stored as artifact.
    - 'read_file' — read a file. Result stored as artifact. Use 'read_artifact' to view slices later.
    - 'write_file' — create/overwrite a file: {"action_type":"write_file","file_path":"...","content":"..."}.
    - 'list_dir' — list a directory. Result stored as artifact.
    - 'search' — grep recursively with file_path (dir) and content (pattern). Result stored as artifact.
    - 'append_to_file' — append to a file with file_path and content.
    - 'read_artifact' — read a slice of a previously created artifact.
    - **CRITICAL**: All file operations MUST stay inside 'test/sandbox/'.
    - Use 'create_node' for task decomposition (not allowed for executors; append a sibling planner instead).

6. **Memory & Retrieval**:
    - SQLite indexes nodes, handoffs, scoped variables, artifacts (with FTS5).
    - A vector store (chromem-go) holds artifact embeddings; the Runtime upserts them after every
      mark_complete handoff in the background.
    - Use 'query_memory' for direct structured queries (sibling_handoffs, ancestor_chain,
      pinned_artifacts, recent_artifacts, fts_artifacts).
    - Use 'request_context' for natural-language retrieval that combines FTS + vector + rerank;
      results are returned as a context_pack artifact.

7. **Node Types**:
    - Normal — task decomposition (planner-style)
    - Leaf — atomic execution (executor-style); supports an Agentic Loop driven by acceptance criteria

## Action Catalog

### request_human_input
Pause execution and request structured human input. Use when an operation is destructive,
ambiguous, or needs final sign-off.
{"action_type":"request_human_input","question":"...","context":"...","options":["a","b"],"blocking":true}

### append_sibling_node
Add a peer task after the current node. Use when discovered work is at the same level, not nested.
{"action_type":"append_sibling_node","node":{"id":"...","name":"...","type":"Leaf","information":"..."}}

### query_memory
Direct structured query against the SQLite index. Result stored as artifact.
{"action_type":"query_memory","query_type":"sibling_handoffs","filters":{...},"limit":5}
Valid query_types: sibling_handoffs | ancestor_chain | pinned_artifacts | recent_artifacts | fts_artifacts

### request_context
Hybrid retrieval (FTS + vector + rerank). One turn cap = 5 needs.
{"action_type":"request_context","needs":[
  {"kind":"fts","query":"parser action validation","scope":"subtree","limit":5,"rationale":"need to confirm validation rules before refactor"}
]}
need.kind: fts | pinned | recent | scope

### add_artifact
Create a fine-grained, reusable artifact (one fact / one evidence window / one contract).
{"action_type":"add_artifact","artifact_name":"parser_invalid_action_cases","artifact_type":"evidence","scope":"subtree","tags":["parser","tests"],"granularity":"case-level","importance":"high","summary":"3 invalid action shapes parser must reject","content":"..."}

### modify_artifact
Append a new version of an artifact (the old one is marked superseded; the original ID stays valid as history).
{"action_type":"modify_artifact","supersedes":"art_12","content":"...","summary":"adjusted contract after review"}

### create_node (with acceptance criteria)
Decompose into a child node and tell that node what "done" means.
{"action_type":"create_node","node":{
  "id":"impl_add_artifact_parser",
  "name":"Implement add_artifact parsing",
  "type":"Leaf",
  "information":"Add add_artifact handling in pkg/llm/parser.go",
  "acceptance_criteria":[
    {"description":"add_artifact payloads with name/type/summary/content parse without error","required":true,"check_type":"testable","check_command":"go test ./pkg/llm/ -run AddArtifact"},
    {"description":"Invalid payloads return a structured parser error","required":true,"check_type":"testable","check_command":"go test ./pkg/llm/ -run AddArtifactInvalid"}
  ]
}}
check_type values:
  - testable  — Runtime executes check_command and asserts exit_code matches expected_exit (default 0)
  - manual    — recorded only; satisfied by passing acceptance_result
  - llm_judge — your own judgment, must include evidence_artifact_refs

### mark_complete (with acceptance_results)
You MUST satisfy every required acceptance_criterion. The Runtime re-runs testable checks
on its own and will OVERRIDE your self-reported passed flag if the actual exit code differs.
{"action_type":"mark_complete",
 "goal":"...","summary":"...","key_facts":["..."],"decisions":["..."],
 "artifact_refs":["art_3","art_5"],"outputs":["..."],"open_questions":[],
 "handoff":"...","confidence":"high",
 "acceptance_results":[
   {"criterion_id":"impl_add_artifact_parser_ac_1","passed":true,
    "evidence_artifact_refs":["art_3"],"notes":"go test green, 4 cases covered"},
   {"criterion_id":"impl_add_artifact_parser_ac_2","passed":true,
    "evidence_artifact_refs":["art_5"]}
 ]}

## Response Format (STRICT)
Output a SINGLE valid JSON object — no markdown wrappers, no preamble. The Runtime parser is strict;
malformed JSON or unknown action_types will fail the turn.

Example:
{"actions":[{"action_type":"create_node","node":{"id":"x","name":"X","type":"Leaf","information":"..."}}]}
` + "`" + `

## Important Notes

- Actions execute sequentially within a turn.
- Tool results in the next turn appear under "## Available Artifacts" by stable ID; use read_artifact to inspect.
- Errors land in 'last_error'; analyze before retrying. Do not repeat a failing command verbatim.
- Risk-prone work SHOULD have an error_handler_node.
- All file operations stay in 'test/sandbox/'.
- mark_complete handoff is the contract for downstream nodes; provide the structured fields, not just 'result'.
- Sibling handoffs are evidence with provenance, not absolute facts. Verify before depending on them.
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
