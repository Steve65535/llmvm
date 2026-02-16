package runtime

import (
	// Added for os/exec output capture
	"encoding/json"
	"fmt"     // Added as per instruction
	"os"      // Added as per instruction
	"os/exec" // Added as per instruction
	"path/filepath"
	"strings"
	"time"

	"github.com/Steve65535/llmvm/pkg/cursor"
	"github.com/Steve65535/llmvm/pkg/llm"
	"github.com/Steve65535/llmvm/pkg/tasknode"
	"github.com/Steve65535/llmvm/pkg/vfs"
)

const (
	MaxCommandResultLength = 4000
	MaxHistoryEntryLength  = 1000
	MaxVariableDumpLength  = 8000
)

// Runtime 是图灵完备的 LLM 运行时引擎
type Runtime struct {
	engine      llm.Engine
	cursor      *cursor.Cursor
	initialReq  string
	nodeCounter int // 全局节点计数器
	vfs         *vfs.VirtualFileSystem

	// OnStepComplete is called after each node execution step
	OnStepComplete func(*tasknode.TaskNode)
}

// NewRuntime 创建新的运行时实例
func NewRuntime(engine llm.Engine, root *tasknode.TaskNode) *Runtime {
	vfsInstance := vfs.New(".") // 默认当前目录
	return &Runtime{
		engine:      engine,
		cursor:      cursor.New(root),
		nodeCounter: 0,
		vfs:         vfsInstance,
	}
}

// Execute 执行深度优先搜索，构建语法树
func (r *Runtime) Execute(initialRequest string) error {
	// 初始化根节点
	if r.cursor.Current == nil {
		return fmt.Errorf("root node is nil")
	}

	// 标记根节点为已遍历（由主循环负责，此处不再提前标记）
	// if !r.cursor.Current.WetherTraveled {
	// 	r.cursor.Current.MarkTraveled()
	// }

	// 主循环：深度优先搜索
	maxIterations := 1000 // 防止无限循环
	iteration := 0

	for !r.cursor.Done() {
		iteration++
		if iteration > maxIterations {
			return fmt.Errorf("maximum iterations (%d) reached, possible infinite loop", maxIterations)
		}

		current := r.cursor.Current
		if current == nil {
			break
		}

		// 📍 IMPROVEMENT(v10/v11): Skip LLM call if already traveled
		// Normal and Loop nodes only need LLM calls for their initial turn (to decompose/decide).
		// Once traveled, they use decideNextStep to manage their children.
		if current.WetherTraveled && current.Type != tasknode.Leaf {
			// Special case: For Normal nodes, if all children are traveled, we move up.
			// But first, we ensure it has a chance to decide its own Finished status if it has no children.
			if current.Type == tasknode.Normal && current.AllChildrenTraveled() {
				fmt.Printf("📍 Step %d: Normal node [%s] %s - All children traveled, moving up\n", iteration, current.ID, current.Name)
				if err := r.decideNextStep(current); err != nil {
					return fmt.Errorf("failed to decide next step: %w", err)
				}
				continue
			}

			fmt.Printf("📍 Step %d: Skipping LLM call for traveled node [%s] %s\n", iteration, current.ID, current.Name)
			if err := r.decideNextStep(current); err != nil {
				return fmt.Errorf("failed to decide next step: %w", err)
			}
			continue
		}

		// 调试输出
		fmt.Printf("📍 Step %d: Processing node [%s] %s (Type: %v, Traveled: %v, Finished: %v)\n",
			iteration, current.ID, current.Name, current.Type, current.WetherTraveled, current.WetherFinished)

		// --- PASS 1: Attention Selection ---
		// ... (rest of the prompt building and LLM call logic) ...
		selectedNodeIDs := []string{}
		if iteration > 0 {
			ids, err := r.selectAttentionNodes(current, initialRequest)
			if err != nil {
				fmt.Printf("  ⚠️ Attention selection failed (skipping): %v\n", err)
			} else {
				selectedNodeIDs = ids
			}
		}

		// ... (Main LLM Call and Action Execution) ...
		var response *llm.Response
		maxRetries := 9
		var lastErr error
		retryCount := 0

		for retryCount <= maxRetries {
			// ... (Prompt building, LLM Call, Parsing, Action Execution) ...
			prompt, err := r.buildPromptWithWorkspace(current, initialRequest, selectedNodeIDs, lastErr)
			if err != nil {
				return fmt.Errorf("failed to build prompt: %w", err)
			}

			if current.Index == -1 {
				r.nodeCounter++
				current.Index = r.nodeCounter
			}

			fmt.Printf("  🤖 Calling LLM (Attempt %d)...\n", retryCount+1)

			// 统计 Token 使用情况
			const DeepSeekContextLimit = 64000 // DeepSeek 的上下文窗口
			r.printTokenStats(prompt, DeepSeekContextLimit)

			output, err := r.engine.Call(prompt)
			if err != nil {
				lastErr = fmt.Errorf("LLM call failed: %w", err)
				retryCount++
				continue
			}

			response, lastErr = llm.ParseResponse(output.Response)
			if lastErr != nil {
				retryCount++
				continue
			}

			actionErr := false
			for _, action := range response.Actions {
				if err := r.executeAction(action, current); err != nil {
					if strings.Contains(err.Error(), "EMERGENCY_SHUTDOWN") {
						return err
					}
					// 🔧 FIX: 错误处理与重试逻辑
					lastErr = fmt.Errorf("failed to execute action: %w", err)
					if r.handleError(current, lastErr) {
						// 错误已被处理（重定向到 Handler），跳过后续 Action
						actionErr = false
						break
					}
					actionErr = true
					break
				}
			}

			if actionErr {
				current.RetryCount++
				if current.RetryCount > current.MaxRetries {
					return fmt.Errorf("maximum retries (%d) reached for node [%s]: %w", current.MaxRetries, current.ID, lastErr)
				}
				continue
			}

			fmt.Printf("  ✅ Step processed successfully: %d action(s)\n", len(response.Actions))
			if !current.WetherTraveled {
				current.MarkTraveled()
			}
			// 重置重试计数
			current.RetryCount = 0
			break
		}

		// 🆕 执行回调（用于持久化等）
		if r.OnStepComplete != nil {
			r.OnStepComplete(current)
		}

		// 5. 决定下一步：下树还是上树
		if err := r.decideNextStep(current); err != nil {
			return fmt.Errorf("failed to decide next step: %w", err)
		}
		fmt.Println()
	}
	return nil
}

