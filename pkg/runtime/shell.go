package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// HandleCLI 在项目根目录执行 shell 命令，返回 stdout+stderr 合并输出。
//
// 真正的 sh -c 执行入口；execute_command / acceptance check_command 都走这里。
// 命令未沙盒化，是 LLMVM 的特权工具，需要靠 Position.Authority 做事前限制。
func (r *Runtime) HandleCLI(command string) (string, error) {
	if command == "" {
		return "", fmt.Errorf("empty command")
	}

	// sh -c 支持管道、重定向、子 shell；Windows 上需要替换为 cmd /c。
	cmd := exec.Command("sh", "-c", command)

	dir, _ := os.Getwd()
	cmd.Dir = dir

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	combined := stdout.String()
	if stderr.Len() > 0 {
		if combined != "" {
			combined += "\n"
		}
		combined += "STDERR: " + stderr.String()
	}

	if err != nil {
		return combined, fmt.Errorf("command execution failed: %w (output: %s)", err, combined)
	}
	if combined == "" {
		return "Command executed successfully (no output).", nil
	}
	return combined, nil
}

// sandboxPath 验证并解析路径，确保在 test/sandbox/ 内。
//
// 用 separator-aware 前缀匹配防止 "test/sandbox-evil/..." 这类绕过。
// read_file / write_file / list_dir / search / append_to_file 都必须先经过这里。
func sandboxPath(filePath string) (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get working directory: %w", err)
	}
	sandboxRoot := filepath.Join(wd, "test", "sandbox")

	var absPath string
	if filepath.IsAbs(filePath) {
		absPath = filepath.Clean(filePath)
	} else {
		absPath = filepath.Clean(filepath.Join(wd, filePath))
	}

	if absPath != sandboxRoot && !strings.HasPrefix(absPath, sandboxRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside sandbox (must be under test/sandbox/)", filePath)
	}
	return absPath, nil
}
