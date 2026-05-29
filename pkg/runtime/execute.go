package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Steve65535/llmvm/pkg/llm"
	"github.com/Steve65535/llmvm/pkg/tasknode"
)

// Execute 执行深度优先搜索，构建语法树。
//
// 核心循环（无硬迭代上限，依赖停滞检测作为安全阀）：
//
//  1. 节点切换 → 重置 stagnation + compression
//  2. 已 Finished 节点直接 decideNextStep（向上回弹）
//  3. WaitingHuman 节点跳过（同步 handler 已处理 / 异步等 --resume）
//  4. 已 Traveled 的 Normal 节点 → 找下一个未遍历子节点 / 上树
//  5. 否则进入 LLM call retry 循环：
//     a. buildPromptWithGlobalContext + 硬预算预检循环压缩
//     b. engine.Call → ParseResponse
//     c. stagnation 检查（identical response 累计）
//     d. ExecuteAction（命中 ErrWaitingHuman 直接返回让 cmd 持久化）
//     e. action 错误 → handleError or RetryCount++
//  6. 节点失败或成功后 decideNextStep 决定 cursor 推进
func (r *Runtime) Execute(initialRequest string) error {
	if r.cursor.Current == nil {
		return fmt.Errorf("root node is nil")
	}

	iteration := 0

	for !r.cursor.Done() {
		iteration++

		current := r.cursor.Current
		if current == nil {
			break
		}

		// 节点切换时重置 stagnation 检测 + 压缩级别
		if current.ID != r.lastNodeID {
			r.lastResponse = ""
			r.stagnationCount = 0
			r.lastNodeID = current.ID
			r.compressionLevel = 0
		}

		// Finished 节点直接交给 decideNextStep（上树）
		if current.WetherFinished {
			if err := r.decideNextStep(current); err != nil {
				return err
			}
			continue
		}

		// WaitingHuman：节点已暂停等待人类输入（同步 handler 已处理 / 异步则等 --resume）
		if current.Status == tasknode.WaitingHuman {
			fmt.Printf("  ⏸️  Node [%s] is WaitingHuman, skipping\n", current.ID)
			continue
		}

		// 已 Traveled 的 Normal 节点：找下一个未遍历子节点 / 上树
		if current.WetherTraveled && current.Type == tasknode.Normal {
			nextChild := current.GetNextUntraveledChild()
			if nextChild != nil {
				r.cursor.MoveDown()
				continue
			}
			if err := r.decideNextStep(current); err != nil {
				return err
			}
			continue
		}

		fmt.Printf("📍 Step %d: Processing node [%s] %s (Type: %v, Traveled: %v, Finished: %v)\n",
			iteration, current.ID, current.Name, current.Type, current.WetherTraveled, current.WetherFinished)

		globalContext := r.buildGlobalContext(current)

		// 启动 goroutine worker 执行单次 turn：LLM 调用 + action 执行。
		// 主循环阻塞在 channel 上——仍然是串行的，但获得了 timeout + panic 隔离。
		ch := make(chan turnResult, 1)
		ctx := context.Background()
		cancel := func() {}
		if r.LeafTurnTimeout > 0 {
			ctx, cancel = context.WithTimeout(context.Background(), r.LeafTurnTimeout)
		}
		go func() {
			defer cancel()
			defer func() {
				if p := recover(); p != nil {
					ch <- turnResult{kind: turnFailed,
						err: fmt.Errorf("panic in runNodeTurn: %v", p)}
				}
			}()
			ch <- r.runNodeTurn(ctx, current, initialRequest, globalContext)
		}()
		res := <-ch
		cancel()

		switch res.kind {
		case turnShutdown:
			return res.err
		case turnWaitingHuman:
			fmt.Printf("  ⏸️  Node [%s] paused: waiting for human input\n", current.ID)
			if r.OnStepComplete != nil {
				r.OnStepComplete(current)
			}
			return ErrWaitingHuman
		case turnFailed:
			current.Status = tasknode.Failed
			current.Result = fmt.Sprintf("Error: %v", res.err)
			r.syncNodeToMemory(current)
			if err := r.decideNextStep(current); err != nil {
				return err
			}
		case turnOK:
			if !current.WetherTraveled {
				current.MarkTraveled()
			}
			current.RetryCount = 0
		}

		if current.Status != tasknode.Failed {
			if err := r.decideNextStep(current); err != nil {
				return fmt.Errorf("failed to decide next step: %w", err)
			}
		}

		if r.OnStepComplete != nil {
			r.OnStepComplete(current)
		}
		fmt.Println()
	}
	return nil
}