// buildPrompt 构建 stateless prompt（不包含历史上下文）
func (r *Runtime) buildPrompt(current *tasknode.TaskNode, request string, retryError error) (string, error) {
	return r.buildPromptInternal(current, request, nil, retryError)
}

// buildPromptWithWorkspace 是 buildPrompt 的包装，支持传入 selectedNodeIDs
func (r *Runtime) buildPromptWithWorkspace(current *tasknode.TaskNode, request string, selectedNodeIDs []string, retryError error) (string, error) {
	return r.buildPromptInternal(current, request, selectedNodeIDs, retryError)
}

// selectAttentionNodes 发起第一次对话，让 LLM 挑选有用的节点
func (r *Runtime) selectAttentionNodes(current *tasknode.TaskNode, request string) ([]string, error) {
	root := r.cursor.GetRoot()
	if root == nil {
		return nil, nil
	}

	// 1. 生成精简的全树索引
	treeIndex := r.getTreeIndex(root, 0)

	// 2. 构建筛选 Prompt
	selectorPrompt := fmt.Sprintf(`You are an Attention Selector for LLMVM.

## Task Tree Index (Compact)
%s

## Current Target Node
- ID: %s
- Name: %s
- Goal: %s

## Your Task
Scan the Task Tree Index and select nodes that contain information relevant to the current goal.

**Selection criteria**:
- Nodes with variables that the current task might need (e.g., file paths, computed values)
- Nodes with results that provide context (e.g., "file exists", "data loaded")
- Recent nodes (higher index) are often more relevant
- Nodes in the same branch or parent chain

**Output format**: Comma-separated Node IDs (e.g., "node_1, node_5, create_dir")
- If no nodes are relevant: return "none"
- Limit to 5-10 most relevant nodes (avoid selecting too many)

Example: "node_1, node_5, create_dir"`, treeIndex, current.ID, current.Name, request)

	fmt.Printf("  🧠 Selecting relevant nodes from Global Tree...\n")
	output, err := r.engine.Call(selectorPrompt)
	if err != nil {
		return nil, err
	}

	raw := strings.TrimSpace(output.Response)
	if strings.ToLower(raw) == "none" || raw == "" {
		return nil, nil
	}

	// 解析 ID 列表
	ids := strings.Split(raw, ",")
	for i, id := range ids {
		ids[i] = strings.TrimSpace(id)
	}
	return ids, nil
}

// getTreeIndex 递归生成紧凑的树索引
func (r *Runtime) getTreeIndex(node *tasknode.TaskNode, indent int) string {
	line := fmt.Sprintf("%s- [%s] %s (Status: %s)\n", strings.Repeat("  ", indent), node.ID, node.Name, nodeStatusStr(node.Status))
	for _, child := range node.Children {
		line += r.getTreeIndex(child, indent+1)
	}
	return line
}

