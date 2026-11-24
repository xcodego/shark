// Package unreachable 检查 Go 包中的不可达代码。
package unreachable

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var diagnosticPattern = regexp.MustCompile(`^(.+\.go):([0-9]+):([0-9]+):(.*)$`)

// Lint 在 dir 中运行 go vet 的 unreachable 分析器；空目录会跳过检查。
func Lint(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("检查目录 %q 失败: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("检查目录 %q 失败: 不是目录", dir)
	}

	hasGoFile := false
	err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 && filepath.Ext(path) == ".go" {
			hasGoFile = true
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("遍历 %q 失败: %w", dir, err)
	}
	if !hasGoFile {
		return nil
	}

	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("获取检查目录绝对路径失败: %w", err)
	}
	cmd := exec.Command("go", "vet", "-unreachable=true", "./...")
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	if message := strings.TrimSpace(string(output)); message != "" {
		return errors.New(absoluteDiagnostics(absDir, message))
	}
	return fmt.Errorf("运行 go vet -unreachable=true ./... 失败: %w", err)
}

func absoluteDiagnostics(dir, output string) string {
	lines := strings.Split(output, "\n")
	for i, line := range lines {
		match := diagnosticPattern.FindStringSubmatch(line)
		if match == nil || filepath.IsAbs(match[1]) {
			continue
		}
		match[1] = filepath.Join(dir, filepath.FromSlash(match[1]))
		lines[i] = match[1] + ":" + match[2] + ":" + match[3] + ":" + match[4]
	}
	return strings.Join(lines, "\n")
}
