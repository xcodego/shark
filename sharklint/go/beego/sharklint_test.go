package beego_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xcodego/shark/sharklint/go/beego"
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

func TestLintBansBeegoImports(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.go"), `package main
import (
	old "github.com/astaxie/beego"
	"github.com/beego/beego/v2/client/orm"
	"github.com/beego/bee/v2"
)
`)
	writeFile(t, filepath.Join(dir, "main_test.go"), `package main
import "github.com/beego/beego/v2"
`)

	err := beego.Lint(dir)
	if err == nil {
		t.Fatal("应当报告 Beego 导入")
	}
	var violation beego.Violation
	if !errors.As(err, &violation) {
		t.Fatalf("错误中应当包含 Violation: %v", err)
	}
	for _, want := range []string{
		"main.go:3:", "main.go:4:", "github.com/astaxie/beego",
		"github.com/beego/beego/v2/client/orm",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("应报告 %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "main_test.go") {
		t.Errorf("不应检查测试文件: %v", err)
	}
	if strings.Contains(err.Error(), "/bee/v2") {
		t.Errorf("不应误报 Beego CLI 包: %v", err)
	}
}

func TestLintCleanAndErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.go"), `package main
import "github.com/beego/bee/v2"
`)
	if err := beego.Lint(dir); err != nil {
		t.Fatalf("出现意外错误: %v", err)
	}

	writeFile(t, filepath.Join(dir, "broken.go"), "package main\nimport {\n")
	if err := beego.Lint(dir); err == nil || !strings.Contains(err.Error(), "解析") {
		t.Fatalf("应当报告语法错误，实际得到 %v", err)
	}
	if err := beego.Lint(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("应当报告目录不存在，实际得到 %v", err)
	}
	if err := beego.Lint(filepath.Join(dir, "main.go")); err == nil || !strings.Contains(err.Error(), "不是目录") {
		t.Fatalf("应当报告目录错误，实际得到 %v", err)
	}
}