// formatGlobalWorkspace 根据选中的 ID 组合详细的 RAM 快照
func (r *Runtime) formatGlobalWorkspace(nodeIDs []string) string {
	if len(nodeIDs) == 0 {
		return "No nodes selected for global workspace."
	}

	// 递归查找并提取节点信息
	var sb strings.Builder
	sb.WriteString("Useful information picked from selected nodes:\n")

	root := r.cursor.GetRoot()
	foundAny := false

	for _, id := range nodeIDs {
		node := r.findNodeByID(root, id)
		if node != nil {
			foundAny = true
			sb.WriteString(fmt.Sprintf("- [%s] %s:\n", node.ID, node.Name))
			if node.Result != "" {
				resultStr := node.Result
				if len(resultStr) > 1000 {
					resultStr = resultStr[:1000] + " ... [TRUNCATED]"
				}
				sb.WriteString(fmt.Sprintf("  Result: %s\n", resultStr))
			}
			if len(node.Variables) > 0 {
				varsJSON, _ := json.Marshal(node.Variables)
				varsStr := string(varsJSON)
				if len(varsStr) > 2000 {
					varsStr = varsStr[:2000] + " ... [TRUNCATED]"
				}
				sb.WriteString(fmt.Sprintf("  Variables: %s\n", varsStr))
			}
		}
	}

	if !foundAny {
		return "No historical context available yet (no matching nodes found)."
	}
	return sb.String()
}

// findNodeByID 递归查找节点
func (r *Runtime) findNodeByID(root *tasknode.TaskNode, id string) *tasknode.TaskNode {
	if root.ID == id {
		return root
	}
	for _, child := range root.Children {
		found := r.findNodeByID(child, id)
		if found != nil {
			return found
		}
	}
	return nil
}

