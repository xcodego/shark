package format_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xcodego/shark/sharklint/go/format"
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

// TestLint 验证未格式化的文件（含测试文件和被构建标签排除的文件）被报告。
func TestLint(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "clean.go"), "package example\n\nfunc clean() {}\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package example\nfunc main( ){ }\n")
	writeFile(t, filepath.Join(dir, "main_test.go"), "package example\nfunc test( ){ }\n")
	writeFile(t, filepath.Join(dir, "excluded.go"), "//go:build never\n\npackage example\nfunc excluded( ){ }\n")

	err := format.Lint(dir)
	if err == nil {
		t.Fatal("应当报告未格式化的 Go 文件")
	}
	var violation format.Violation
	if !errors.As(err, &violation) {
		t.Fatalf("错误中应当包含 Violation: %v", err)
	}
	for _, file := range []string{"main.go:2:", "main_test.go:2:", "excluded.go:4:"} {
		if !strings.Contains(err.Error(), file) {
			t.Errorf("应报告 %s: %v", file, err)
		}
	}
	if strings.Contains(err.Error(), "clean.go") {
		t.Errorf("不应报告已格式化文件: %v", err)
	}
}

// TestLintErrors 验证格式错误、目录不存在和非目录的错误报告。
func TestLintErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "broken.go"), "package example\nfunc broken( {\n")
	if err := format.Lint(dir); err == nil || !strings.Contains(err.Error(), "gofmt 检查") {
		t.Fatalf("应当报告格式错误，实际得到 %v", err)
	}
	if err := format.Lint(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("应当报告目录不存在，实际得到 %v", err)
	}
	if err := format.Lint(filepath.Join(dir, "broken.go")); err == nil || !strings.Contains(err.Error(), "不是目录") {
		t.Fatalf("应当报告目录错误，实际得到 %v", err)
	}
}
