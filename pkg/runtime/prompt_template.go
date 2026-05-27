package runtime

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed prompts
var promptsFS embed.FS

// LoadPrompt 从 embed 的 prompts/ 目录加载 markdown 模板。
//
// prompt 内容是源代码的一部分（//go:embed 编译进 binary），保证 prompt 与代码同步。
// 缺失等价于 binary 损坏，立即 panic（不悄悄退化为空字符串）。
func LoadPrompt(name string) string {
	data, err := promptsFS.ReadFile("prompts/" + name)
	if err != nil {
		panic(fmt.Sprintf("runtime: missing embedded prompt %q: %v", name, err))
	}
	return string(data)
}

// AvailablePrompts 列出 prompts/ 下所有 .md 文件名。
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

// promptTemplateV2 是节点 prompt 的拼装模板（从 prompts/node_prompt.md 加载）。
//
// 占位符顺序与 buildPromptInternalV2 调用 fmt.Sprintf 的实参顺序对齐：
//
//	1.  positionSummary
//	2.  allowedActions
//	3.  acceptanceBlock
//	4.  taskPath
//	5.  current.ID
//	6.  current.Name
//	7.  current.Type
//	8.  current.Status
//	9.  current.Index
//	10. current.WetherTraveled
//	11. current.WetherFinished
//	12. current.Information (joined)
//	13. parentInfo.ID
//	14. parentInfo.Name
//	15. parentInfo.Type
//	16. parentInfo.Status
//	17. childrenInfo
//	18. loopInfo
//	19. workspaceStr (global context)
//	20. structured request JSON
//	21. varsStr
//	22. errorContext
//	23. request
//	24. current.Type (in Node Type Guidelines)
var promptTemplateV2 = LoadPrompt("node_prompt.md")
