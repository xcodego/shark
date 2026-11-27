package goroutine_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xcodego/shark/sharklint/go/goroutine"
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

// TestCheckDir 验证非测试文件中的 go 语句被报告, 测试文件、注释、字符串和非 .go 文件不报告。
func TestCheckDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "safe.go"), `package example
// go ignored()
var text = "go ignored()"
func safe() { app.Go(func(){}) }
`)
	writeFile(t, filepath.Join(dir, "nested", "worker_test.go"), `//go:build never

package example
func test() {
	go worker()
	go func() { go worker() }()
}
`)
	writeFile(t, filepath.Join(dir, "ignored.txt"), "go worker()")

	err := goroutine.Lint(dir)
	if err != nil {
		t.Fatalf("测试文件中的违规应当被忽略，实际得到 %v", err)
	}

	writeFile(t, filepath.Join(dir, "nested", "worker.go"), `package example
func worker() {
	go run()
}
`)
	err = goroutine.Lint(dir)
	if err == nil || !strings.Contains(err.Error(), "worker.go:3:2") {
		t.Fatalf("应当报告非测试文件中的违规，实际得到 %v", err)
	}
	var violation goroutine.Violation
	if !errors.As(err, &violation) {
		t.Errorf("错误 %v 中应当包含 Violation", err)
	}
}

// TestCheckDirCleanAndSymlinks 验证符号链接指向的违规代码不被检查。
func TestCheckDirCleanAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "safe.go"), "package example\nfunc safe() {}\n")
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "bad.go"), "package example\nfunc bad() { go f() }\n")
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "bad.go"), filepath.Join(dir, "link.go")); err != nil {
		t.Fatal(err)
	}
	if err := goroutine.Lint(dir); err != nil {
		t.Fatalf("出现意外错误: %v", err)
	}
}

// TestForceIgnoreComment 验证 goroutine 豁免注释生效, 其他检查器的豁免注释不生效。
func TestForceIgnoreComment(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "worker.go"), `package example
func worker() {
	// force		ignore sharklint   :	goroutine
	go ignored()

	// force ignore sharklint : other
	go reported()
}
`)

	err := goroutine.Lint(dir)
	if err == nil || !strings.Contains(err.Error(), "worker.go:7:2") || strings.Contains(err.Error(), "worker.go:4:2") {
		t.Fatalf("应当只报告没有匹配强制忽略注释的语句，实际得到 %v", err)
	}
}

// TestCheckDirErrors 验证解析错误、目录不存在和非目录的错误报告。
func TestCheckDirErrors(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.go")
	writeFile(t, bad, "package example\nfunc broken( {\n")
	writeFile(t, filepath.Join(dir, "violation.go"), "package example\nfunc bad() { go f() }\n")
	err := goroutine.Lint(dir)
	if err == nil || !strings.Contains(err.Error(), "解析") || !strings.Contains(err.Error(), "禁止直接使用 go 启动协程") {
		t.Fatalf("应当同时报告解析错误和违规，实际得到 %v", err)
	}
	if err := goroutine.Lint(bad); err == nil || !strings.Contains(err.Error(), "不是目录") {
		t.Fatalf("应当报告目录错误，实际得到 %v", err)
	}
	if err := goroutine.Lint(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("应当报告目录不存在，实际得到 %v", err)
	}
}
