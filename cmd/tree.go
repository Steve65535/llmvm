package main

import (
	"fmt"

	"github.com/Steve65535/llmvm/pkg/tasknode"
)

// printTree 递归打印任务树到 stdout（最终结果展示）。
//
// 在 main 退出前调用一次；不参与运行时调度。
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
