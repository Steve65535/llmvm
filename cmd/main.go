// Command llmvm 是 LLMVM 的 CLI 入口。
//
// main.go 只做编排：读 flag → 初始化 engine + state → 启动 Runtime → 落盘。
// 各步骤的细节散在同包的其他文件：
//
//	state.go   — SaveState 序列化与 .json/.sqlite 路径派生
//	engine.go  — LLM 引擎选择（API / Stub）
//	signals.go — Ctrl+C / SIGTERM 紧急保存
//	human.go   — WaitingHuman 节点扫描 + stdin 读人类回复
//	tree.go    — 最终任务树打印
package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/Steve65535/llmvm/pkg/artifact"
	"github.com/Steve65535/llmvm/pkg/runtime"
	"github.com/Steve65535/llmvm/pkg/tasknode"
)

func main() {
	savePath := flag.String("save", "", "Path to save execution state (JSON)")
	loadPath := flag.String("load", "", "Path to load execution state (JSON)")
	resumeNode := flag.String("resume", "", "Node ID to resume (inject human response for WaitingHuman node)")
	flag.Parse()

	engine := initEngine()

	root, savedArtifacts, initialRequest := obtainRoot(*loadPath)

	rt := runtime.NewRuntime(engine, root, derivedSQLitePath(*savePath, *loadPath))
	if savedArtifacts != nil {
		rt.SetArtifacts(savedArtifacts)
		fmt.Println("✅ Artifact store restored")
	}

	// --load 后从 AST 重建 SQLite 索引，防止 .sqlite 丢失或过期。
	if *loadPath != "" {
		fmt.Println("🔄 Rebuilding SQLite index from loaded state...")
		if err := rt.RebuildIndexFromAST(); err != nil {
			fmt.Printf("⚠️  SQLite rebuild failed: %v (continuing with AST fallback)\n", err)
		} else {
			fmt.Println("✅ SQLite index rebuilt")
		}
	}

	if !handleResume(rt, root, *resumeNode, *loadPath) {
		return // resume 流程中已经决定退出（例如发现 WaitingHuman 等待用户）
	}

	// 增量保存 + 紧急保存
	if *savePath != "" {
		rt.OnStepComplete = func(node *tasknode.TaskNode) {
			fmt.Printf("💾 Autosaving state to %s...\n", *savePath)
			persistState(*savePath, root, rt.GetArtifacts())
		}
	}
	installEmergencySaveHandler(*savePath, root, rt.GetArtifacts)

	fmt.Println("🌳 Starting execution...")
	if err := rt.Execute(initialRequest); err != nil {
		fmt.Printf("❌ Execution error: %v\n", err)
	}

	if *savePath != "" {
		fmt.Printf("💾 Saving state to %s...\n", *savePath)
		persistState(*savePath, root, rt.GetArtifacts())
		fmt.Println("✅ State saved successfully")
	}

	fmt.Println()
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("✅ Final Syntax Tree:")
	fmt.Println(strings.Repeat("=", 60))
	printTree(root, 0)
}

// obtainRoot 根据 flag/args 决定是 --load 恢复还是新建 root。
//
// 返回 (root, 已保存的 artifact store 或 nil, 用户的初始请求字符串)。
func obtainRoot(loadPath string) (*tasknode.TaskNode, *artifact.Store, string) {
	if loadPath != "" {
		root, arts, initialRequest := loadState(loadPath)
		return root, arts, initialRequest
	}

	command := readCommandFromArgs()
	if command == "" {
		os.Exit(0)
	}

	root := tasknode.NewTaskNode("root", "Root Task", tasknode.Normal, []string{command})
	root.SetStatus(tasknode.Running)
	return root, nil, command
}

// readCommandFromArgs 从命令行参数或交互模式读取用户的初始请求。
func readCommandFromArgs() string {
	args := flag.Args()
	if len(args) > 0 {
		command := strings.Join(args, " ")
		fmt.Printf("📝 Command from arguments: %s\n\n", command)
		return command
	}

	fmt.Println("🚀 LLMVM - Advanced Agent Runtime")
	fmt.Println("==========================================")
	fmt.Println("Enter your command (or 'exit' to quit):")
	fmt.Print("> ")

	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		log.Fatalf("Failed to read input: %v", err)
	}

	command := strings.TrimSpace(input)
	if command == "" || strings.ToLower(command) == "exit" {
		return ""
	}
	return command
}

// handleResume 处理 --resume 流程，返回 false 表示 main 应该立即退出。
//
// 三种情况：
//  1. 显式 --resume <id>：注入人类回复 → 继续 Execute（返回 true）
//  2. --load 且树中有 WaitingHuman：提示用户用 --resume，退出（返回 false）
//  3. 其它：直接进 Execute（返回 true）
func handleResume(rt *runtime.Runtime, root *tasknode.TaskNode, resumeNode, loadPath string) bool {
	if resumeNode != "" {
		resp := promptHumanResponse(resumeNode)
		if err := rt.ResumeWithHumanResponse(resumeNode, resp); err != nil {
			log.Fatalf("❌ Resume failed: %v", err)
		}
		fmt.Printf("✅ Injected human response for node [%s]\n", resumeNode)
		return true
	}

	if loadPath == "" {
		return true
	}

	waiting := findWaitingHumanNodes(root)
	if len(waiting) == 0 {
		return true
	}
	fmt.Printf("⏸️  Found %d WaitingHuman node(s): %s\n", len(waiting), strings.Join(waiting, ", "))
	fmt.Printf("   Use --resume <node-id> to inject a human response and continue.\n")
	return false
}
