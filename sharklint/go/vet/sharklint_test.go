package vet_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xcodego/shark/sharklint/go/vet"
)

// writeFile 创建 path 的父目录并写入 content, 供测试构造源码文件。
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// TestLint 验证 go vet 诊断被报告且可定位到文件。
func TestLint(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/project\n\ngo 1.26\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main
import "fmt"
func main() { fmt.Printf("%d", "not a number") }
`)

	err := vet.Lint(dir)
	if err == nil || !strings.Contains(err.Error(), filepath.Join(dir, "main.go")+":3:") ||
		strings.Contains(err.Error(), "go vet ./... 检查") ||
		!strings.Contains(err.Error(), "format") {
		t.Fatalf("应当报告可定位到文件的 go vet 错误，实际得到 %v", err)
	}
}

// TestLintCleanAndErrors 验证无问题、无 Go 文件及各类错误的处理。
func TestLintCleanAndErrors(t *testing.T) {
	if err := vet.Lint(t.TempDir()); err != nil {
		t.Fatalf("空目录不应报错，实际得到 %v", err)
	}

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/project\n\ngo 1.26\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package main\nfunc main() {}\n")
	if err := vet.Lint(dir); err != nil {
		t.Fatalf("干净 Go 包不应报错，实际得到 %v", err)
	}
	if err := vet.Lint(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("应当报告目录不存在，实际得到 %v", err)
	}
	if err := vet.Lint(filepath.Join(dir, "main.go")); err == nil || !strings.Contains(err.Error(), "不是目录") {
		t.Fatalf("应当报告目录错误，实际得到 %v", err)
	}
}
