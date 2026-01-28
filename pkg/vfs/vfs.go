package vfs

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
)

// VirtualFileSystem 是物理文件系统的封装
type VirtualFileSystem struct {
	root string
}

// New 创建 VFS 实例
func New(root string) *VirtualFileSystem {
	// 如果 root 是相对路径，转换为绝对路径
	absRoot, err := filepath.Abs(root)
	if err != nil {
		absRoot = root
	}
	return &VirtualFileSystem{root: absRoot}
}

// resolve 将虚拟路径转换为真实路径，并防止路径穿越
func (vfs *VirtualFileSystem) resolve(path string) string {
	return filepath.Join(vfs.root, path)
}

// Read 从文件读取内容
func (vfs *VirtualFileSystem) Read(path string) ([]byte, error) {
	return ioutil.ReadFile(vfs.resolve(path))
}

// Write 向文件写入内容
func (vfs *VirtualFileSystem) Write(path string, data []byte) error {
	// 确保父目录存在
	fullPath := vfs.resolve(path)
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}
	return ioutil.WriteFile(fullPath, data, 0644)
}

// List 列出目录下文件
func (vfs *VirtualFileSystem) List(dir string) ([]string, error) {
	files, err := ioutil.ReadDir(vfs.resolve(dir))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, f := range files {
		names = append(names, f.Name())
	}
	return names, nil
}

// Delete 删除文件
func (vfs *VirtualFileSystem) Delete(path string) error {
	return os.Remove(vfs.resolve(path))
}

// Exists 检查文件是否存在
func (vfs *VirtualFileSystem) Exists(path string) bool {
	_, err := os.Stat(vfs.resolve(path))
	return !os.IsNotExist(err)
}