func (r *Runtime) buildPromptInternal(current *tasknode.TaskNode, request string, selectedNodeIDs []string, retryError error) (string, error) {
	// 构建任务路径
	taskPath := r.cursor.GetPath()

	// 构建父节点信息
	parentInfo := llm.NodeState{
		ID:          "none",
		Name:        "none",
		Type:        "none",
		Status:      "none",
		Information: "none",
	}
	if current.Parent != nil {
		parentInfo = r.nodeToState(current.Parent)
	}

	// 构建当前节点信息
	currentInfo := r.nodeToState(current)

	// 获取当前节点的子节点状态信息
	childrenInfo := r.getChildrenInfo(current)

	// 获取当前是否在 Loop 中
	isInLoop := r.cursor.IsInLoop()
	currentLoop := r.cursor.GetCurrentLoop()
	loopInfo := ""
	if isInLoop && currentLoop != nil {
		loopInfo = fmt.Sprintf(`Currently inside Loop node: "%s" (ID: %s)
- All children finished: %v
- **To end this loop**: Mark the child node as 'finished' when the loop condition is met
- **Loop variables**: Check the scoped variables below for loop counters (e.g., current_index, iteration_count)
- **Important**: If you want to continue iterating, do NOT mark children as finished`,
			currentLoop.Name, currentLoop.ID, currentLoop.AllChildrenFinished())
	} else {
		loopInfo = "Not currently in a Loop"
	}

	// 构建请求结构
	req := llm.Request{
		TaskPath:    taskPath,
		ParentInfo:  parentInfo,
		CurrentInfo: currentInfo,
		Request:     request,
	}

	// 收集从根到当前的 Scoped Variables
	scopedVariables := r.collectScopedVariables(current)
	varsStr := formatVariables(scopedVariables)

	// Global Workspace (Stateless, based on selection)
	workspaceStr := r.formatGlobalWorkspace(selectedNodeIDs)

	// 转换为 JSON（用于结构化数据展示）
	jsonData, err := json.MarshalIndent(req, "", "    ")
	if err != nil {
		return "", err
	}

	// 如果有重试错误信息，添加到 prompt 中
	errorContext := ""
	if retryError != nil {
		errorContext = fmt.Sprintf(`
> [!IMPORTANT]
> **Previous Attempt Failed**:
> Error: %v
>
> **Common causes**:
> - Invalid JSON format (missing quotes, trailing commas, unescaped characters)
> - Wrong action_type (must be exactly: create_node, mark_complete, update_variables, execute_command, shutdown)
> - Missing required fields (e.g., node.id, node.name, node.type for create_node)
> - Invalid node.type (must be exactly: Normal, Loop, or Leaf)
>
> **Please**:
> 1. Fix the JSON syntax error carefully
> 2. Verify all required fields are present
> 3. Double-check action_type and node.type spelling (case-sensitive)
> 4. Ensure all strings are in double quotes
`, retryError)
	}

	// 构建完整的 prompt
	prompt := fmt.Sprintf(`## Current Context

**Task Path**: %s

**Current Node**:
- ID: %s
- Name: %s
- Type: %s
- Status: %s
- Index: %d
- WetherTraveled: %v
- WetherFinished: %v
- Information: %s

**Parent Node**:
- ID: %s
- Name: %s
- Type: %s
- Status: %s

**Children Status**:
%s

**Loop Context**:
%s

## Global Workspace (Ephemeral RAM)
%s

## Structured Request Data

%s

## Scoped Variables (Current Path Context)

%s
%s

## Request

%s

## Your Task

Please respond with valid JSON in the required format.

## Node Type Guidelines

**When to create each type**:
- **Normal**: For tasks that can be decomposed into sequential sub-tasks
  - Example: "Build web app" → [Setup, Frontend, Backend, Deploy]
  
- **Loop**: For tasks that need iteration until a condition is met
  - Example: "Verify all even numbers 4-1000" → Loop with condition check
  - **Critical**: You MUST mark child nodes as 'finished' when the loop should end
  
- **Leaf**: For atomic tasks that can be completed in one step
  - Example: "Read file.txt", "Calculate sum", "Print result"
  - Should NOT have child nodes

**Current node type**: %s
- If Normal and has no children yet: Consider decomposing into sub-tasks
- If Loop and children not finished: Continue iteration or mark finished to end loop
- If Leaf: Execute the task directly using commands or mark_complete

## Execution Requirements (STRICT):

1. **Physical Persistence check**: If you see a file mentioned in a previous node's 'Result', **DO NOT** assume it exists physically. You MUST use 'ls' to verify its existence before attempting to 'cat' it.

2. **Persistence**: To save results for future steps beyond semantic memory, you **MUST** use 'execute_command' with 'write'.

3. **Decomposition**: If the current node is a Leaf node, process it now. If it requires multiple steps or complex logic, you SHOULD create child nodes first.

4. **Tool Use**: Use 'pwd' to see current directory (always /). Use 'ls' to explore.

5. **Variable Naming**: 
   - Use descriptive names that include context (e.g., 'outer_loop_counter', 'file_processing_index')
   - Avoid generic names like 'i', 'temp', 'data' in nested structures
   - If you're in a Loop, prefix loop-specific variables with the loop's purpose (e.g., 'goldbach_current_even')

6. **Incremental File Writing**:
   - Use 'append_to_file' for building documents incrementally
   - Each node can append its own section without rewriting the entire file
   - This is more efficient and less error-prone than rewriting
   - Example: Building a report where each node adds a section

7. **CRITICAL: STRUCTURAL INTEGRITY & ERROR HANDLING**:
   - **JSON Format**: You MUST return a single valid JSON. Any extra text or markdown will cause system failure.
   - **Error Handling**: For risky operations (I/O, network, complex calculations), you MUST provide an `+"`"+`error_handler_node`+"`"+` in your `+"`"+`create_node`+"`"+` action. Failure to do so will result in cascading failures and task termination.
   - **Completeness**: Every path in your tree MUST end with a `+"`"+`mark_complete`+"`"+` action.

## Response Format Examples

**Example 1: Create child nodes**
`+"```json"+`
{
  "actions": [
    {
      "action_type": "create_node",
      "node": {
        "id": "read_data",
        "name": "Read Data File",
        "type": "Leaf",
        "information": "Read data.csv and parse"
      }
    }
  ]
}
`+"```"+`

**Example 2: Mark current node complete**
`+"```json"+`
{
  "actions": [
    {
      "action_type": "mark_complete",
      "result": "Task completed successfully",
      "is_important": true
    }
  ]
}
`+"```"+`

**Example 3: Execute command**
`+"```json"+`
{
  "actions": [
    {
      "action_type": "execute_command",
      "command": "ls -la"
    }
  ]
}
`+"```"+`

**Example 4: Append to file (incremental write)**
`+"```json"+`
{
  "actions": [
    {
      "action_type": "append_to_file",
      "file_path": "/absolute/path/to/document.md",
      "content": "\n### A Midsummer Night's Dream\n\n**Written**: 1595-1596\n**Plot**: Four lovers and fairies in a magical forest.\n"
    }
  ]
}
`+"```"+`

**Use append_to_file when**:
- Building documents incrementally (reports, logs, analysis)
- Writing content with special characters (quotes, apostrophes) - NO shell escaping needed!
- Avoiding rewriting entire files (saves tokens and prevents errors)

**Critical**: 
- All string values must be in double quotes
- No trailing commas
- action_type must be exact (case-sensitive)
- node.type must be exactly: Normal, Loop, or Leaf
- **IMPORTANT**: Prefer append_to_file over echo commands to avoid shell escaping issues!
`,
		formatPath(taskPath),
		current.ID, current.Name, nodeTypeStr(current.Type), nodeStatusStr(current.Status), current.Index,
		current.WetherTraveled, current.WetherFinished, strings.Join(current.Information, "\n"),
		parentInfo.ID, parentInfo.Name, parentInfo.Type, parentInfo.Status,
		childrenInfo,
		loopInfo,
		workspaceStr,
		string(jsonData),
		varsStr,
		errorContext,
		request,
		nodeTypeStr(current.Type))

	return prompt, nil
}

