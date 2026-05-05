package runtime

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Steve65535/llmvm/pkg/llm"
	"github.com/Steve65535/llmvm/pkg/tasknode"
)

// ErrWaitingHuman 是 request_human_input 触发持久化暂停时返回的哨兵错误。
// Execute 主循环捕获此错误后应持久化状态并退出，等待外部调用 ResumeWithHumanResponse。
var ErrWaitingHuman = errors.New("execution paused: waiting for human input")

// handleRequestHumanInput 处理 request_human_input action。
//
// 语义：
//  1. 将请求持久化到节点（HumanRequest 字段），状态设为 WaitingHuman。
//  2. 如果 HumanInputFunc 已注入（测试/交互模式），同步调用并立即恢复。
//  3. 否则返回 ErrWaitingHuman，让 Execute 主循环退出并持久化状态。
//     外部通过 ResumeWithHumanResponse 注入回复后重新调用 Execute 恢复执行。
func (r *Runtime) handleRequestHumanInput(action llm.Action, node *tasknode.TaskNode) error {
	req := &tasknode.HumanRequest{
		Question:  action.Question,
		Context:   action.Context,
		Options:   action.Options,
		Blocking:  action.Blocking,
		CreatedAt: time.Now().Unix(),
	}

	node.HumanRequest = req
	node.Status = tasknode.WaitingHuman

	fmt.Printf("\n🤚 Human Input Required for node [%s]\n", node.ID)
	fmt.Printf("   Question: %s\n", req.Question)
	if req.Context != "" {
		fmt.Printf("   Context: %s\n", req.Context)
	}
	if len(req.Options) > 0 {
		fmt.Printf("   Options: %s\n", strings.Join(req.Options, " | "))
	}

	// 同步模式：HumanInputFunc 已注入（测试或交互式 CLI）
	if r.HumanInputFunc != nil {
		resp, err := r.HumanInputFunc(req)
		if err != nil {
			return fmt.Errorf("human input failed: %w", err)
		}
		r.applyHumanResponse(node, resp)
		return nil
	}

	// 非阻塞模式：stdin 交互（仅在 non-blocking=false 时使用，方便本地调试）
	if !action.Blocking {
		resp, err := readHumanInputFromStdin(req)
		if err != nil {
			return fmt.Errorf("human input failed: %w", err)
		}
		r.applyHumanResponse(node, resp)
		return nil
	}

	// 持久化暂停：返回哨兵错误，Execute 主循环负责持久化并退出
	return ErrWaitingHuman
}

// applyHumanResponse 将人类回复注入节点状态，恢复为 Pending。
func (r *Runtime) applyHumanResponse(node *tasknode.TaskNode, resp *tasknode.HumanResponse) {
	node.HumanResponse = resp
	node.Status = tasknode.Pending

	if node.Variables == nil {
		node.Variables = make(map[string]interface{})
	}
	node.Variables["human_response"] = resp.Value
	if resp.Note != "" {
		node.Variables["human_note"] = resp.Note
	}

	// 同步变量到 SQLite
	if r.memStore != nil {
		_ = r.memStore.UpsertScopedVariable(node.ID, node.ID, "human_response", resp.Value, resp.Value)
		if resp.Note != "" {
			_ = r.memStore.UpsertScopedVariable(node.ID, node.ID, "human_note", resp.Note, resp.Note)
		}
	}

	fmt.Printf("  ✅ Human responded: %q\n", resp.Value)
}

// ResumeWithHumanResponse 向处于 WaitingHuman 状态的节点注入人类回复。
// 注入后节点恢复为 Pending，调用方应重新调用 Execute 继续执行。
// 如果找不到 WaitingHuman 节点，返回错误。
func (r *Runtime) ResumeWithHumanResponse(nodeID string, resp *tasknode.HumanResponse) error {
	root := r.cursor.GetRoot()
	if root == nil {
		return fmt.Errorf("ResumeWithHumanResponse: tree is empty")
	}

	var target *tasknode.TaskNode
	root.Traverse(func(n *tasknode.TaskNode) {
		if n.ID == nodeID && n.Status == tasknode.WaitingHuman {
			target = n
		}
	})

	if target == nil {
		return fmt.Errorf("ResumeWithHumanResponse: node %q not found or not in WaitingHuman state", nodeID)
	}

	r.applyHumanResponse(target, resp)
	return nil
}

