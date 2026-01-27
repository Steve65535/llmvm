package vfs

// 面向 AI 的虚拟文件系统的实现

import (
	"sync"
	"time"
)

type FileNode struct {
	Name        string
	Description string
	Parent      *FileNode
	Children    []*FileNode
	Lock        sync.RWMutex
	CreateAt    time.Time
	UpdateAt    time.Time
	IsDir       bool
	ContentHref string //如果是文件 指向href
}

// NewDir 创建一个节点
func NewDir(name string, description string, parent *FileNode) *FileNode {
	now := time.Now()
	node := &FileNode{
		Name:        name,
		Description: description,
		Parent:      parent,
		IsDir:       true,
		Children:    []*FileNode{},
		CreateAt:    now,
		UpdateAt:    now,
	}
	if parent != nil {
		parent.AddChild(node)
	}
	return node
}

func NewFile(name string, description string, parent *FileNode, href string) *FileNode {
	now := time.Now()
	node := &FileNode{
		Name:        name,
		Description: description,
		Parent:      parent,
		IsDir:       false,
		ContentHref: href,
		CreateAt:    now,
		UpdateAt:    now,
	}
	if parent != nil {
		parent.AddChild(node)
	}
	return node
}

func (n *FileNode) AddChild(child *FileNode) {
	if !n.IsDir {
		return
	}
	n.Lock.Lock()
	defer n.Lock.Unlock()
	n.Children = append(n.Children, child)
	n.UpdateAt = time.Now()
}

func (n *FileNode) FindChild(name string) *FileNode {
	n.Lock.RLock()
	defer n.Lock.RUnlock()
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// RemoveChild 根据名字删除子节点
func (n *FileNode) RemoveChild(name string) bool {
	if !n.IsDir {
		return false
	}
	n.Lock.Lock()
	defer n.Lock.Unlock()
	for i, c := range n.Children {
		if c.Name == name {
			// 删除子节点
			n.Children = append(n.Children[:i], n.Children[i+1:]...)
			n.UpdateAt = time.Now()
			return true
		}
	}
	return false
}

// ListChildren 返回当前目录下所有子节点名称
func (n *FileNode) ListChildren() []string {
	n.Lock.RLock()
	defer n.Lock.RUnlock()
	names := make([]string, len(n.Children))
	for i, c := range n.Children {
		names[i] = c.Name
	}
	return names
}