// getChildrenInfo 获取子节点的状态信息
func (r *Runtime) getChildrenInfo(node *tasknode.TaskNode) string {
	if len(node.Children) == 0 {
		return "No children nodes yet."
	}

	info := fmt.Sprintf("Total children: %d\n", len(node.Children))
	allTraveled := true
	allFinished := true

	for i, child := range node.Children {
		traveled := "No"
		finished := "No"
		if child.WetherTraveled {
			traveled = "Yes"
		} else {
			allTraveled = false
		}
		if child.WetherFinished {
			finished = "Yes"
		} else {
			allFinished = false
		}

		childType := "Normal"
		switch child.Type {
		case tasknode.Loop:
			childType = "Loop"
		case tasknode.Leaf:
			childType = "Leaf"
		}

		// 添加状态说明
		statusNote := ""
		if child.WetherTraveled && child.WetherFinished {
			statusNote = " → This child has been executed and completed"
		} else if child.WetherTraveled && !child.WetherFinished {
			statusNote = " → This child has been visited but not fully completed (may have unfinished sub-tasks)"
		} else {
			statusNote = " → This child has not been executed yet"
		}

		info += fmt.Sprintf("  %d. [%s] %s (ID: %s) - Traveled: %s, Finished: %s%s\n",
			i+1, childType, child.Name, child.ID, traveled, finished, statusNote)
	}

	// 添加状态含义说明
	info += "\n**Status meanings**:\n"
	info += "- Traveled: Whether this node has been visited by the execution cursor\n"
	info += "- Finished: Whether this node has completed its task (for Loop nodes, this controls iteration)\n\n"

	if allTraveled {
		info += "All children have been traveled: Yes\n"
	} else {
		info += "All children have been traveled: No\n"
	}
	if allFinished {
		info += "All children have been finished: Yes"
	} else {
		info += "All children have been finished: No"
	}

	return info
}

// formatPath 格式化路径为可读字符串
func formatPath(path []string) string {
	if len(path) == 0 {
		return "root"
	}
	result := ""
	for i, p := range path {
		if i > 0 {
			result += " -> "
		}
		result += p
	}
	return result
}

// nodeToState 将 TaskNode 转换为 NodeState
func (r *Runtime) nodeToState(node *tasknode.TaskNode) llm.NodeState {
	status := "Pending"
	switch node.Status {
	case tasknode.Running:
		status = "Running"
	case tasknode.Completed:
		status = "Completed"
	case tasknode.Failed:
		status = "Failed"
	}

	nodeType := "Normal"
	switch node.Type {
	case tasknode.Loop:
		nodeType = "Loop"
	case tasknode.Leaf:
		nodeType = "Leaf"
	}

	information := ""
	if len(node.Information) > 0 {
		information = node.Information[0]
	}

	return llm.NodeState{
		ID:          node.ID,
		Name:        node.Name,
		Type:        nodeType,
		Status:      status,
		Information: information,
		Variables:   node.Variables,
		Index:       node.Index,
		Result:      node.Result,
		IsImportant: node.IsImportant,
	}
}

// collectScopedVariables 收集从根节点到当前路径的所有变量（越靠近当前节点优先级越高）
func (r *Runtime) collectScopedVariables(current *tasknode.TaskNode) map[string]interface{} {
	vars := make(map[string]interface{})
	path := []*tasknode.TaskNode{}
	node := current
	for node != nil {
		path = append([]*tasknode.TaskNode{node}, path...)
		node = node.Parent
	}

	for _, n := range path {
		for k, v := range n.Variables {
			vars[k] = v
		}
	}
	return vars
}

