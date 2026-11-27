package panic_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xcodego/shark/sharklint/go/panic"
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

// TestLintReportsPanicCalls 验证非 main 函数中的 panic 被报告, main 函数、注释及 Logger.Panic 不报告。
func TestLintReportsPanicCalls(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "panic.go"), `package example
func bad() {
	panic("direct")
	((panic))("parenthesized")
}
func safe(logger interface{ Panic(...any) }) {
	logger.Panic("method")
}
// panic("comment")
var text = "panic(\"string\")"
`)
	writeFile(t, filepath.Join(dir, "panic_test.go"), "package example\nfunc test() { panic(\"ignored\") }\n")
	writeFile(t, filepath.Join(dir, "entry", "main.go"), `package main
func main() {
	panic("allowed")
	func() { panic("also allowed") }()
}
func helper() { panic("reported") }
`)

	err := panic.Lint(dir)
	if err == nil {
		t.Fatal("应当报告直接调用 panic")
	}
	var violation panic.Violation
	if !errors.As(err, &violation) {
		t.Fatalf("错误中应当包含 Violation: %v", err)
	}
	if !strings.Contains(err.Error(), "panic.go:3:2:") ||
		!strings.Contains(err.Error(), "panic.go:4:2:") {
		t.Errorf("应报告两处 panic 调用，实际得到 %v", err)
	}
	if strings.Contains(err.Error(), "panic_test.go") ||
		strings.Contains(err.Error(), "panic.go:7:") ||
		strings.Contains(err.Error(), "main.go:3:") ||
		strings.Contains(err.Error(), "main.go:4:") {
		t.Errorf("不应报告测试文件、方法调用或 main 函数中的 panic: %v", err)
	}
	if !strings.Contains(err.Error(), "main.go:6:17:") {
		t.Errorf("应报告 main 包其他函数中的 panic: %v", err)
	}
}

// TestLintForceIgnoreAndErrors 验证豁免注释及各类错误的报告。
func TestLintForceIgnoreAndErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "panic.go"), `package example
func ignored() {
	// force ignore sharklint: panic
	panic("ignored")
}
func reported() { panic("reported") }
`)

	err := panic.Lint(dir)
	if err == nil || !strings.Contains(err.Error(), "panic.go:6:19:") ||
		strings.Contains(err.Error(), "panic.go:4:") {
		t.Fatalf("应当只报告未忽略的 panic 调用，实际得到 %v", err)
	}

	writeFile(t, filepath.Join(dir, "broken.go"), "package example\nfunc broken( {\n")
	err = panic.Lint(dir)
	if err == nil || !strings.Contains(err.Error(), "解析") ||
		!strings.Contains(err.Error(), "禁止直接调用 panic") {
		t.Fatalf("应当同时报告解析错误和违规，实际得到 %v", err)
	}
	if err := panic.Lint(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("应当报告目录不存在，实际得到 %v", err)
	}
	if err := panic.Lint(filepath.Join(dir, "panic.go")); err == nil || !strings.Contains(err.Error(), "不是目录") {
		t.Fatalf("应当报告目录错误，实际得到 %v", err)
	}
}
