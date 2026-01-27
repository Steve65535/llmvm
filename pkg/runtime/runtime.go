package runtime

import (
	"encoding/json"
	"fmt"

	"github.com/Steve65535/llmvm/pkg/cursor"
	"github.com/Steve65535/llmvm/pkg/llm"
	"github.com/Steve65535/llmvm/pkg/tasknode"
)

// Runtime 是图灵完备的 LLM 运行时引擎
type Runtime struct {
	engine llm.Engine
	cursor *cursor.Cursor
}

// NewRuntime 创建新的运行时实例
func NewRuntime(engine llm.Engine, root *tasknode.TaskNode) *Runtime {
	return &Runtime{
		engine: engine,
		cursor: cursor.New(root),
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

		// 1. 构建 stateless prompt（不包含历史上下文）
		prompt, err := r.buildPrompt(current, initialRequest)
		if err != nil {
			return fmt.Errorf("failed to build prompt: %w", err)
		}

		// 输出 prompt 用于调试
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("📤 PROMPT TO LLM:")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println(prompt)
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println()

		// 2. 与 LLM 对话（stateless）
		fmt.Printf("  🤖 Calling LLM...\n")
		output, err := r.engine.Call(prompt)
		if err != nil {
			return fmt.Errorf("LLM call failed: %w", err)
		}

		// 输出原始响应
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("📥 RAW RESPONSE FROM LLM:")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println(output.Response)
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println()

		// 3. 解析 LLM 响应
		response, err := llm.ParseResponse(output.Response)
		if err != nil {
			return fmt.Errorf("failed to parse response: %w", err)
		}

		fmt.Printf("  ✅ Parsed successfully: %d action(s)\n", len(response.Actions))

		// 4. 执行动作：创建节点或标记完成
		for _, action := range response.Actions {
			if err := r.executeAction(action, current); err != nil {
				return fmt.Errorf("failed to execute action: %w", err)
			}
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
func (r *Runtime) buildPrompt(current *tasknode.TaskNode, request string) (string, error) {
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

	// 转换为 JSON（用于结构化数据展示）
	jsonData, err := json.MarshalIndent(req, "", "    ")
	if err != nil {
		return "", err
	}

	// 构建完整的 prompt，包含上下文信息
	prompt := fmt.Sprintf(`## Current Context

**Task Path**: %s

**Current Node**:
- ID: %s
- Name: %s
- Type: %s
- Status: %s
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

## Structured Request Data

%s

## Request

%s

## Your Task

Based on the above context, determine what actions to take. Consider:
1. If the current node is a Leaf node, you should mark it as complete after processing
2. If the current node needs decomposition, create appropriate child nodes
3. If the current node is a Loop node, ensure all children are created before marking complete
4. Node types:
   - Normal: For task decomposition
   - Loop: For iterative/cyclic tasks
   - Leaf: For atomic tasks that can be completed in one step

Please respond with valid JSON in the required format.`,
		formatPath(taskPath),
		currentInfo.ID, currentInfo.Name, currentInfo.Type, currentInfo.Status,
		current.WetherTraveled, current.WetherFinished, currentInfo.Information,
		parentInfo.ID, parentInfo.Name, parentInfo.Type, parentInfo.Status,
		childrenInfo,
		loopInfo,
		string(jsonData),
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
	}
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
		if r.cursor.Current != nil {
			r.cursor.Current.MarkFinished()
		}
		return nil
	default:
		return fmt.Errorf("unknown action type: %s", action.ActionType)
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
