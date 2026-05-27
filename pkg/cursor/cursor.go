package cursor

import (
	"github.com/Steve65535/llmvm/pkg/tasknode"
)

// Cursor 是语法树的读写头，用于深度优先遍历。
// Loop 节点已删除，循环语义由"验收标准 + retry budget"协议替代，因此 cursor 不再维护 LoopStack。
type Cursor struct {
	Root    *tasknode.TaskNode
	Current *tasknode.TaskNode
}

func New(root *tasknode.TaskNode) *Cursor {
	return &Cursor{
		Root:    root,
		Current: root,
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
		c.Current = nextChild
		return true
	}
	return false
}

// MoveFirstUnfinishedChild 向下移动到第一个未完成的子节点（无论是否已遍历）
func (c *Cursor) MoveFirstUnfinishedChild() bool {
	if c.Current == nil {
		return false
	}
	for _, child := range c.Current.Children {
		if !child.WetherFinished {
			c.Current = child
			return true
		}
	}
	return false
}

// MoveUp 向上返回到父节点
func (c *Cursor) MoveUp() bool {
	if c.Current == nil || c.Current.Parent == nil {
		c.Current = nil
		return false
	}
	c.Current = c.Current.Parent
	return true
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