// formatVariables 格式化变量为可读字符串
func formatVariables(vars map[string]interface{}) string {
	if len(vars) == 0 {
		return "No scoped variables."
	}

	var sb strings.Builder

	// Print Command History first if available
	if hist, ok := vars["command_output_history"]; ok {
		sb.WriteString("### Command Execution History (Last 20):\n")
		// Handle []string vs []interface{}
		var history []string
		if casted, ok := hist.([]string); ok {
			history = casted
		} else if castedInterface, ok := hist.([]interface{}); ok {
			for _, item := range castedInterface {
				if str, ok := item.(string); ok {
					history = append(history, str)
				}
			}
		}

		for _, entry := range history {
			sb.WriteString(entry + "\n\n")
		}

		// Remove it from the map copy to avoid duplication in JSON dump
		// NOTE: scopedVariables is a copy in collectScopedVariables, so modifying it here is safe-ish,
		// but collectScopedVariables creates a fresh map for us.
		delete(vars, "command_output_history")
	}

	if len(vars) > 0 {
		data, _ := json.MarshalIndent(vars, "", "  ")
		varsStr := string(data)
		if len(varsStr) > MaxVariableDumpLength {
			varsStr = varsStr[:MaxVariableDumpLength] + "\n... [TRUNCATED DUE TO SIZE]"
		}
		sb.WriteString("\n### Other Variables:\n")
		sb.WriteString(varsStr)
	}

	return sb.String()
}

// executeAction 执行 LLM 返回的动作
func (r *Runtime) executeAction(action llm.Action, parent *tasknode.TaskNode) error {
	switch action.ActionType {
	case "create_node":
		// 创建新节点
		childNode := action.Node.ToTaskNode()
		parent.AddChild(childNode)

		// 🆕 如果指定了错误处理节点
		if action.ErrorHandlerNode.ID != "" {
			errorHandler := action.ErrorHandlerNode.ToTaskNode()
			childNode.ErrorHandler = errorHandler
		}
		return nil
	case "mark_complete":
		// 标记当前节点为完成
		parent.MarkFinished()
		if action.Result != "" {
			parent.Result = action.Result
		}
		if action.IsImportant {
			parent.IsImportant = true
		}
		return nil
	case "update_variables":
		// 更新当前节点的变量
		if action.Variables != nil {
			if parent.Variables == nil {
				parent.Variables = make(map[string]interface{})
			}
			for k, v := range action.Variables {
				parent.Variables[k] = v
			}
		}
		if action.Result != "" {
			parent.Result = action.Result
		}
		if action.IsImportant {
			parent.IsImportant = true
		}
		return nil
	case "execute_command":
		fmt.Printf("💻 Executing command: %s\n", action.Command)
		result, err := r.handleCLI(action.Command)
		if err != nil {
			return fmt.Errorf("command execution failed: %w", err)
		}
		fmt.Printf("📝 Command result: %s\n", result)
		// Truncate result for last_command_result
		storageResult := result
		if len(storageResult) > MaxCommandResultLength {
			storageResult = storageResult[:MaxCommandResultLength] + "\n... [TRUNCATED]"
		}
		parent.Variables["last_command_result"] = storageResult

		// Append to Command History
		// Truncate entry for history to avoid context bloating
		if len(result) > MaxHistoryEntryLength {
			result = result[:MaxHistoryEntryLength] + "\n... [TRUNCATED]"
		}
		histEntry := fmt.Sprintf("[%s] $ %s\n> %s", time.Now().Format("15:04:05"), action.Command, result)
		var history []string
		if existing, ok := parent.Variables["command_output_history"]; ok {
			if casted, ok := existing.([]string); ok {
				history = casted
			} else if castedInterface, ok := existing.([]interface{}); ok {
				// Handle JSON unmarshaled slice
				for _, item := range castedInterface {
					if str, ok := item.(string); ok {
						history = append(history, str)
					}
				}
			}
		}
		history = append(history, histEntry)
		// Keep last 20 commands to avoid context overflow
		if len(history) > 20 {
			history = history[len(history)-20:]
		}
		parent.Variables["command_output_history"] = history
		return nil
	case "append_to_file":
		fmt.Printf("📝 Appending to file: %s\n", action.FilePath)

		// 读取现有内容（如果文件存在）
		existingContent := ""
		if data, err := os.ReadFile(action.FilePath); err == nil {
			existingContent = string(data)
		}

		// 追加新内容
		newContent := existingContent + action.Content

		// 确保目录存在
		dir := filepath.Dir(action.FilePath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}

		// 写入文件
		if err := os.WriteFile(action.FilePath, []byte(newContent), 0644); err != nil {
			return fmt.Errorf("failed to append to file %s: %w", action.FilePath, err)
		}

		// 保存结果到变量
		if parent.Variables == nil {
			parent.Variables = make(map[string]interface{})
		}
		parent.Variables["last_file_written"] = action.FilePath
		parent.Variables["last_file_size"] = len(newContent)

		fmt.Printf("✅ Successfully appended %d bytes to %s (total: %d bytes)\n",
			len(action.Content), action.FilePath, len(newContent))

		return nil
	case "shutdown":
		return fmt.Errorf("EMERGENCY_SHUTDOWN: %s", action.Result)
	default:
		return fmt.Errorf("unknown action type: %s", action.ActionType)
	}
}

