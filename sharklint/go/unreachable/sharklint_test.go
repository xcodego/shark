package unreachable_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xcodego/shark/sharklint/go/unreachable"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLintReportsUnreachableCode(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/project\n\ngo 1.26\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main
func main() {
	return
	println("unreachable")
}
`)

	err := unreachable.Lint(dir)
	if err == nil || !strings.Contains(err.Error(), filepath.Join(dir, "main.go")+":4:") ||
		!strings.Contains(err.Error(), "unreachable code") {
		t.Fatalf("应当报告不可达代码及其位置，实际得到 %v", err)
	}
}

func TestLintCleanAndErrors(t *testing.T) {
	if err := unreachable.Lint(t.TempDir()); err != nil {
		t.Fatalf("空目录不应报错，实际得到 %v", err)
	}

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/project\n\ngo 1.26\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package main\nfunc main() {}\n")
	if err := unreachable.Lint(dir); err != nil {
		t.Fatalf("没有不可达代码的 Go 包不应报错，实际得到 %v", err)
	}
	if err := unreachable.Lint(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("应当报告目录不存在，实际得到 %v", err)
	}
	if err := unreachable.Lint(filepath.Join(dir, "main.go")); err == nil || !strings.Contains(err.Error(), "不是目录") {
		t.Fatalf("应当报告目录错误，实际得到 %v", err)
	}
}
