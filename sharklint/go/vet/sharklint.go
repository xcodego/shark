// Package vet 使用 go vet 检查 Go 包; unreachable 分析器由 unreachable 包单独负责。
package vet

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

// diagnosticPattern 匹配 go vet 输出中 "文件:行:列:消息" 形式的诊断行。
var diagnosticPattern = regexp.MustCompile(`^(.+\.go):([0-9]+):([0-9]+):(.*)$`)

// Lint 在 dir 中运行 go vet -unreachable=false ./...（关闭 unreachable 分析器, 避免与 unreachable 包重复报告）;
// dir 下没有 .go 文件时跳过检查。
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
	cmd := exec.Command("go", "vet", "-unreachable=false", "./...")
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	if message := strings.TrimSpace(string(output)); message != "" {
		return errors.New(absoluteDiagnostics(absDir, message))
	}
	return fmt.Errorf("运行 go vet -unreachable=false ./... 失败: %w", err)
}

// absoluteDiagnostics 把输出中相对路径的诊断行改写为基于 dir 的路径, 便于直接定位文件。
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