// readHumanInputFromStdin 从标准输入读取人类回复（本地调试用）。
func readHumanInputFromStdin(req *tasknode.HumanRequest) (*tasknode.HumanResponse, error) {
	scanner := bufio.NewScanner(os.Stdin)

	if len(req.Options) > 0 {
		fmt.Printf("   Enter one of [%s]: ", strings.Join(req.Options, "/"))
	} else {
		fmt.Print("   Your response: ")
	}

	if !scanner.Scan() {
		return nil, fmt.Errorf("stdin closed")
	}
	value := strings.TrimSpace(scanner.Text())

	if len(req.Options) > 0 {
		valid := false
		for _, opt := range req.Options {
			if strings.EqualFold(value, opt) {
				value = opt
				valid = true
				break
			}
		}
		if !valid {
			fmt.Printf("   ⚠️  Invalid option %q, defaulting to first option: %s\n", value, req.Options[0])
			value = req.Options[0]
		}
	}

	fmt.Print("   Optional note (press Enter to skip): ")
	note := ""
	if scanner.Scan() {
		note = strings.TrimSpace(scanner.Text())
	}

	return &tasknode.HumanResponse{
		Value:     value,
		Note:      note,
		Timestamp: time.Now().Unix(),
	}, nil
}

// handleAppendSiblingNode 处理 append_sibling_node action。
func (r *Runtime) handleAppendSiblingNode(action llm.Action, current *tasknode.TaskNode) error {
	sibling := action.Node.ToTaskNode()

	if err := current.AppendSiblingAfter(sibling); err != nil {
		return fmt.Errorf("append_sibling_node failed: %w", err)
	}

	fmt.Printf("  ➕ append_sibling_node: inserted [%s] %s after [%s]\n",
		sibling.ID, sibling.Name, current.ID)
	return nil
}

// handleQueryMemory 处理 query_memory action（受限查询，不暴露任意 SQL）。
func (r *Runtime) handleQueryMemory(action llm.Action, node *tasknode.TaskNode) error {
	if r.memStore == nil {
		return fmt.Errorf("query_memory: memory store not available")
	}

	limit := action.Limit
	if limit <= 0 {
		limit = 5
	}

	var result interface{}
	var err error

	switch action.QueryType {
	case "sibling_handoffs":
		parentID := ""
		if node.Parent != nil {
			parentID = node.Parent.ID
		}
		if v, ok := action.Filters["parent_id"]; ok {
			if s, ok := v.(string); ok {
				parentID = s
			}
		}
		var statuses []string
		if v, ok := action.Filters["status"]; ok {
			switch sv := v.(type) {
			case []interface{}:
				for _, s := range sv {
					if str, ok := s.(string); ok {
						statuses = append(statuses, str)
					}
				}
			case []string:
				statuses = sv
			}
		}
		result, err = r.memStore.QuerySiblingHandoffs(parentID, node.ID, statuses, limit)

	case "ancestor_chain":
		result, err = r.memStore.QueryAncestorChain(node.ID)

	case "pinned_artifacts":
		result, err = r.memStore.QueryPinnedArtifacts(limit)

	case "recent_artifacts":
		result, err = r.memStore.QueryRecentArtifacts(limit)

	case "fts_artifacts":
		q := ""
		if v, ok := action.Filters["query"]; ok {
			if s, ok := v.(string); ok {
				q = s
			}
		}
		if q == "" {
			return fmt.Errorf("query_memory fts_artifacts: missing filters.query")
		}
		result, err = r.memStore.SearchArtifactsFTS(q, limit)

	default:
		return fmt.Errorf("query_memory: unknown query_type %q", action.QueryType)
	}

	if err != nil {
		return fmt.Errorf("query_memory %s failed: %w", action.QueryType, err)
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	art := r.artifacts.Add("query_memory", action.QueryType, string(data), node.ID)
	if node.Variables == nil {
		node.Variables = make(map[string]interface{})
	}
	node.Variables["last_query_result"] = art.ID
	if r.memStore != nil {
		_ = r.memStore.UpsertArtifact(art.ID, node.ID, art.Type, art.Source, art.Summary, art.SpillPath, art.Pinned)
		_ = r.memStore.IndexArtifactFTS(art.ID, art.Source, art.Summary, string(data))
	}
	fmt.Printf("  🔎 query_memory(%s) → %s (%d bytes)\n", action.QueryType, art.ID, len(data))
	return nil
}
