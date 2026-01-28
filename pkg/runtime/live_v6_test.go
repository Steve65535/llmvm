package runtime

import (
	"fmt"
	"os"
	"testing"

	"github.com/Steve65535/llmvm/pkg/llm"
	"github.com/Steve65535/llmvm/pkg/tasknode"
)

func TestLiveTwoPassAttention(t *testing.T) {
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		t.Skip("DEEPSEEK_API_KEY not set")
	}

	engine, err := llm.NewDeepSeekEngine()
	if err != nil {
		t.Fatalf("Failed to create engine: %v", err)
	}

	// 构造一个需要跨分支信息感知的任务
	// 根任务：先在一个分支创建并写入一个密码，然后在另一个完全不同的分支尝试读取这个密码。
	// 密码不能通过父子传递（DFS 路径不同），必须通过 Global RAM。
	root := tasknode.NewTaskNode("root", "Cross-Branch RAM Test", tasknode.Normal, []string{
		"Phase 1: In branch A, generate a unique random string and store it in a variable named 'secret_token'. Mark the node as complete.",
		"Phase 2: In branch B, retrieve the 'secret_token' from the Global Workspace (RAM) and write it to a file 'verified.txt'.",
	})

	rt := NewRuntime(engine, root)

	err = rt.Execute("Demonstrate that branch B can see branch A's secret via the two-pass RAM selector.")
	if err != nil {
		t.Fatalf("Runtime execution failed: %v", err)
	}

	fmt.Println("\n--- FINAL VERIFICATION ---")
	fmt.Println(rt.formatGlobalWorkspace([]string{})) // 打印一下整棵树的索引情况查看 RAM 状态
}