// decideNextStep 决定 cursor 下一步：下树还是上树。
//
// 删除 Loop 后只剩两种角色：
//   - Leaf：委托给 agentic_loop.go 的 HandleLeafAgenticLoop
//   - Normal：所有子节点遍历完则上树
func (r *Runtime) decideNextStep(current *tasknode.TaskNode) error {
	// Leaf 节点交给 Agentic Loop（多轮 ReAct → mark_complete）
	if r.HandleLeafAgenticLoop(current) {
		return nil
	}

	if current.WetherTraveled {
		nextChild := current.GetNextUntraveledChild()
		if nextChild != nil {
			r.cursor.MoveDown()
			return nil
		}

		allFinished := current.AllChildrenFinished()
		if current.AllChildrenTraveled() || len(current.Children) == 0 {
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

// handleError 尝试把错误重定向到节点的 ErrorHandler。
//
// 返回 true 表示已重定向（调用方应中止当前 turn 的剩余 actions，等下次 cursor 调度）。
// 返回 false 表示节点没有 ErrorHandler，调用方按一般失败处理（RetryCount++）。
func (r *Runtime) handleError(node *tasknode.TaskNode, err error) bool {
	if node.ErrorHandler == nil {
		return false
	}

	fmt.Printf("  ⚠️ Node [%s] failed: %v\n", node.ID, err)
	fmt.Printf("  🔧 Executing error handler: %s\n", node.ErrorHandler.Name)

	if node.Variables == nil {
		node.Variables = make(map[string]interface{})
	}
	node.Variables["last_error"] = err.Error()

	// 把错误处理节点添加为子节点（如果还没有）
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

	// 跳过原有正常路径
	if !node.WetherTraveled {
		node.MarkTraveled()
	}
	return true
}

// hasUnfinishedChildren 判断节点是否仍有未完成的子节点（cursor 调度辅助）。
func (r *Runtime) hasUnfinishedChildren(node *tasknode.TaskNode) bool {
	for _, child := range node.Children {
		if !child.WetherFinished {
			return true
		}
	}
	return false
}

// turnKind 描述单次 LLM turn 的结果类型。
type turnKind int

const (
	turnOK           turnKind = iota // actions 执行成功，节点继续
	turnFailed                       // 节点应标记 Failed
	turnWaitingHuman                 // 节点暂停等待人类输入
	turnShutdown                     // EMERGENCY_SHUTDOWN
)

// turnResult 是 runNodeTurn 的返回值。
type turnResult struct {
	kind     turnKind
	response *llm.Response
	err      error
}

// runNodeTurn 执行单次节点 LLM turn：build prompt → budget check → Call → parse → stagnation → ExecuteAction。
//
// 调用方（Execute 主循环）负责 cursor 推进和 AST 状态变更；
// 本方法只做 LLM 交互和 action 执行，不碰 cursor。
// 通过 ctx 支持超时取消；panic 由调用方的 goroutine recover 捕获。
func (r *Runtime) runNodeTurn(ctx context.Context, current *tasknode.TaskNode,
	initialRequest, globalContext string) turnResult {

	const maxRetries = 9
	var lastErr error
	retryCount := 0

	for retryCount <= maxRetries {
		// 检查 ctx 是否已取消（超时）
		select {
		case <-ctx.Done():
			return turnResult{kind: turnFailed, err: fmt.Errorf("turn timeout: %w", ctx.Err())}
		default:
		}

		prompt, err := r.buildPromptWithGlobalContext(current, initialRequest, globalContext, lastErr)
		if err != nil {
			return turnResult{kind: turnFailed, err: fmt.Errorf("failed to build prompt: %w", err)}
		}

		if current.Index == -1 {
			r.nodeCounter++
			current.Index = r.nodeCounter
		}

		// 硬预算预检：循环压缩直到 prompt 落在预算内
		for {
			estimatedTokens := EstimateTokenCount(prompt)
			if estimatedTokens <= r.budget.ContextBudget {
				break
			}
			if r.compressionLevel >= 4 {
				lastErr = fmt.Errorf("prompt %d tokens exceeds budget %d even at max compression level 4",
					estimatedTokens, r.budget.ContextBudget)
				retryCount++
				fmt.Printf("  🚨 Budget hard limit: %v\n", lastErr)
				break
			}
			r.compressionLevel++
			fmt.Printf("  🗜️  Prompt %d tokens exceeds budget %d, pre-compressing to level %d\n",
				estimatedTokens, r.budget.ContextBudget, r.compressionLevel)
			prompt, err = r.buildPromptWithGlobalContext(current, initialRequest, globalContext, lastErr)
			if err != nil {
				return turnResult{kind: turnFailed, err: fmt.Errorf("failed to rebuild prompt: %w", err)}
			}
		}
		if lastErr != nil && r.compressionLevel >= 4 {
			continue
		}

		fmt.Printf("  🤖 Calling LLM (Attempt %d)...\n", retryCount+1)
		r.printTokenStats(prompt, r.budget.ContextBudget)

		output, err := r.engine.Call(prompt)
		if err != nil {
			if isContextOverflow(err) {
				if r.compressionLevel >= 4 {
					lastErr = fmt.Errorf("context overflow persists at max compression (level %d): %w",
						r.compressionLevel, err)
					retryCount++
					continue
				}
				r.compressionLevel++
				fmt.Printf("  🗜️  Context overflow detected, escalating compression to level %d\n",
					r.compressionLevel)
				continue
			}
			lastErr = fmt.Errorf("LLM call failed: %w", err)
			retryCount++
			continue
		}

		response, parseErr := llm.ParseResponse(output.Response)
		if parseErr != nil {
			lastErr = parseErr
			retryCount++
			continue
		}

		// Stagnation Detection
		if output.Response == r.lastResponse {
			r.stagnationCount++
			fmt.Printf("  ⚠️  Stagnation Detected (Level %d/4) for node [%s]\n", r.stagnationCount, current.ID)
			if r.stagnationCount >= 4 {
				fmt.Printf("  🚨 CRITICAL STAGNATION: LLM is stuck repeating itself. Forcing node failure.\n")
				return turnResult{
					kind: turnFailed,
					err:  fmt.Errorf("Critical Stagnation - LLM repeated the exact same response 4 times"),
				}
			} else if r.stagnationCount >= 2 {
				lastErr = fmt.Errorf("STAGNATION_DETECTED: You are repeating your previous response exactly. Break the loop! Change your strategy or create a new node to progress.")
				retryCount++
				continue
			}
		} else {
			r.lastResponse = output.Response
			r.stagnationCount = 0
		}

		// Execute actions
		actionErr := false
		for _, action := range response.Actions {
			if err := r.ExecuteAction(action, current); err != nil {
				if strings.Contains(err.Error(), "EMERGENCY_SHUTDOWN") {
					return turnResult{kind: turnShutdown, err: err}
				}
				if errors.Is(err, ErrWaitingHuman) {
					return turnResult{kind: turnWaitingHuman, err: ErrWaitingHuman}
				}
				lastErr = fmt.Errorf("failed to execute action: %w", err)
				if r.handleError(current, lastErr) {
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
				return turnResult{
					kind: turnFailed,
					err:  fmt.Errorf("Maximum retries reached. Last error: %v", lastErr),
				}
			}
			continue
		}

		fmt.Printf("  ✅ Step processed successfully: %d action(s)\n", len(response.Actions))
		return turnResult{kind: turnOK, response: response}
	}

	// retry 耗尽
	return turnResult{kind: turnFailed, err: fmt.Errorf("Maximum LLM/API retries reached. Last error: %v", lastErr)}
}
