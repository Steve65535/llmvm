package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/Steve65535/llmvm/pkg/llm"
	"github.com/Steve65535/llmvm/pkg/runtime"
	"github.com/Steve65535/llmvm/pkg/tasknode"
)

func main() {
	// 1. 初始化 LLM 引擎
	var engine llm.Engine
	apiEngine, err := llm.NewDeepSeekEngine()
	if err != nil {
		// 如果 DeepSeek 不可用，使用 StubEngine 进行测试
		fmt.Println("⚠️  Warning: DeepSeek API not available, using StubEngine for testing")
		fmt.Println("   Set DEEPSEEK_API_KEY environment variable to use DeepSeek API")
		fmt.Println()
		engine = &llm.StubEngine{}
	} else {
		fmt.Println("✅ DeepSeek API initialized successfully")
		fmt.Println()
		engine = apiEngine
	}

	// 2. 获取用户命令
	var command string
	if len(os.Args) > 1 {
		// 从命令行参数获取命令
		command = strings.Join(os.Args[1:], " ")
		fmt.Printf("📝 Command from arguments: %s\n\n", command)
	} else {
		// 交互式输入命令
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
		if command == "" {
			fmt.Println("No command provided. Exiting.")
			return
		}
		if strings.ToLower(command) == "exit" {
			fmt.Println("Goodbye!")
			return
		}
		fmt.Println()
	}

	// 3. 创建根节点（根据用户命令）
	root := tasknode.NewTaskNode("root", "Root Task", tasknode.Normal, []string{command})
	root.SetStatus(tasknode.Running)

	// 4. 创建运行时
	rt := runtime.NewRuntime(engine, root)

	// 5. 执行深度优先搜索，构建语法树
	fmt.Println("🌳 Starting syntax tree construction...")
	fmt.Printf("📋 Initial command: %s\n\n", command)
	fmt.Println("⏳ Executing depth-first search...")
	fmt.Println()

	if err := rt.Execute(command); err != nil {
		log.Fatalf("❌ Execution failed: %v", err)
	}

	// 6. 打印结果树
	fmt.Println()
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("✅ Execution Complete - Syntax Tree:")
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
