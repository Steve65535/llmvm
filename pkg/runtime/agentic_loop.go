package runtime

import (
	"fmt"

	"github.com/Steve65535/llmvm/pkg/tasknode"
)

// HandleLeafAgenticLoop 处理 Leaf 节点的 Agentic Loop 遍历逻辑。
// 返回 true 表示已处理（调用者自理或已完成），false 表示非 Leaf 节点或需要继续处理。
func (r *Runtime) HandleLeafAgenticLoop(current *tasknode.TaskNode) bool {
	if current.Type != tasknode.Leaf {
		return false
	}

	// 🔧 FIX(Defect 3): 如果节点已经 Failed 或 Finished，强制完成并上移，防止死循环
	if current.Status == tasknode.Failed || current.WetherFinished {
		fmt.Printf("  ❌ Leaf [%s] is Failed/Finished, forcing MoveUp\n", current.ID)
		r.clearLeafScratchpad(current)
		if !current.WetherFinished {
			current.MarkFinished()
		}
		r.cursor.MoveUp()
		return true
	}

	if current.SingleFinished {
		// LLM 明确说“我做完了” (mark_complete)
		fmt.Printf("  ✅ Leaf [%s] marked complete by LLM, cleaning up scratchpad and popping\n", current.ID)
		r.clearLeafScratchpad(current)
		current.MarkFinished()
		r.cursor.MoveUp()
		return true
	}

	// 安全阀：MaxRetries (防止 Leaf 节点陷入死循环)
	if current.IterationCount >= current.MaxRetries {
		fmt.Printf("  ⚠️ Leaf [%s] hit max retries (%d), marking Failed\n", current.ID, current.MaxRetries)
		r.clearLeafScratchpad(current)

		// 🔴 FIX: DO NOT call MarkFinished() here. If it failed, it is NOT finished.
		// Faking 'Finished' causes parent Loops to exit prematurely.
		if current.Status != tasknode.Failed {
			current.Status = tasknode.Failed
			current.Result = "Error: Maximum Agentic Loop retries reached"
		}

		r.cursor.MoveUp()
		return true
	}

	// Stay: 核心机制
	// 我们不 MoveUp，而是留在当前节点。
	// 为了让主循环下一轮继续处理它，我们需要标记它为“未遍历”。
	// 这样 Execute 逻辑就会再次为它准备 Context 并呼叫 LLM。
	fmt.Printf("  🔄 Leaf [%s] continuing Agentic Loop (Iteration: %d/%d)\n", current.ID, current.IterationCount+1, current.MaxRetries)
	current.WetherTraveled = false
	current.IterationCount++
	return true
}

// clearLeafScratchpad 强制清空 Leaf 节点的本地 RAM（过程数据）。
// 仅保留 Result 和 IsImportant 等总结性信息。
func (r *Runtime) clearLeafScratchpad(node *tasknode.TaskNode) {
	if node.Variables == nil {
		return
	}
	// 丢弃所有历史输出和中间状态，防止泄漏到总线 context
	delete(node.Variables, "command_output_history")
	delete(node.Variables, "last_command_result")
	delete(node.Variables, "_artifact_view")
	fmt.Printf("  🧹 Scratcpad cleared for Leaf [%s]\n", node.ID)
}
