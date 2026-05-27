package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Steve65535/llmvm/pkg/artifact"
	"github.com/Steve65535/llmvm/pkg/llm"
	"github.com/Steve65535/llmvm/pkg/runtime"
	"github.com/Steve65535/llmvm/pkg/tasknode"
)

// SaveState 持久化格式：包含任务树 + artifact store
type SaveState struct {
	Root      *tasknode.TaskNode `json:"root"`
	Artifacts *artifact.Store    `json:"artifacts,omitempty"`
}

func main() {
	// 0. 解析命令行参数
	savePath := flag.String("save", "", "Path to save execution state (JSON)")
	loadPath := flag.String("load", "", "Path to load execution state (JSON)")
	resumeNode := flag.String("resume", "", "Node ID to resume (inject human response for WaitingHuman node)")
	flag.Parse()

	// 1. 初始化 LLM 引擎
	var engine llm.Engine
	apiEngine, err := llm.NewLLMEngine()
	if err != nil {
		fmt.Println("⚠️  Warning: LLM API not available, using StubEngine for testing")
		engine = &llm.StubEngine{}
	} else {
		fmt.Println("✅ LLM Engine initialized successfully")
		engine = apiEngine
	}

	var root *tasknode.TaskNode
	var initialRequest string
	var savedArtifacts *artifact.Store

	// 2. 加载或创建初始状态
	if *loadPath != "" {
		fmt.Printf("📂 Loading state from %s...\n", *loadPath)
		data, err := ioutil.ReadFile(*loadPath)
		if err != nil {
			log.Fatalf("❌ Failed to read save file: %v", err)
		}
		var state SaveState
		if err := json.Unmarshal(data, &state); err != nil {
			// 向后兼容：尝试直接解析为 TaskNode
			if err2 := json.Unmarshal(data, &root); err2 != nil {
				log.Fatalf("❌ Failed to unmarshal state: %v", err)
			}
		} else {
			root = state.Root
			savedArtifacts = state.Artifacts
		}
		root.RestoreParents()
		// 🔧 FIX(Defect 4): 防止 Information 为空时越界 panic
		if len(root.Information) > 0 {
			initialRequest = root.Information[0]
		} else {
			log.Fatalf("❌ Loaded state has no initial request in root.Information")
		}
		fmt.Println("✅ State loaded successfully")
	} else {
		// 获取用户命令
		var command string
		args := flag.Args()
		if len(args) > 0 {
			command = strings.Join(args, " ")
			fmt.Printf("📝 Command from arguments: %s\n\n", command)
		} else {
			fmt.Println("🚀 LLMVM - Advanced Agent Runtime")
			fmt.Println("==========================================")
			fmt.Println("Enter your command (or 'exit' to quit):")
			fmt.Print("> ")

			reader := bufio.NewReader(os.Stdin)
			input, err := reader.ReadString('\n')
			if err != nil {
				log.Fatalf("Failed to read input: %v", err)
			}

			command = strings.TrimSpace(input)
			if command == "" || strings.ToLower(command) == "exit" {
				return
			}
		}
		initialRequest = command
		root = tasknode.NewTaskNode("root", "Root Task", tasknode.Normal, []string{command})
		root.SetStatus(tasknode.Running)
	}

	// 3. 创建运行时并执行
	// 从 --save 路径派生 SQLite 文件名（foo.json → foo.sqlite）
	dbPath := ""
	if *savePath != "" {
		ext := filepath.Ext(*savePath)
		dbPath = strings.TrimSuffix(*savePath, ext) + ".sqlite"
	} else if *loadPath != "" {
		ext := filepath.Ext(*loadPath)
		dbPath = strings.TrimSuffix(*loadPath, ext) + ".sqlite"
	}
	rt := runtime.NewRuntime(engine, root, dbPath)

	// 恢复 artifact store（如果从保存点加载）
	if savedArtifacts != nil {
		rt.SetArtifacts(savedArtifacts)
		fmt.Println("✅ Artifact store restored")
	}

	// --load 后重建 SQLite index，防止 .sqlite 丢失或过期
	if *loadPath != "" {
		fmt.Println("🔄 Rebuilding SQLite index from loaded state...")
		if err := rt.RebuildIndexFromAST(); err != nil {
			fmt.Printf("⚠️  SQLite rebuild failed: %v (continuing with AST fallback)\n", err)
		} else {
			fmt.Println("✅ SQLite index rebuilt")
		}
	}

	// --resume：向 WaitingHuman 节点注入人类回复
	if *resumeNode != "" {
		resp := promptHumanResponse(*resumeNode)
		if err := rt.ResumeWithHumanResponse(*resumeNode, resp); err != nil {
			log.Fatalf("❌ Resume failed: %v", err)
		}
		fmt.Printf("✅ Injected human response for node [%s]\n", *resumeNode)
	} else if *loadPath != "" {
		// 自动检测树中是否有 WaitingHuman 节点
		if waiting := findWaitingHumanNodes(root); len(waiting) > 0 {
			fmt.Printf("⏸️  Found %d WaitingHuman node(s): %s\n", len(waiting), strings.Join(waiting, ", "))
			fmt.Printf("   Use --resume <node-id> to inject a human response and continue.\n")
			os.Exit(0)
		}
	}

	// 保存辅助函数
	saveState := func(path string) {
		state := SaveState{Root: root, Artifacts: rt.GetArtifacts()}
		data, _ := json.MarshalIndent(state, "", "  ")
		ioutil.WriteFile(path, data, 0644)
	}

	// 🆕 增量保存支持
	if *savePath != "" {
		rt.OnStepComplete = func(node *tasknode.TaskNode) {
			fmt.Printf("💾 Autosaving state to %s...\n", *savePath)
			saveState(*savePath)
		}
	}

	// 🆕 信号处理（Ctrl+C 自动保存）
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n⚠️  Interrupted. Performing emergency save...")
		if *savePath != "" {
			saveState(*savePath)
			fmt.Printf("✅ State saved to %s. Exiting.\n", *savePath)
		}
		os.Exit(0)
	}()

	fmt.Println("🌳 Starting execution...")
	if err := rt.Execute(initialRequest); err != nil {
		fmt.Printf("❌ Execution error: %v\n", err)
		// 即使出错也尝试保存状态
	}

	// 4. 如果指定了保存路径，则持久化
	if *savePath != "" {
		fmt.Printf("💾 Saving state to %s...\n", *savePath)
		saveState(*savePath)
		fmt.Println("✅ State saved successfully")
	}

	// 5. 打印结果树
	fmt.Println()
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("✅ Final Syntax Tree:")
	fmt.Println(strings.Repeat("=", 60))
	printTree(root, 0)
}

