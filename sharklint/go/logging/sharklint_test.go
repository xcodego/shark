package logging_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xcodego/shark/sharklint/go/logging"
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

// TestLintReportsDisallowedLogging 验证直接打印、zap 全局 Logger 与直接创建 Logger 被报告。
func TestLintReportsDisallowedLogging(t *testing.T) {
	dir := t.TempDir()
	source := `package example
import (
	f "fmt"
	z "go.uber.org/zap"
)
func bad(logger *z.Logger) {
	f.Print("one")
	f.Println("two")
	f.Printf("three")
	f.Fprintln(nil, "four")
	z.L().Info("global")
	z.S().Info("global sugar")
	z.NewNop()
	z.NewProductionConfig().Build()
	z.Config{}.Build()
	logger.Info(f.Sprintf("safe formatting"))
}
`
	writeFile(t, filepath.Join(dir, "logging.go"), source)
	writeFile(t, filepath.Join(dir, "logging_test.go"), "package example\nfunc test() { println(\"ignored\") }\n")

	err := logging.Lint(dir)
	if err == nil {
		t.Fatal("应当报告不合规的日志调用")
	}
	var violation logging.Violation
	if !errors.As(err, &violation) {
		t.Fatalf("错误中应当包含 Violation: %v", err)
	}
	for _, want := range []string{
		"logging.go:7:", "logging.go:8:", "logging.go:9:", "logging.go:10:",
		"logging.go:11:", "logging.go:12:", "logging.go:13:", "logging.go:14:",
		"logging.go:15:",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("应报告 %s: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "logging_test.go") {
		t.Errorf("不应报告测试文件: %v", err)
	}
	if strings.Contains(err.Error(), "logging.go:16:") {
		t.Errorf("不应报告 fmt.Sprintf 或注入的 Logger: %v", err)
	}
}

// TestLintAliasesDotImportsAndFactory 验证别名、点导入、内置 print 的识别, 以及 sharklog.New 和 CLI 输出的例外。
func TestLintAliasesDotImportsAndFactory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "logging.go"), `package example
import . "fmt"
func bad() { println("builtin"); Println("dot import") }
`)
	writeFile(t, filepath.Join(dir, "sharklog", "logger.go"), `package sharklog
import "go.uber.org/zap"
func New() { zap.NewNop() }
func helper() { zap.NewNop() }
`)
	writeFile(t, filepath.Join(dir, "sharklint", "cmd", "sharklint", "main.go"), `package main
import "fmt"
func main() { fmt.Fprintln(nil, "command output") }
`)

	err := logging.Lint(dir)
	if err == nil || !strings.Contains(err.Error(), "logging.go:3:14:") ||
		!strings.Contains(err.Error(), "logging.go:3:34:") ||
		!strings.Contains(err.Error(), "logger.go:4:17:") {
		t.Fatalf("应当报告内置、点导入及工厂外的直接打印或 Logger 创建，实际得到 %v", err)
	}
	if strings.Contains(err.Error(), "logger.go:3:") || strings.Contains(err.Error(), "main.go") {
		t.Errorf("sharklog.New 可创建基础 Logger，CLI 输出不属于日志: %v", err)
	}
}

// TestLintRejectsLogAndSlogImports 验证 log、log/slog 导入被禁止, sharkdb 的 slog 适配文件除外。
func TestLintRejectsLogAndSlogImports(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "logging.go"), `package example
import (
	l "log"
	s "log/slog"
)
func report() {
	l.Printf("direct log")
	s.Info("direct slog")
}
`)
	writeFile(t, filepath.Join(dir, "sharkdb", "binloglistener.go"), `package sharkdb
import "log/slog"
var _ *slog.Logger
`)

	err := logging.Lint(dir)
	if err == nil || !strings.Contains(err.Error(), "logging.go:3:") ||
		!strings.Contains(err.Error(), "logging.go:4:") ||
		!strings.Contains(err.Error(), "禁止导入 log 或 log/slog") {
		t.Fatalf("应当禁止导入 log 和 log/slog，实际得到 %v", err)
	}
	if strings.Contains(err.Error(), "binloglistener.go") {
		t.Errorf("go-mysql 的 slog 兼容适配应当被允许: %v", err)
	}
}

// TestLintForceIgnoreAndErrors 验证豁免注释及各类错误的报告。
func TestLintForceIgnoreAndErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "logging.go"), `package example
import "fmt"
func ignored() {
	// force ignore sharklint: logging
	fmt.Println("ignored")
}
func reported() {
	fmt.Println("reported")
}
`)

	err := logging.Lint(dir)
	if err == nil || !strings.Contains(err.Error(), "logging.go:8:") ||
		strings.Contains(err.Error(), "logging.go:5:") {
		t.Fatalf("应当只报告未忽略的调用，实际得到 %v", err)
	}
	writeFile(t, filepath.Join(dir, "broken.go"), "package example\nfunc broken( {\n")
	err = logging.Lint(dir)
	if err == nil || !strings.Contains(err.Error(), "解析") ||
		!strings.Contains(err.Error(), "禁止使用 fmt.Print*") {
		t.Fatalf("应当同时报告解析错误和违规，实际得到 %v", err)
	}
	if err := logging.Lint(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("应当报告目录不存在，实际得到 %v", err)
	}
	if err := logging.Lint(filepath.Join(dir, "logging.go")); err == nil || !strings.Contains(err.Error(), "不是目录") {
		t.Fatalf("应当报告目录错误，实际得到 %v", err)
	}
}
