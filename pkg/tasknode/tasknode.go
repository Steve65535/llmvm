package tasknode

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

type TaskStatus int

const (
	Pending TaskStatus = iota
	Running
	Completed
	Failed
	WaitingHuman // 节点暂停，等待人类输入
)

type TaskType int

const (
	Normal TaskType = iota
	Loop
	Leaf
)

// HumanRequest 是模型发出的结构化人类输入请求
type HumanRequest struct {
	Question  string   `json:"question"`
	Context   string   `json:"context,omitempty"`
	Options   []string `json:"options,omitempty"`
	Blocking  bool     `json:"blocking"`
	CreatedAt int64    `json:"created_at"`
}

// HumanResponse 是人类对请求的结构化回复
type HumanResponse struct {
	Value       string `json:"value"`
	Note        string `json:"note,omitempty"`
	RelatedNode string `json:"related_node,omitempty"`
	Timestamp   int64  `json:"timestamp"`
}

type TaskNode struct {
	ID             string
	Name           string
	Status         TaskStatus
	Type           TaskType
	Information    []string
	Parent         *TaskNode `json:"-"`
	Children       []*TaskNode
	CreatedAt      time.Time
	UpdatedAt      time.Time
	WetherTraveled bool // 是否已遍历过
	WetherFinished bool // 是否已完成（主要用于 Loop 节点）
	SingleFinished bool // Agentic Loop: LLM explicitly called mark_complete
	Variables      map[string]interface{}
	Index          int    // 节点全局索引
	Result         string // 节点执行结果摘要
	IsImportant    bool   // 是否为重要节点（将被纳入全局 RAM）
	ErrorHandler   *TaskNode
	MaxRetries     int
	RetryCount     int
	IterationCount int // Agentic Loop: Current iteration count

	// Node Report（结构化交接）
	KeyFacts     []string `json:"key_facts,omitempty"`
	ArtifactRefs []string `json:"artifact_refs,omitempty"`
	Handoff      string   `json:"handoff,omitempty"`

	// 扩展结构化交接字段
	Goal          string   `json:"goal,omitempty"`
	Summary       string   `json:"summary,omitempty"`
	Decisions     []string `json:"decisions,omitempty"`
	Assumptions   []string `json:"assumptions,omitempty"`
	Outputs       []string `json:"outputs,omitempty"`
	OpenQuestions []string `json:"open_questions,omitempty"`
	Confidence    string   `json:"confidence,omitempty"` // "high", "medium", "low", "auto_generated"

	// Human-in-the-loop
	HumanRequest  *HumanRequest  `json:"human_request,omitempty"`
	HumanResponse *HumanResponse `json:"human_response,omitempty"`

	mutex sync.Mutex
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
		IsImportant:    false,
		ErrorHandler:   nil,
		MaxRetries:     20, // Increased to 20 to support multi-step manual reasoning
		RetryCount:     0,
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

// ResetChildrenStatus 重置所有子节点的遍历和完成状态（用于 Loop 节点重新执行）
func (t *TaskNode) ResetChildrenStatus() {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	for _, child := range t.Children {
		child.WetherTraveled = false
		child.WetherFinished = false
		child.Status = Pending
		child.UpdatedAt = time.Now()
	}
}

// RestoreParents 递归恢复所有子节点的 Parent 指针（用于从 JSON 反序列化后恢复树结构）
func (t *TaskNode) RestoreParents() {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	for _, child := range t.Children {
		child.Parent = t
		child.RestoreParents()
	}
}

// AppendSiblingAfter 在当前节点之后插入一个同级节点。
// 禁止在 root 节点（无 Parent）上调用。
// 禁止在 Loop 节点的直接子节点上调用（Loop 语义尚未验证）。
func (t *TaskNode) AppendSiblingAfter(sibling *TaskNode) error {
	if t.Parent == nil {
		return fmt.Errorf("cannot append sibling to root node")
	}
	if t.Parent.Type == Loop {
		return fmt.Errorf("append_sibling_node is not allowed inside a Loop node (v1 restriction)")
	}

	parent := t.Parent
	parent.mutex.Lock()
	defer parent.mutex.Unlock()

	// 找到当前节点在父节点 children 中的位置
	idx := -1
	for i, c := range parent.Children {
		if c.ID == t.ID {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("current node %s not found in parent's children", t.ID)
	}

	sibling.Parent = parent
	// 插入到 idx+1 位置
	newChildren := make([]*TaskNode, 0, len(parent.Children)+1)
	newChildren = append(newChildren, parent.Children[:idx+1]...)
	newChildren = append(newChildren, sibling)
	newChildren = append(newChildren, parent.Children[idx+1:]...)
	parent.Children = newChildren
	parent.UpdatedAt = time.Now()
	return nil
}
