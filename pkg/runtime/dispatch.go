package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Steve65535/llmvm/pkg/llm"
	"github.com/Steve65535/llmvm/pkg/tasknode"
)

// ExecuteAction 执行 LLM 返回的单个动作。
//
// 入口先做 Position-based authority 校验（fail-fast），再 dispatch 到对应 handler。
// 拒绝越权动作不会消耗节点 retry budget——错误返回，主循环按一般失败处理。
//
// 各 case 的语义记录在 system prompt 的 "## Action Catalog" 段。
func (r *Runtime) ExecuteAction(action llm.Action, parent *tasknode.TaskNode) error {
	pos := BuildPosition(parent)
	if err := CheckAuthority(pos, action.ActionType); err != nil {
		return err
	}

	switch action.ActionType {
	case "create_node":
		return r.actionCreateNode(action, parent)
	case "mark_complete":
		return r.actionMarkComplete(action, parent)
	case "update_variables":
		return r.actionUpdateVariables(action, parent)
	case "execute_command":
		return r.actionExecuteCommand(action, parent)
	case "append_to_file":
		return r.actionAppendToFile(action, parent)
	case "read_file":
		return r.actionReadFile(action, parent)
	case "write_file":
		return r.actionWriteFile(action, parent)
	case "list_dir":
		return r.actionListDir(action, parent)
	case "search":
		return r.actionSearch(action, parent)
	case "read_artifact":
		return r.actionReadArtifact(action, parent)
	case "shutdown":
		return fmt.Errorf("EMERGENCY_SHUTDOWN: %s", action.Result)
	case "request_human_input":
		return r.handleRequestHumanInput(action, parent)
	case "append_sibling_node":
		return r.handleAppendSiblingNode(action, parent)
	case "query_memory":
		return r.handleQueryMemory(action, parent)
	case "add_artifact":
		return r.handleAddArtifact(action, parent)
	case "modify_artifact":
		return r.handleModifyArtifact(action, parent)
	case "request_context":
		return r.handleRequestContext(action, parent)
	default:
		return fmt.Errorf("unknown action type: %s", action.ActionType)
	}
}

// === Tree mutation ===

func (r *Runtime) actionCreateNode(action llm.Action, parent *tasknode.TaskNode) error {
	childNode := action.Node.ToTaskNode()
	parent.AddChild(childNode)

	if action.ErrorHandlerNode.ID != "" {
		errorHandler := action.ErrorHandlerNode.ToTaskNode()
		childNode.ErrorHandler = errorHandler
	}
	r.inheritAcceptanceCriteria(childNode)
	r.syncNodeToMemory(childNode)
	r.persistAcceptanceCriteria(childNode)
	return nil
}

// actionMarkComplete 处理 mark_complete：验收 → 落 handoff 字段 → SQLite + 异步向量索引。
//
// 校验顺序很重要：必须先跑 acceptance（包括 testable 的 shell 命令），missing 时直接拒绝。
// 拒绝时 SingleFinished 不会被设置，cursor 也不会推进，节点保持当前 turn。
func (r *Runtime) actionMarkComplete(action llm.Action, parent *tasknode.TaskNode) error {
	if len(action.AcceptanceResults) > 0 {
		r.applyAcceptanceResults(parent, action.AcceptanceResults)
	}
	if missing := r.missingRequiredAcceptance(parent); len(missing) > 0 {
		fmt.Printf("  🚫 mark_complete blocked: missing required acceptance results for %v\n", missing)
		return fmt.Errorf("mark_complete rejected: required acceptance criteria not satisfied: %s",
			strings.Join(missing, ", "))
	}

	parent.SingleFinished = true

	if action.Summary != "" {
		parent.Result = action.Summary
		parent.Summary = action.Summary
	} else if action.Result != "" {
		parent.Result = action.Result
	}
	if action.IsImportant {
		parent.IsImportant = true
	}
	if action.Variables != nil {
		if parent.Variables == nil {
			parent.Variables = make(map[string]interface{})
		}
		for k, v := range action.Variables {
			parent.Variables[k] = v
		}
	}
	if action.Goal != "" {
		parent.Goal = action.Goal
	}
	if len(action.Decisions) > 0 {
		parent.Decisions = action.Decisions
	}
	if len(action.Assumptions) > 0 {
		parent.Assumptions = action.Assumptions
	}
	if len(action.Outputs) > 0 {
		parent.Outputs = action.Outputs
	}
	if len(action.OpenQuestions) > 0 {
		parent.OpenQuestions = action.OpenQuestions
	}
	if action.Confidence != "" {
		parent.Confidence = action.Confidence
	}
	if len(action.KeyFacts) > 0 {
		parent.KeyFacts = action.KeyFacts
	}
	if len(action.ArtifactRefs) > 0 {
		parent.ArtifactRefs = action.ArtifactRefs
		for _, ref := range action.ArtifactRefs {
			r.artifacts.Pin(ref)
			if r.memStore != nil {
				_ = r.memStore.UpdateArtifactPinned(ref, true)
			}
		}
	}
	if action.Handoff != "" {
		parent.Handoff = action.Handoff
	}
	if len(parent.KeyFacts) == 0 {
		parent.KeyFacts = r.generateOperationLog(parent)
	}
	if parent.Confidence == "" {
		if len(parent.KeyFacts) > 0 && parent.KeyFacts[0] != "" && !strings.HasPrefix(parent.KeyFacts[0], "[auto]") {
			parent.Confidence = "medium"
		} else {
			parent.Confidence = "auto_generated"
		}
	}
	if parent.IsImportant {
		for _, art := range r.artifacts.ListByNode(parent.ID) {
			r.artifacts.Pin(art.ID)
			if r.memStore != nil {
				_ = r.memStore.UpdateArtifactPinned(art.ID, true)
			}
		}
	}
	r.syncNodeToMemory(parent)
	// 节点 handoff 完成 → 异步把本节点产出的 artifact 批量 embed 入向量库
	r.IndexNodeArtifactsAsync(parent)
	fmt.Printf("  ✅ Action: mark_complete (Result: %s)\n", parent.Result)
	return nil
}

