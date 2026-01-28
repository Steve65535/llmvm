package tasknode

import (
	"errors"
	"sync"
	"time"
)

type TaskStatus int

const (
	Pending TaskStatus = iota
	Running
	Completed
	Failed
)

type TaskType int

const (
	Normal TaskType = iota
	Loop
	Leaf
)

type TaskNode struct {
	ID             string
	Name           string
	Status         TaskStatus
	Type           TaskType
	Information    []string
	Parent         *TaskNode
	Children       []*TaskNode
	CreatedAt      time.Time
	UpdatedAt      time.Time
	WetherTraveled bool // 是否已遍历过
	WetherFinished bool // 是否已完成（主要用于 Loop 节点）
	Variables      map[string]interface{}
	Index          int    // 节点全局索引
	Result         string // 节点执行结果摘要
	mutex          sync.Mutex
}

func NewTaskNode(id, name string, typ TaskType, info []string) *TaskNode {
	return &TaskNode{
		ID:             id,
		Name:           name,
		Status:         Pending,
		Type:           typ,
		Information:    info,
		Children:       []*TaskNode{},
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		WetherTraveled: false,
		WetherFinished: false,
		Variables:      make(map[string]interface{}),
		Index:          -1, // 默认为 -1，由 Runtime 分配
		Result:         "",
	}
}

func (t *TaskNode) AddChild(child *TaskNode) {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	child.Parent = t
	t.Children = append(t.Children, child)
	t.UpdatedAt = time.Now()
}

func (t *TaskNode) RemoveChild(childID string) error {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	for i, c := range t.Children {
		if c.ID == childID {
			t.Children = append(t.Children[:i], t.Children[i+1:]...)
			t.UpdatedAt = time.Now()
			return nil
		}
	}
	return errors.New("child not found")
}

func (t *TaskNode) SetStatus(status TaskStatus) {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	t.Status = status
	t.UpdatedAt = time.Now()
}

func (t *TaskNode) IsCompleted() bool {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	return t.Status == Completed
}

func (t *TaskNode) Traverse(fn func(node *TaskNode)) {
	stack := []*TaskNode{t}
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		fn(node)

		for i := len(node.Children) - 1; i >= 0; i-- {
			stack = append(stack, node.Children[i])
		}
	}
}

// MarkTraveled 标记节点为已遍历
func (t *TaskNode) MarkTraveled() {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.WetherTraveled = true
	t.UpdatedAt = time.Now()
}

// MarkFinished 标记节点为已完成
func (t *TaskNode) MarkFinished() {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.WetherFinished = true
	t.Status = Completed
	t.UpdatedAt = time.Now()
}

// AllChildrenTraveled 检查所有子节点是否都已遍历
func (t *TaskNode) AllChildrenTraveled() bool {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	for _, child := range t.Children {
		if !child.WetherTraveled {
			return false
		}
	}
	return true
}

// AllChildrenFinished 检查所有子节点是否都已完成（用于 Loop 节点）
func (t *TaskNode) AllChildrenFinished() bool {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	for _, child := range t.Children {
		if !child.WetherFinished {
			return false
		}
	}
	return true
}

// GetNextUntraveledChild 获取下一个未遍历的子节点
func (t *TaskNode) GetNextUntraveledChild() *TaskNode {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	for _, child := range t.Children {
		if !child.WetherTraveled {
			return child
		}
	}
	return nil
}

// ResetChildrenTraveled 重置所有子节点的遍历状态（用于 Loop 节点重新执行）
func (t *TaskNode) ResetChildrenTraveled() {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	for _, child := range t.Children {
		child.WetherTraveled = false
		child.UpdatedAt = time.Now()
	}
}
