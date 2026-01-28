package runtime

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Steve65535/llmvm/pkg/cursor"
	"github.com/Steve65535/llmvm/pkg/llm"
	"github.com/Steve65535/llmvm/pkg/tasknode"
	"github.com/Steve65535/llmvm/pkg/vfs"
)

// Runtime 是图灵完备的 LLM 运行时引擎
type Runtime struct {
	engine      llm.Engine
	cursor      *cursor.Cursor
	initialReq  string
	nodeCounter int // 全局节点计数器
	vfs         *vfs.VirtualFileSystem
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

	// 标记根节点为已遍历（如果还没有）
	if !r.cursor.Current.WetherTraveled {
		r.cursor.Current.MarkTraveled()
	}

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

		// 调试输出
		fmt.Printf("📍 Step %d: Processing node [%s] %s (Type: %v, Traveled: %v, Finished: %v)\n",
			iteration, current.ID, current.Name, current.Type, current.WetherTraveled, current.WetherFinished)

		// --- PASS 1: Attention Selection ---
		// 每次对话前的先验：挑选有用的关联节点
		selectedNodeIDs := []string{}
		// 只有在迭代次数大于 1（即已经产生了一些历史节点）时才需要扫索引
		if iteration > 0 {
			ids, err := r.selectAttentionNodes(current, initialRequest)
			if err != nil {
				fmt.Printf("  ⚠️ Attention selection failed (skipping): %v\n", err)
			} else {
				selectedNodeIDs = ids
			}
		}

		// 2. 构建 stateless prompt（包含选中的 RAM 信息）
		var response *llm.Response
		maxRetries := 3
		var lastErr error
		retryCount := 0

		for retryCount <= maxRetries {
			if retryCount > 0 {
				fmt.Printf("  ⚠️ Retry %d/%d due to error: %v\n", retryCount, maxRetries, lastErr)
			}

			// 使用 PASS 1 选中的节点构建主 Prompt
			prompt, err := r.buildPromptWithWorkspace(current, initialRequest, selectedNodeIDs, lastErr)
			if err != nil {
				return fmt.Errorf("failed to build prompt: %w", err)
			}

			// 确保当前节点有 Index
			if current.Index == -1 {
				r.nodeCounter++
				current.Index = r.nodeCounter
			}

			// 输出 prompt 用于调试
			if retryCount == 0 {
				fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
				fmt.Println("📤 PROMPT TO LLM:")
				fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
				fmt.Println(prompt)
				fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
				fmt.Println()
			}

			// 2. 与 LLM 对话（stateless）
			fmt.Printf("  🤖 Calling LLM (Attempt %d)...\n", retryCount+1)
			output, err := r.engine.Call(prompt)
			if err != nil {
				lastErr = fmt.Errorf("LLM call failed: %w", err)
				retryCount++
				continue
			}

			// 3. 解析 LLM 响应
			response, lastErr = llm.ParseResponse(output.Response)
			if lastErr != nil {
				retryCount++
				continue
			}

			// 4. 执行动作：创建节点或标记完成
			actionErr := false
			for _, action := range response.Actions {
				if err := r.executeAction(action, current); err != nil {
					lastErr = fmt.Errorf("failed to execute action: %w", err)
					actionErr = true
					break
				}
			}

			if actionErr {
				retryCount++
				continue
			}

			// 如果走到这里，解析和执行都成功了
			fmt.Printf("  ✅ Step processed successfully: %d action(s)\n", len(response.Actions))
			break
		}

		if retryCount > maxRetries {
			return fmt.Errorf("maximum retries (%d) reached for node [%s]: %w", maxRetries, current.ID, lastErr)
		}

		// 打印成功执行的动作（可选，因为之前已经打印了部分信息）
		for _, action := range response.Actions {
			if action.ActionType == "create_node" {
				fmt.Printf("    ➕ Created node: [%s] %s (Type: %s)\n",
					action.Node.ID, action.Node.Name, action.Node.Type)
			} else if action.ActionType == "mark_complete" {
				fmt.Printf("    ✓ Marked node as complete\n")
			}
		}

		// 5. 决定下一步：下树还是上树
		oldCurrent := r.cursor.Current
		if err := r.decideNextStep(current); err != nil {
			return fmt.Errorf("failed to decide next step: %w", err)
		}

		// 检查是否移动
		if r.cursor.Current != oldCurrent {
			if r.cursor.Current != nil {
				fmt.Printf("  🔄 Moved to: [%s] %s\n", r.cursor.Current.ID, r.cursor.Current.Name)
			} else {
				fmt.Printf("  🔄 Reached root, execution complete\n")
			}
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
Your task is to scan the current state of the task tree and identify nodes whose Variables or Results are useful for the current step.

## Task Tree Index (Compact)
%s

## Current Target Node
- ID: %s
- Name: %s
- Goal: %s

## Your Task
Identify which nodes from the Tree Index contain information (variables/results) that might be needed to solve the current goal. 
Return ONLY a comma-separated list of Node IDs. If none are useful, return "none".
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
				sb.WriteString(fmt.Sprintf("  Result: %s\n", node.Result))
			}
			if len(node.Variables) > 0 {
				varsJSON, _ := json.Marshal(node.Variables)
				sb.WriteString(fmt.Sprintf("  Variables: %s\n", string(varsJSON)))
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
		loopInfo = fmt.Sprintf("Currently inside Loop node: %s (ID: %s). All children finished: %v",
			currentLoop.Name, currentLoop.ID, currentLoop.AllChildrenFinished())
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
> The previous response resulted in the following error:
> %v
>
> Please fix the error and provide a valid JSON response.
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

Based on the above context, determine what actions to take. Consider:
1. If the current node is a Leaf node, you should mark it as complete after processing.
2. IMPORTANT: A Leaf node is defined by its semantic complexity. It should be small enough so that its context and result fit perfectly within a Large Language Model's optimal context window. If a task is too large, decompose it further.
3. If the current node needs decomposition, create appropriate child nodes.
4. If the current node is a Loop node, ensure all children are created before marking complete.
5. You can execute system commands using 'execute_command' to interact with the file system.
6. Node types:
   - Normal: For task decomposition
   - Loop: For iterative/cyclic tasks
   - Leaf: For atomic tasks that fit in context

Please respond with valid JSON in the required format.`,
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
		request)

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

		info += fmt.Sprintf("  %d. [%s] %s (ID: %s) - Traveled: %s, Finished: %s\n",
			i+1, childType, child.Name, child.ID, traveled, finished)
	}

	if allTraveled {
		info += "\nAll children have been traveled."
	}
	if allFinished {
		info += "\nAll children have been finished."
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
	data, _ := json.MarshalIndent(vars, "", "  ")
	return string(data)
}

// executeAction 执行 LLM 返回的动作
func (r *Runtime) executeAction(action llm.Action, parent *tasknode.TaskNode) error {
	switch action.ActionType {
	case "create_node":
		// 创建新节点
		childNode := action.Node.ToTaskNode()
		parent.AddChild(childNode)
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
		// 将结果存回节点变量
		if parent.Variables == nil {
			parent.Variables = make(map[string]interface{})
		}
		parent.Variables["last_command_result"] = result
		return nil
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
	sb.WriteString(fmt.Sprintf("Global Workspace (RAM - Key findings and important variables picked from all nodes):\n"))
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
	// 简单的空格分割
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return "", fmt.Errorf("empty command")
	}

	cmd := parts[0]
	// 过滤掉常用参数，只保留真正的路径参数（这是一个简单的启发式处理）
	var args []string
	for _, p := range parts[1:] {
		if !strings.HasPrefix(p, "-") {
			args = append(args, p)
		}
	}

	switch cmd {
	case "ls":
		dir := "."
		if len(args) > 0 {
			dir = args[0]
		}
		files, err := r.vfs.List(dir)
		if err != nil {
			return "", err
		}
		return strings.Join(files, ", "), nil
	case "cat":
		if len(args) == 0 {
			return "", fmt.Errorf("cat requires a file path")
		}
		content, err := r.vfs.Read(args[0])
		if err != nil {
			return "", err
		}
		return string(content), nil
	case "write":
		// write 通常格式为: write filename content...
		// 这里的解析需要更细致
		if len(parts) < 3 {
			return "", fmt.Errorf("write requires a file path and content")
		}
		path := parts[1]
		// 重新组合内容，避开前两个单词
		content := strings.Join(parts[2:], " ")
		err := r.vfs.Write(path, []byte(content))
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Successfully wrote to %s", path), nil
	case "rm":
		if len(args) == 0 {
			return "", fmt.Errorf("rm requires a file path")
		}
		err := r.vfs.Delete(args[0])
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Successfully deleted %s", args[0]), nil
	default:
		return "", fmt.Errorf("unsupported command: %s (only ls, cat, write, rm are supported)", cmd)
	}
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
	// 对于 Leaf 节点，执行完成后直接返回上级
	if current.Type == tasknode.Leaf {
		// Leaf 节点执行后标记为完成
		if !current.WetherFinished {
			current.MarkFinished()
		}
		r.cursor.MoveUp()
		return nil
	}

	// 对于 Loop 节点，检查子节点是否都已完成
	if current.Type == tasknode.Loop {
		if current.AllChildrenFinished() {
			// 所有子节点都已完成，标记当前 loop 节点为完成
			current.MarkFinished()
			r.cursor.MoveUp()
			return nil
		}
		// Loop 节点未完成
		// 如果所有子节点都已遍历但未完成，重置遍历状态以继续循环
		if current.AllChildrenTraveled() && !current.AllChildrenFinished() {
			// 重置子节点的遍历状态，允许重新执行
			current.ResetChildrenTraveled()
			fmt.Printf("  🔄 Loop not finished, resetting children traveled state to continue loop\n")
		}
		// 继续处理子节点
	}

	// 对于普通节点和 Loop 节点：
	// 如果 wethertraveled 是 1（已遍历），寻找下一个未遍历的子节点
	if current.WetherTraveled {
		nextChild := current.GetNextUntraveledChild()
		if nextChild != nil {
			// 有未遍历的子节点，向下移动
			r.cursor.MoveDown()
			return nil
		}
		// 所有子节点都已遍历
		// 检查是否所有子节点都已完成
		if len(current.Children) > 0 && current.AllChildrenFinished() {
			// 所有子节点都已完成，标记当前节点为完成
			current.MarkFinished()
		}
		// 返回上级（如果是根节点，MoveUp 会将 Current 设为 nil，循环会结束）
		r.cursor.MoveUp()
		return nil
	}

	// 当前节点未遍历，先标记为已遍历，然后尝试向下移动
	current.MarkTraveled()
	if r.cursor.MoveDown() {
		return nil
	}

	// 无法向下移动（没有子节点）
	// 如果是 Leaf 节点或没有子节点，标记为完成
	if current.Type == tasknode.Leaf || len(current.Children) == 0 {
		current.MarkFinished()
	}
	// 返回上级（如果是根节点，MoveUp 会将 Current 设为 nil，循环会结束）
	r.cursor.MoveUp()
	return nil
}

// GetCurrentNode 获取当前节点
func (r *Runtime) GetCurrentNode() *tasknode.TaskNode {
	return r.cursor.Current
}

// IsDone 检查是否已完成
func (r *Runtime) IsDone() bool {
	return r.cursor.Done()
}