func (r *Runtime) actionUpdateVariables(action llm.Action, parent *tasknode.TaskNode) error {
	if action.Variables != nil {
		if parent.Variables == nil {
			parent.Variables = make(map[string]interface{})
		}
		for k, v := range action.Variables {
			parent.Variables[k] = v
			if r.memStore != nil {
				valueRef := ""
				valueSummary := ""
				switch val := v.(type) {
				case string:
					if len(val) > 200 {
						valueSummary = val[:200] + "..."
					} else {
						valueSummary = val
					}
					valueRef = val
				default:
					if b, err := json.Marshal(v); err == nil {
						s := string(b)
						if len(s) > 200 {
							valueSummary = s[:200] + "..."
						} else {
							valueSummary = s
						}
						valueRef = s
					}
				}
				_ = r.memStore.UpsertScopedVariable(parent.ID, parent.ID, k, valueRef, valueSummary)
			}
		}
	}
	if action.Result != "" {
		parent.Result = action.Result
	}
	if action.IsImportant {
		parent.IsImportant = true
	}
	return nil
}

// === Tools ===

func (r *Runtime) actionExecuteCommand(action llm.Action, parent *tasknode.TaskNode) error {
	fmt.Printf("💻 Executing command: %s\n", action.Command)
	result, err := r.HandleCLI(action.Command)
	if err != nil {
		return fmt.Errorf("command execution failed: %w", err)
	}
	fmt.Printf("📝 Command result: %s\n", result)
	if parent.Variables == nil {
		parent.Variables = make(map[string]interface{})
	}
	art := r.artifacts.Add("command", action.Command, result, parent.ID)
	parent.Variables["last_command"] = art.ID
	if r.memStore != nil {
		_ = r.memStore.UpsertArtifact(art.ID, parent.ID, art.Type, art.Source, art.Summary, art.SpillPath, art.Pinned)
		_ = r.memStore.IndexArtifactFTS(art.ID, art.Source, art.Summary, result)
	}

	// 保留 command history 用于 agentic loop 上下文
	if len(result) > MaxHistoryEntryLength {
		result = result[:MaxHistoryEntryLength] + "\n... [TRUNCATED]"
	}
	histEntry := fmt.Sprintf("[%s] $ %s\n> %s", time.Now().Format("15:04:05"), action.Command, result)
	var history []string
	if existing, ok := parent.Variables["command_output_history"]; ok {
		if casted, ok := existing.([]string); ok {
			history = casted
		} else if castedInterface, ok := existing.([]interface{}); ok {
			for _, item := range castedInterface {
				if str, ok := item.(string); ok {
					history = append(history, str)
				}
			}
		}
	}
	history = append(history, histEntry)
	if len(history) > 10 {
		history = history[len(history)-10:]
	}
	parent.Variables["command_output_history"] = history
	return nil
}

func (r *Runtime) actionAppendToFile(action llm.Action, parent *tasknode.TaskNode) error {
	safePath, err := sandboxPath(action.FilePath)
	if err != nil {
		return err
	}
	fmt.Printf("📝 Appending to file: %s\n", safePath)

	existingContent := ""
	if data, err := os.ReadFile(safePath); err == nil {
		existingContent = string(data)
	}

	newContent := existingContent + action.Content
	dir := filepath.Dir(safePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}
	if err := os.WriteFile(safePath, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("failed to append to file %s: %w", safePath, err)
	}

	if parent.Variables == nil {
		parent.Variables = make(map[string]interface{})
	}
	parent.Variables["last_file_written"] = safePath
	parent.Variables["last_file_size"] = len(newContent)
	fmt.Printf("✅ Successfully appended %d bytes to %s (total: %d bytes)\n",
		len(action.Content), safePath, len(newContent))
	return nil
}

