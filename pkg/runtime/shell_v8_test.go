package runtime

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Steve65535/llmvm/pkg/llm"
	"github.com/Steve65535/llmvm/pkg/tasknode"
)

func TestShellPiping(t *testing.T) {
	// Create a dummy root node
	root := tasknode.NewTaskNode("root", "Shell Test", tasknode.Normal, []string{"Run a piping test"})

	// Create runtime with a stub engine (we won't actually call the engine for handleCLI)
	rt := NewRuntime(&llm.StubEngine{}, root)

	// Test a complex piping command
	// We'll create a temp file, grep it, count lines
	tmpFile := "shell_test_v8.txt"
	defer os.Remove(tmpFile)

	content := "line1\nline2\nline3\nmatch_this\nline5"
	err := os.WriteFile(tmpFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	// Command: cat <file> | grep "match" | wc -l
	cmd := fmt.Sprintf("cat %s | grep \"match\" | wc -l", tmpFile)
	result, err := rt.handleCLI(cmd)
	if err != nil {
		t.Fatalf("handleCLI failed: %v", err)
	}

	fmt.Printf("Shell result: [%s]\n", result)
	if !strings.Contains(strings.TrimSpace(result), "1") {
		t.Errorf("Expected result to contain '1', got '%s'", result)
	}
}
