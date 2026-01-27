package vfs

import "errors"

// VirtualFileSystem 是最小骨架
type VirtualFileSystem struct {
    root string
}

// New 创建 VFS 实例
func New(root string) *VirtualFileSystem {
    return &VirtualFileSystem{root: root}
}

// Read 从虚拟路径读取文件
func (vfs *VirtualFileSystem) Read(path string) ([]byte, error) {
    return nil, errors.New("Read not implemented")
}

// Write 向虚拟路径写入文件
func (vfs *VirtualFileSystem) Write(path string, data []byte) error {
    return errors.New("Write not implemented")
}

// List 列出目录下文件
func (vfs *VirtualFileSystem) List(dir string) ([]string, error) {
    return nil, errors.New("List not implemented")
}