func (r *Runtime) actionReadFile(action llm.Action, parent *tasknode.TaskNode) error {
	safePath, err := sandboxPath(action.FilePath)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(safePath)
	if err != nil {
		return fmt.Errorf("read_file failed: %w", err)
	}
	if parent.Variables == nil {
		parent.Variables = make(map[string]interface{})
	}
	art := r.artifacts.Add("file_read", safePath, string(data), parent.ID)
	parent.Variables["last_read"] = art.ID
	if r.memStore != nil {
		_ = r.memStore.UpsertArtifact(art.ID, parent.ID, art.Type, art.Source, art.Summary, art.SpillPath, art.Pinned)
		_ = r.memStore.IndexArtifactFTS(art.ID, art.Source, art.Summary, string(data))
	}
	fmt.Printf("📖 read_file: %s → %s (%d bytes)\n", safePath, art.ID, len(data))
	return nil
}

func (r *Runtime) actionWriteFile(action llm.Action, parent *tasknode.TaskNode) error {
	safePath, err := sandboxPath(action.FilePath)
	if err != nil {
		return err
	}
	dir := filepath.Dir(safePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("write_file mkdir failed: %w", err)
	}
	if err := os.WriteFile(safePath, []byte(action.Content), 0644); err != nil {
		return fmt.Errorf("write_file failed: %w", err)
	}
	fmt.Printf("✏️  write_file: %s (%d bytes)\n", safePath, len(action.Content))
	return nil
}

func (r *Runtime) actionListDir(action llm.Action, parent *tasknode.TaskNode) error {
	safePath, err := sandboxPath(action.FilePath)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(safePath)
	if err != nil {
		return fmt.Errorf("list_dir failed: %w", err)
	}
	var lines []string
	for _, e := range entries {
		if e.IsDir() {
			lines = append(lines, e.Name()+"/")
		} else {
			lines = append(lines, e.Name())
		}
	}
	if parent.Variables == nil {
		parent.Variables = make(map[string]interface{})
	}
	art := r.artifacts.Add("dir_list", safePath, strings.Join(lines, "\n"), parent.ID)
	parent.Variables["last_list"] = art.ID
	if r.memStore != nil {
		_ = r.memStore.UpsertArtifact(art.ID, parent.ID, art.Type, art.Source, art.Summary, art.SpillPath, art.Pinned)
		_ = r.memStore.IndexArtifactFTS(art.ID, art.Source, art.Summary, strings.Join(lines, "\n"))
	}
	fmt.Printf("📂 list_dir: %s → %s (%d entries)\n", safePath, art.ID, len(entries))
	return nil
}

func (r *Runtime) actionSearch(action llm.Action, parent *tasknode.TaskNode) error {
	safePath, err := sandboxPath(action.FilePath)
	if err != nil {
		return err
	}
	cmd := exec.Command("grep", "-r", "--include=*", "-n", action.Content, safePath)
	out, err := cmd.CombinedOutput()
	result := string(out)
	var searchStatus string
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			searchStatus = "[NO MATCHES FOUND]"
		} else {
			searchStatus = fmt.Sprintf("[SEARCH ERROR: %v]", err)
		}
	}
	if searchStatus != "" {
		result = searchStatus + "\n" + result
	}
	if parent.Variables == nil {
		parent.Variables = make(map[string]interface{})
	}
	source := fmt.Sprintf("%s@%s", action.Content, safePath)
	art := r.artifacts.Add("search", source, result, parent.ID)
	parent.Variables["last_search"] = art.ID
	if r.memStore != nil {
		_ = r.memStore.UpsertArtifact(art.ID, parent.ID, art.Type, art.Source, art.Summary, art.SpillPath, art.Pinned)
		_ = r.memStore.IndexArtifactFTS(art.ID, art.Source, art.Summary, result)
	}
	fmt.Printf("🔍 search: pattern=%q in %s → %s\n", action.Content, safePath, art.ID)
	return nil
}

func (r *Runtime) actionReadArtifact(action llm.Action, parent *tasknode.TaskNode) error {
	startLine := action.StartLine
	endLine := action.EndLine
	if startLine <= 0 {
		startLine = 1
	}
	slice, err := r.artifacts.ReadSlice(action.ArtifactID, startLine, endLine)
	if err != nil {
		return fmt.Errorf("read_artifact failed: %w", err)
	}
	if parent.Variables == nil {
		parent.Variables = make(map[string]interface{})
	}
	if len(slice) > r.budget.MaxCommandResultChars {
		slice = slice[:r.budget.MaxCommandResultChars] + fmt.Sprintf("\n... [TRUNCATED: showing %d of more chars]", r.budget.MaxCommandResultChars)
	}
	// 单槽覆盖：每次 read_artifact 覆盖上一次，不累积片段
	parent.Variables["_artifact_view"] = fmt.Sprintf("[%s lines %d-%d]\n%s", action.ArtifactID, startLine, endLine, slice)
	fmt.Printf("📎 read_artifact: %s lines %d-%d (%d chars)\n", action.ArtifactID, startLine, endLine, len(slice))
	return nil
}