// NodeResult 辅助结构用于排序
type NodeResult struct {
	Index       int
	Name        string
	Result      string
	Variables   map[string]interface{}
	IsImportant bool
}

// collectGlobalAttention 递归扫描整棵树，提取所有“有用”的节点信息（重要的或有结果的）
func (r *Runtime) collectGlobalAttention(node *tasknode.TaskNode) []NodeResult {
	var results []NodeResult
	// 挑选逻辑：显示被标记为 Important 的节点，或者至少有 Result 的节点
	if node.IsImportant || node.Result != "" {
		results = append(results, NodeResult{
			Index:       node.Index,
			Name:        node.Name,
			Result:      node.Result,
			Variables:   node.Variables,
			IsImportant: node.IsImportant,
		})
	}
	for _, child := range node.Children {
		results = append(results, r.collectGlobalAttention(child)...)
	}
	return results
}

func (r *Runtime) formatHistory() string {
	// 从根节点开始全量扫描
	root := r.cursor.GetRoot()
	if root == nil {
		return "No global workspace context available."
	}

	allResults := r.collectGlobalAttention(root)
	if len(allResults) == 0 {
		return "No global workspace entries available yet."
	}

	// 1. 优先级排序：Important 节点排在前面，然后按 Index 倒序
	// 这是一个启发式的滑动窗口：如果内容太多，优先保留 Important 的
	for i := 0; i < len(allResults); i++ {
		for j := i + 1; j < len(allResults); j++ {
			// 排序规则：Important 优先，同级按 Index 降序
			iImportance := 0
			if allResults[i].IsImportant {
				iImportance = 1
			}
			jImportance := 0
			if allResults[j].IsImportant {
				jImportance = 1
			}

			if iImportance < jImportance || (iImportance == jImportance && allResults[i].Index < allResults[j].Index) {
				allResults[i], allResults[j] = allResults[j], allResults[i]
			}
		}
	}

	// 2. 限制窗口大小（例如显示最近 10 个最有用的节点）
	const maxWindow = 10
	limit := len(allResults)
	if limit > maxWindow {
		limit = maxWindow
	}

	window := allResults[:limit]
	// 再按 Index 正序排回来显示
	for i := 0; i < len(window); i++ {
		for j := i + 1; j < len(window); j++ {
			if window[i].Index > window[j].Index {
				window[i], window[j] = window[j], window[i]
			}
		}
	}

	var sb strings.Builder
	sb.WriteString("Global Workspace (RAM - Key findings and important variables picked from all nodes):\n")
	for _, res := range window {
		impTag := ""
		if res.IsImportant {
			impTag = " [PINNED]"
		}
		sb.WriteString(fmt.Sprintf("- [%d] %s%s:\n", res.Index, res.Name, impTag))
		if res.Result != "" {
			sb.WriteString(fmt.Sprintf("  Result: %s\n", res.Result))
		}
		if len(res.Variables) > 0 {
			varsJSON, _ := json.Marshal(res.Variables)
			sb.WriteString(fmt.Sprintf("  Variables: %s\n", string(varsJSON)))
		}
	}
	return sb.String()
}

func (r *Runtime) handleCLI(command string) (string, error) {
	if command == "" {
		return "", fmt.Errorf("empty command")
	}

	// Use sh -c to allow piping, redirection, and other shell features
	// On Windows, this would be cmd /c or powershell -c
	cmd := exec.Command("sh", "-c", command)

	// Set work dir to current dir if possible
	dir, _ := os.Getwd()
	cmd.Dir = dir

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	combined := stdout.String()
	if stderr.Len() > 0 {
		if combined != "" {
			combined += "\n"
		}
		combined += "STDERR: " + stderr.String()
	}

	if err != nil {
		return combined, fmt.Errorf("command execution failed: %w (output: %s)", err, combined)
	}

	if combined == "" {
		return "Command executed successfully (no output).", nil
	}

	return combined, nil
}

// estimateTokenCount 估算文本的 Token 数量
// 简单规则：英文约 4 字符 = 1 token，中文约 1.5 字符 = 1 token
func estimateTokenCount(text string) int {
	// 统计中文字符数量
	chineseCount := 0
	for _, r := range text {
		// 简单判断：大于 ASCII 范围的字符视为中文/多字节字符
		if r > 127 {
			chineseCount++
		}
	}

	// 英文字符数量
	englishCount := len(text) - chineseCount

	// 估算 Token 数量
	tokens := (englishCount / 4) + (chineseCount * 2 / 3)
	return tokens
}

