package cursor

import (
	"github.com/Steve65535/llmvm/pkg/tasknode"
)

// Cursor 是语法树的读写头，用于深度优先遍历
type Cursor struct {
	Root      *tasknode.TaskNode
	Current   *tasknode.TaskNode
	LoopStack []*tasknode.TaskNode // Loop 节点的栈，用于管理循环状态
}

func New(root *tasknode.TaskNode) *Cursor {
	return &Cursor{
		Root:      root,
		Current:   root,
		LoopStack: []*tasknode.TaskNode{},
	}
}

// Done 检查是否已完成所有遍历
func (c *Cursor) Done() bool {
	return c.Current == nil
}

// MoveDown 向下移动到下一个未遍历的子节点
func (c *Cursor) MoveDown() bool {
	if c.Current == nil {
		return false
	}

	nextChild := c.Current.GetNextUntraveledChild()
	if nextChild != nil {
		// 如果当前节点是 Loop 节点，且不在栈顶（幂等入栈），将其推入栈
		if c.Current.Type == tasknode.Loop {
			if len(c.LoopStack) == 0 || c.LoopStack[len(c.LoopStack)-1] != c.Current {
				c.LoopStack = append(c.LoopStack, c.Current)
			}
		}
		c.Current = nextChild
		return true
	}
	return false
}

// MoveUp 向上返回到父节点
func (c *Cursor) MoveUp() bool {
	if c.Current == nil || c.Current.Parent == nil {
		c.Current = nil
		return false
	}

	// 如果当前节点是 Loop 节点，且我们正从它向上移动（意味着它已结束）
	if c.Current.Type == tasknode.Loop {
		c.PopLoop(c.Current)
	}

	c.Current = c.Current.Parent
	return true
}

// PopLoop 从栈中弹出指定的 Loop 节点
func (c *Cursor) PopLoop(node *tasknode.TaskNode) {
	if len(c.LoopStack) > 0 && c.LoopStack[len(c.LoopStack)-1] == node {
		c.LoopStack = c.LoopStack[:len(c.LoopStack)-1]
	}
}

// GetCurrentLoop 获取当前所在的 Loop 节点（栈顶）
func (c *Cursor) GetCurrentLoop() *tasknode.TaskNode {
	if len(c.LoopStack) == 0 {
		return nil
	}
	return c.LoopStack[len(c.LoopStack)-1]
}

// IsInLoop 检查当前是否在 Loop 节点内
func (c *Cursor) IsInLoop() bool {
	return len(c.LoopStack) > 0
}

// GetPath 获取从根节点到当前节点的路径
func (c *Cursor) GetPath() []string {
	if c.Current == nil {
		return []string{}
	}

	path := []string{}
	node := c.Current
	for node != nil {
		path = append([]string{node.Name}, path...)
		node = node.Parent
	}
	return path
}

// GetRoot 返回执行树的根节点
func (c *Cursor) GetRoot() *tasknode.TaskNode {
	return c.Root
}