// printTree 递归打印任务树
func printTree(node *tasknode.TaskNode, indent int) {
	prefix := ""
	for i := 0; i < indent; i++ {
		prefix += "  "
	}

	nodeType := "Normal"
	if node.Type == tasknode.Leaf {
		nodeType = "Leaf"
	}

	status := "Pending"
	switch node.Status {
	case tasknode.Running:
		status = "Running"
	case tasknode.Completed:
		status = "Completed"
	case tasknode.Failed:
		status = "Failed"
	case tasknode.WaitingHuman:
		status = "WaitingHuman"
	}

	fmt.Printf("%s[%s] %s (ID: %s, Status: %s, Traveled: %v, Finished: %v)\n",
		prefix, nodeType, node.Name, node.ID, status, node.WetherTraveled, node.WetherFinished)

	if len(node.Information) > 0 {
		for _, info := range node.Information {
			fmt.Printf("%s  Info: %s\n", prefix, info)
		}
	}

	for _, child := range node.Children {
		printTree(child, indent+1)
	}
}

// findWaitingHumanNodes 递归收集所有 WaitingHuman 节点的 ID。
func findWaitingHumanNodes(node *tasknode.TaskNode) []string {
	var ids []string
	if node.Status == tasknode.WaitingHuman {
		ids = append(ids, node.ID)
	}
	for _, child := range node.Children {
		ids = append(ids, findWaitingHumanNodes(child)...)
	}
	return ids
}

// promptHumanResponse 从 stdin 读取人类回复（用于 --resume 流程）。
func promptHumanResponse(nodeID string) *tasknode.HumanResponse {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("🤚 Resuming node [%s] — enter your response:\n> ", nodeID)
	value, _ := reader.ReadString('\n')
	value = strings.TrimSpace(value)
	fmt.Print("Optional note (press Enter to skip): ")
	note, _ := reader.ReadString('\n')
	note = strings.TrimSpace(note)
	return &tasknode.HumanResponse{Value: value, Note: note, Timestamp: time.Now().Unix()}
}