// printTokenStats 打印 Token 统计信息
func (r *Runtime) printTokenStats(prompt string, contextLimit int) {
	tokenCount := estimateTokenCount(prompt)
	percentage := float64(tokenCount) / float64(contextLimit) * 100

	fmt.Printf("\n📊 Token Statistics:\n")
	fmt.Printf("   Prompt length: %d characters\n", len(prompt))
	fmt.Printf("   Estimated tokens: %d / %d (%.1f%%)\n", tokenCount, contextLimit, percentage)

	if percentage > 90 {
		fmt.Printf("   ⚠️  WARNING: Approaching context limit!\n")
	} else if percentage > 75 {
		fmt.Printf("   ⚡ CAUTION: Using >75%% of context window\n")
	} else {
		fmt.Printf("   ✅ Context usage is healthy\n")
	}
	fmt.Printf("\n")
}

func nodeTypeStr(t tasknode.TaskType) string {
	switch t {
	case tasknode.Loop:
		return "Loop"
	case tasknode.Leaf:
		return "Leaf"
	default:
		return "Normal"
	}
}

func nodeStatusStr(s tasknode.TaskStatus) string {
	switch s {
	case tasknode.Running:
		return "Running"
	case tasknode.Completed:
		return "Completed"
	case tasknode.Failed:
		return "Failed"
	default:
		return "Pending"
	}
}

// decideNextStep 决定下一步：下树还是上树
// 根据 detail.md 的描述：
// - 对于普通节点：如果 wethertraveled 是 1，则寻找下一个子节点；如果全部子节点 wethertraveled，就返回上级
// - 对于 Loop 节点：检查子节点是否都 finished，如果 finished 就 pop 栈并跳出 loop
// - 对于 Leaf 节点：执行完成后直接返回上级
func (r *Runtime) decideNextStep(current *tasknode.TaskNode) error {
	// For Leaf nodes: Move up. They originate WetherFinished via actions.
	if current.Type == tasknode.Leaf {
		r.cursor.MoveUp()
		return nil
	}

	// For Normal/Loop nodes:
	// We only decide next step AFTER the node has been traveled (processed at least once).
	if current.WetherTraveled {
		// 1. Try to move down to the next untraveled child.
		nextChild := current.GetNextUntraveledChild()
		if nextChild != nil {
			r.cursor.MoveDown()
			return nil
		}

		// 2. All children are finished. Check completion logic.
		allFinished := current.AllChildrenFinished()

		// Case: Loop node
		if current.Type == tasknode.Loop {
			if allFinished {
				current.MarkFinished()
				r.cursor.MoveUp()
				return nil
			}
			// Loop not finished: reset children (traveled AND finished) and stay.
			current.ResetChildrenStatus()
			fmt.Printf("  🔄 Loop [%s] not finished, resetting children status for next iteration\n", current.ID)
			return nil // Stay here to start next iteration
		}

		// Case: Normal node
		// According to user instructions: "all traveled才是完成了" (All traveled means completed).
		// Pop Criteria: Move up once all children are traveled.
		if current.AllChildrenTraveled() || len(current.Children) == 0 {
			// Hierarchical Finished: Only mark finished if all children are finished.
			if allFinished || len(current.Children) == 0 {
				if !current.WetherFinished {
					current.MarkFinished()
				}
			}
			r.cursor.MoveUp()
		} else {
			fmt.Printf("  ⚠️ Normal node [%s] has untraveled children, staying\n", current.ID)
		}
		return nil
	}

	return fmt.Errorf("node [%s] was not marked as traveled before deciding next step", current.ID)
}

// handleError 尝试处理发生的错误。如果已经有配套的 ErrorHandler，则重定向执行。
func (r *Runtime) handleError(node *tasknode.TaskNode, err error) bool {
	if node.ErrorHandler == nil {
		return false // 没有错误处理器
	}

	fmt.Printf("  ⚠️ Node [%s] failed: %v\n", node.ID, err)
	fmt.Printf("  🔧 Executing error handler: %s\n", node.ErrorHandler.Name)

	// 保存错误信息到变量
	if node.Variables == nil {
		node.Variables = make(map[string]interface{})
	}
	node.Variables["last_error"] = err.Error()

	// 将错误处理节点添加为子节点（如果还没有）
	isAlreadyChild := false
	for _, c := range node.Children {
		if c.ID == node.ErrorHandler.ID {
			isAlreadyChild = true
			break
		}
	}
	if !isAlreadyChild {
		node.AddChild(node.ErrorHandler)
	}

	// 标记当前节点为已遍历（跳过原来可能未完成的正常路径）
	if !node.WetherTraveled {
		node.MarkTraveled()
	}

	return true // 错误已被重定向处理
}

// GetCurrentNode 获取当前节点
func (r *Runtime) GetCurrentNode() *tasknode.TaskNode {
	return r.cursor.Current
}

// IsDone 检查是否已完成
func (r *Runtime) IsDone() bool {
	return r.cursor.Done()
}
