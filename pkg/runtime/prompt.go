package runtime

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Steve65535/llmvm/pkg/llm"
	"github.com/Steve65535/llmvm/pkg/tasknode"
)

// buildPromptWithGlobalContext 使用 Runtime 自动组装的全局上下文。
func (r *Runtime) buildPromptWithGlobalContext(current *tasknode.TaskNode, request string, globalContext string, retryError error) (string, error) {
	return r.buildPromptInternalV2(current, request, globalContext, retryError)
}

// buildPromptInternalV2 是 prompt 拼装的核心。
//
// 顺序（保持稳定，便于 prompt cache 命中）：
//
//	## Position（位置工程入口）
//	## Allowed Actions
//	## Acceptance Criteria（如有）
//	## Current Context（taskPath / current node / parent / children / agentic loop hint）
//	## Global Context（buildGlobalContext 来的 context pack）
//	## Structured Request Data（NodeState JSON）
//	## Scoped Variables
//	## Request
//	## Node Type Guidelines / Execution Requirements / Examples
//
// 模板里的 fixed 文本将在 Phase 15 抽到 .md 文件并通过 embed 加载；现阶段先平移过来。
func (r *Runtime) buildPromptInternalV2(current *tasknode.TaskNode, request string, globalContext string, retryError error) (string, error) {
	taskPath := r.cursor.GetPath()

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

	currentInfo := r.nodeToState(current)
	childrenInfo := r.getChildrenInfo(current)

	// Agentic Loop 状态：仅 Leaf 节点在多轮 ReAct 迭代时显示
	loopInfo := ""
	if current.Type == tasknode.Leaf && current.IterationCount > 0 {
		loopInfo = fmt.Sprintf(`> [!IMPORTANT]
> **AGENTIC LOOP ACTIVE (Iteration %d/%d)**
> You are currently in an autonomous refinement loop for this Leaf node.
>
> **Status**:
> - You have already executed commands in previous turns.
> - You are NOT done yet (otherwise you would have called 'mark_complete').
> - If you have completed the goal based on previous observations, you MUST call 'mark_complete' now.
> - If the previous attempt failed or was insufficient, analyze the 'Command Execution History' below and try a DIFFERENT approach.
> - DO NOT repeat the same ineffective command.`, current.IterationCount+1, current.MaxRetries)
	}

	req := llm.Request{
		TaskPath:    taskPath,
		ParentInfo:  parentInfo,
		CurrentInfo: currentInfo,
		Request:     request,
	}
	jsonData, err := json.MarshalIndent(req, "", "    ")
	if err != nil {
		return "", err
	}

	errorContext := ""
	if retryError != nil {
		specificAdvice := ""
		errStr := retryError.Error()
		if strings.Contains(errStr, "SyntaxError") && strings.Contains(errStr, "python") {
			specificAdvice = "\n> [!CAUTION]\n" +
				"> **Python Syntax Error Detected**: You are likely using 'for' or 'if' blocks in a single-line `python3 -c` command. \n" +
				"> This is NOT allowed in one-liners. \n" +
				"> **Fix**: Use list comprehensions or write the code to a temporary .py file and run it instead."
		}
		errorContext = fmt.Sprintf(`
> [!IMPORTANT]
> **Previous Attempt Failed**:
> Error: %v
%s
>
> **Possible Causes & Self-Correction**:
> 1. **Mirroring Failure**: You just repeated the exact same error as the previous turn. STOP and change your strategy.
> 2. **Python Syntax**: If using '-c', don't use nested blocks. Use simple expressions.
> 3. **JSON Format**: Ensure no unescaped quotes or trailing commas.
`, retryError, specificAdvice)
	}

	// === 硬预算约束：先测量固定部分，再把剩余空间分给可变部分 ===
	fixedParts := []string{
		formatPath(taskPath),
		current.ID, current.Name, nodeTypeStr(current.Type), nodeStatusStr(current.Status),
		fmt.Sprintf("%d", current.Index),
		fmt.Sprintf("%v", current.WetherTraveled), fmt.Sprintf("%v", current.WetherFinished),
		strings.Join(current.Information, "\n"),
		parentInfo.ID, parentInfo.Name, parentInfo.Type, parentInfo.Status,
		childrenInfo,
		loopInfo,
		string(jsonData),
		errorContext,
		request,
		nodeTypeStr(current.Type),
	}
	fixedChars := 3000 // prompt 模板本身的固定文本（标题、说明、示例等）
	for _, p := range fixedParts {
		fixedChars += len(p)
	}

	totalCharBudget := r.budget.ContextBudget * 4 * 80 / 100
	remainingChars := totalCharBudget - fixedChars
	if remainingChars < 1000 {
		remainingChars = 1000
	}
	globalContextBudget := remainingChars * 50 / 100
	variablesBudget := remainingChars * 40 / 100

	scopedVariables := r.CollectScopedVariables(current)
	applyCompression(scopedVariables, r.compressionLevel)
	varsStr := formatVariables(scopedVariables, variablesBudget)

	workspaceStr := globalContext
	if r.compressionLevel >= 1 {
		workspaceStr = "[COMPRESSED: global context omitted]"
	}
	if len(workspaceStr) > globalContextBudget {
		workspaceStr = workspaceStr[:globalContextBudget] + "\n... [GLOBAL CONTEXT TRUNCATED TO FIT BUDGET]"
	}

	// Position 摘要 + Allowed Actions（位置工程入口）
	pos := BuildPosition(current)
	positionSummary := FormatPositionSummary(pos)
	allowedActions := FormatAllowedActions(pos)
	acceptanceBlock := formatAcceptanceCriteria(current)

	prompt := fmt.Sprintf(promptTemplateV2,
		positionSummary,
		allowedActions,
		acceptanceBlock,
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

// getChildrenInfo 获取子节点的状态信息（输出到 prompt 的 ## Children Status 段）。
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
		if child.Type == tasknode.Leaf {
			childType = "Leaf"
		}

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

		// 已完成节点的 Result：上游可能依赖这个判断迭代条件
		if child.WetherFinished && child.Result != "" {
			info += fmt.Sprintf("     Result: %s\n", child.Result)
		}
	}

	info += "\n**Status meanings**:\n"
	info += "- Traveled: Whether this node has been visited by the execution cursor\n"
	info += "- Finished: Whether this node has completed its task\n\n"

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

// nodeToState 将 TaskNode 转换为 NodeState（受预算约束的可序列化快照）。
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
	if node.Type == tasknode.Leaf {
		nodeType = "Leaf"
	}

	information := ""
	if len(node.Information) > 0 {
		information = node.Information[0]
	}

	// 防御：克隆 + 按字段截断变量，避免单变量爆 token
	truncatedVars := make(map[string]interface{})
	for k, v := range node.Variables {
		switch val := v.(type) {
		case string:
			if len(val) > 1000 {
				truncatedVars[k] = val[:1000] + "... [TRUNCATED]"
			} else {
				truncatedVars[k] = val
			}
		case []string:
			limit := 10
			start := 0
			if len(val) > limit {
				start = len(val) - limit
			}
			newSlice := []string{}
			if start > 0 {
				newSlice = append(newSlice, "... [EARLIER HISTORY REMOVED]")
			}
			for i := start; i < len(val); i++ {
				s := val[i]
				if len(s) > 1000 {
					s = s[:1000] + "... [TRUNCATED]"
				}
				newSlice = append(newSlice, s)
			}
			truncatedVars[k] = newSlice
		default:
			truncatedVars[k] = v
		}
	}

	return llm.NodeState{
		ID:          node.ID,
		Name:        node.Name,
		Type:        nodeType,
		Status:      status,
		Information: information,
		Variables:   truncatedVars,
		Index:       node.Index,
		Result:      node.Result,
		IsImportant: node.IsImportant,
	}
}

// formatAcceptanceCriteria 把节点的验收标准转成 prompt 段落。
//
// 节点没有验收标准时返回空串（不输出空段，避免噪声）。
func formatAcceptanceCriteria(node *tasknode.TaskNode) string {
	if len(node.AcceptanceCriteria) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## Acceptance Criteria (must satisfy before mark_complete)\n")
	for _, c := range node.AcceptanceCriteria {
		req := "optional"
		if c.Required {
			req = "REQUIRED"
		}
		sb.WriteString(fmt.Sprintf("- [%s] %s — check_type=%s [%s]",
			c.ID, c.Description, c.CheckType, req))
		if c.CheckCommand != "" {
			sb.WriteString(fmt.Sprintf(" (cmd: `%s`)", c.CheckCommand))
		}
		sb.WriteString("\n")
	}
	if len(node.AcceptanceResults) > 0 {
		sb.WriteString("\n### Previous results\n")
		for _, r := range node.AcceptanceResults {
			status := "FAIL"
			if r.Passed {
				status = "PASS"
			}
			sb.WriteString(fmt.Sprintf("- %s: %s — %s\n", r.CriterionID, status, r.Notes))
		}
	}
	return sb.String() + "\n"
}
