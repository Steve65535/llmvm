package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Steve65535/llmvm/pkg/artifact"
	"github.com/Steve65535/llmvm/pkg/tasknode"
)

// installEmergencySaveHandler 注册 Ctrl+C / SIGTERM 信号 handler。
//
// 收到信号时立即落盘 SaveState（如果 savePath 非空），然后 os.Exit(0)。
// 这是"长任务被人类打断"场景的兜底——保证 30 步进度不会因为按错键消失。
//
// 调用方持有的回调因为闭包捕获 root 指针，能拿到当前最新状态。
func installEmergencySaveHandler(savePath string, root *tasknode.TaskNode, getArts func() *artifact.Store) {
	if savePath == "" {
		return
	}
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n⚠️  Interrupted. Performing emergency save...")
		persistState(savePath, root, getArts())
		fmt.Printf("✅ State saved to %s. Exiting.\n", savePath)
		os.Exit(0)
	}()
}
