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
	"strings"
	"syscall"

	"github.com/Steve65535/llmvm/pkg/llm"
	"github.com/Steve65535/llmvm/pkg/runtime"
	"github.com/Steve65535/llmvm/pkg/tasknode"
)

func main() {
	// 0. 解析命令行参数
	savePath := flag.String("save", "", "Path to save execution state (JSON)")
	loadPath := flag.String("load", "", "Path to load execution state (JSON)")
	flag.Parse()

	// 1. 初始化 LLM 引擎
	var engine llm.Engine
	apiEngine, err := llm.NewDeepSeekEngine()
	if err != nil {
		fmt.Println("⚠️  Warning: DeepSeek API not available, using StubEngine for testing")
		engine = &llm.StubEngine{}
	} else {
		fmt.Println("✅ DeepSeek API initialized successfully")
		engine = apiEngine
	}

	var root *tasknode.TaskNode
	var initialRequest string

	// 2. 加载或创建初始状态
	if *loadPath != "" {
		fmt.Printf("📂 Loading state from %s...\n", *loadPath)
		data, err := ioutil.ReadFile(*loadPath)
		if err != nil {
			log.Fatalf("❌ Failed to read save file: %v", err)
		}
		if err := json.Unmarshal(data, &root); err != nil {
			log.Fatalf("❌ Failed to unmarshal state: %v", err)
		}
		root.RestoreParents()
		initialRequest = root.Information[0] // 假设第一个 info 是原始请求
		fmt.Println("✅ State loaded successfully")
	} else {
		// 获取用户命令
		var command string
		args := flag.Args()
		if len(args) > 0 {
			command = strings.Join(args, " ")
			fmt.Printf("📝 Command from arguments: %s\n\n", command)
		} else {
			fmt.Println("🚀 LLMVM - Turing Complete Agent Runtime")
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
	rt := runtime.NewRuntime(engine, root)

	// 🆕 增量保存支持
	if *savePath != "" {
		rt.OnStepComplete = func(node *tasknode.TaskNode) {
			fmt.Printf("💾 Autosaving state to %s...\n", *savePath)
			data, _ := json.MarshalIndent(root, "", "  ")
			ioutil.WriteFile(*savePath, data, 0644)
		}
	}

	// 🆕 信号处理（Ctrl+C 自动保存）
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n⚠️  Interrupted. Performing emergency save...")
		if *savePath != "" {
			data, _ := json.MarshalIndent(root, "", "  ")
			ioutil.WriteFile(*savePath, data, 0644)
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
		data, _ := json.MarshalIndent(root, "", "  ")
		if err := ioutil.WriteFile(*savePath, data, 0644); err != nil {
			fmt.Printf("❌ Failed to save state: %v\n", err)
		} else {
			fmt.Println("✅ State saved successfully")
		}
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
	switch node.Type {
	case tasknode.Loop:
		nodeType = "Loop"
	case tasknode.Leaf:
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
