package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Steve65535/llmvm/pkg/tasknode"
)

// findWaitingHumanNodes 递归收集所有 WaitingHuman 节点的 ID。
//
// 用于 --load 自动检测：发现等待中的节点时提示用户用 --resume 恢复。
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
//
// 读两次 stdin：value 必填，note 可选回车跳过。timestamp 落到 unix epoch。
func promptHumanResponse(nodeID string) *tasknode.HumanResponse {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("🤚 Resuming node [%s] — enter your response:\n> ", nodeID)
	value, _ := reader.ReadString('\n')
	value = strings.TrimSpace(value)

	fmt.Print("Optional note (press Enter to skip): ")
	note, _ := reader.ReadString('\n')
	note = strings.TrimSpace(note)

	return &tasknode.HumanResponse{
		Value:     value,
		Note:      note,
		Timestamp: time.Now().Unix(),
	}
}
