package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"path/filepath"
	"strings"

	"github.com/Steve65535/llmvm/pkg/artifact"
	"github.com/Steve65535/llmvm/pkg/tasknode"
)

// SaveState 持久化格式：包含任务树 + artifact store。
//
// SQLite + chromem-go 是从 SaveState + artifact store 重建的镜像，所以不进 SaveState；
// 这样存档只是一个 JSON 文件，便于 review 和 git diff。
type SaveState struct {
	Root      *tasknode.TaskNode `json:"root"`
	Artifacts *artifact.Store    `json:"artifacts,omitempty"`
}

// persistState 把当前 root + artifact store 写到 path（pretty-print JSON）。
func persistState(path string, root *tasknode.TaskNode, arts *artifact.Store) {
	state := SaveState{Root: root, Artifacts: arts}
	data, _ := json.MarshalIndent(state, "", "  ")
	_ = ioutil.WriteFile(path, data, 0644)
}

// loadState 从 JSON 文件恢复 root + artifact store。
//
// 兼容老格式：如果整个文件是 *TaskNode（而不是 SaveState），也接受。
func loadState(path string) (*tasknode.TaskNode, *artifact.Store, string) {
	fmt.Printf("📂 Loading state from %s...\n", path)
	data, err := ioutil.ReadFile(path)
	if err != nil {
		log.Fatalf("❌ Failed to read save file: %v", err)
	}

	var root *tasknode.TaskNode
	var arts *artifact.Store

	var state SaveState
	if err := json.Unmarshal(data, &state); err != nil {
		// 向后兼容：尝试直接解析为 TaskNode
		if err2 := json.Unmarshal(data, &root); err2 != nil {
			log.Fatalf("❌ Failed to unmarshal state: %v", err)
		}
	} else {
		root = state.Root
		arts = state.Artifacts
	}

	root.RestoreParents()
	if len(root.Information) == 0 {
		log.Fatalf("❌ Loaded state has no initial request in root.Information")
	}
	initialRequest := root.Information[0]
	fmt.Println("✅ State loaded successfully")
	return root, arts, initialRequest
}

// derivedSQLitePath 从 save/load 的 .json 路径派生 .sqlite 文件名。
//
// foo.json → foo.sqlite。两者都为空时返回空串，runtime 会回退到 :memory:。
func derivedSQLitePath(savePath, loadPath string) string {
	src := savePath
	if src == "" {
		src = loadPath
	}
	if src == "" {
		return ""
	}
	ext := filepath.Ext(src)
	return strings.TrimSuffix(src, ext) + ".sqlite"
}
