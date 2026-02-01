package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Steve65535/llmvm/pkg/llm"
	"github.com/Steve65535/llmvm/pkg/tasknode"
)

// TestAppendToFileBasic 测试基本的文件追加功能
func TestAppendToFileBasic(t *testing.T) {
	// 创建临时目录
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")

	// 创建 Runtime
	root := tasknode.NewTaskNode("root", "Root", tasknode.Normal, []string{"Test"})
	engine := &MockEngine{}
	runtime := NewRuntime(engine, root)

	// 测试 1: 追加到新文件
	action1 := llm.Action{
		ActionType: "append_to_file",
		FilePath:   testFile,
		Content:    "First line\n",
	}

	err := runtime.executeAction(action1, root)
	if err != nil {
		t.Fatalf("Failed to append to new file: %v", err)
	}

	// 验证文件内容
	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}
	if string(content) != "First line\n" {
		t.Errorf("Expected 'First line\\n', got '%s'", string(content))
	}

	// 验证变量
	if root.Variables["last_file_written"] != testFile {
		t.Errorf("Expected last_file_written to be %s, got %v", testFile, root.Variables["last_file_written"])
	}
	if root.Variables["last_file_size"] != 11 {
		t.Errorf("Expected last_file_size to be 11, got %v", root.Variables["last_file_size"])
	}

	// 测试 2: 追加到现有文件
	action2 := llm.Action{
		ActionType: "append_to_file",
		FilePath:   testFile,
		Content:    "Second line\n",
	}

	err = runtime.executeAction(action2, root)
	if err != nil {
		t.Fatalf("Failed to append to existing file: %v", err)
	}

	// 验证文件内容
	content, err = os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}
	expected := "First line\nSecond line\n"
	if string(content) != expected {
		t.Errorf("Expected '%s', got '%s'", expected, string(content))
	}

	// 验证变量更新
	if root.Variables["last_file_size"] != 23 {
		t.Errorf("Expected last_file_size to be 23, got %v", root.Variables["last_file_size"])
	}
}

// TestAppendToFileMultiple 测试多次追加
func TestAppendToFileMultiple(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "report.md")

	root := tasknode.NewTaskNode("root", "Root", tasknode.Normal, []string{"Test"})
	engine := &MockEngine{}
	runtime := NewRuntime(engine, root)

	// 模拟树结构中的多个节点追加内容
	sections := []string{
		"# Report\n\n",
		"## Introduction\n\nThis is the introduction.\n\n",
		"## Analysis\n\nThis is the analysis.\n\n",
		"## Conclusion\n\nThis is the conclusion.\n",
	}

	for i, section := range sections {
		action := llm.Action{
			ActionType: "append_to_file",
			FilePath:   testFile,
			Content:    section,
		}

		err := runtime.executeAction(action, root)
		if err != nil {
			t.Fatalf("Failed to append section %d: %v", i, err)
		}
	}

	// 验证最终内容
	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	expected := "# Report\n\n## Introduction\n\nThis is the introduction.\n\n## Analysis\n\nThis is the analysis.\n\n## Conclusion\n\nThis is the conclusion.\n"
	if string(content) != expected {
		t.Errorf("Expected:\n%s\nGot:\n%s", expected, string(content))
	}
}

// TestAppendToFileDirectoryCreation 测试目录自动创建
func TestAppendToFileDirectoryCreation(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "subdir", "nested", "test.txt")

	root := tasknode.NewTaskNode("root", "Root", tasknode.Normal, []string{"Test"})
	engine := &MockEngine{}
	runtime := NewRuntime(engine, root)

	action := llm.Action{
		ActionType: "append_to_file",
		FilePath:   testFile,
		Content:    "Content in nested directory\n",
	}

	err := runtime.executeAction(action, root)
	if err != nil {
		t.Fatalf("Failed to append to file in nested directory: %v", err)
	}

	// 验证文件存在
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Errorf("File was not created in nested directory")
	}

	// 验证内容
	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}
	if string(content) != "Content in nested directory\n" {
		t.Errorf("Unexpected content: %s", string(content))
	}
}

// MockEngine for testing
type MockEngine struct{}

func (m *MockEngine) Call(prompt string) (*llm.Output, error) {
	return &llm.Output{Response: `{"actions": []}`}, nil
}

func (m *MockEngine) CallAsync(prompt string) <-chan *llm.Output {
	ch := make(chan *llm.Output, 1)
	ch <- &llm.Output{Response: `{"actions": []}`}
	close(ch)
	return ch
}
