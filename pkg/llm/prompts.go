package llm

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed prompts
var promptsFS embed.FS

// LoadPrompt 从 embed 的 prompts/ 目录加载 markdown 模板。
//
// 模板内容是源代码的一部分（由 //go:embed 编译进 binary），保证 prompt 与代码版本同步。
// 失败立即 panic：prompt 缺失等价于 binary 损坏，应该被运维察觉而不是悄悄退化。
func LoadPrompt(name string) string {
	data, err := promptsFS.ReadFile("prompts/" + name)
	if err != nil {
		panic(fmt.Sprintf("llm: missing embedded prompt %q: %v", name, err))
	}
	return string(data)
}

// AvailablePrompts 列出 prompts/ 下所有 .md 文件名（调试 / 健康检查用）。
func AvailablePrompts() ([]string, error) {
	entries, err := fs.ReadDir(promptsFS, "prompts")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// SystemPrompt 返回当前 system prompt。模型版本切换 / A/B 时只需替换 system.md。
func SystemPrompt() string {
	return LoadPrompt("system.md")
}
