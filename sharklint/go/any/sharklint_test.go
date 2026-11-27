package any_test

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/xcodego/shark/sharklint/go/any"
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

// TestLint 验证空接口的报告范围: 跳过测试文件, 包含被构建标签排除的文件。
func TestLint(t *testing.T) {
	dir := t.TempDir()
	source := `package example
type Empty interface{} // want
type EmptyAlias = interface { } // want
type Reader interface { Read([]byte) (int, error) }
type Constraint interface { ~int | ~string }
var value any
// interface{} in a comment is not a type.
`
	writeFile(t, filepath.Join(dir, "types.go"), source)
	writeFile(t, filepath.Join(dir, "types_test.go"), "package example\ntype Ignored interface{}\n")
	writeFile(t, filepath.Join(dir, "excluded.go"), `//go:build never
package example
type AlsoIgnored interface{}
`)

	err := any.Lint(dir)
	if err == nil {
		t.Fatal("应当报告空接口")
	}
	var violation any.Violation
	if !errors.As(err, &violation) {
		t.Fatalf("错误中应当包含 Violation: %v", err)
	}

	for i, line := range strings.Split(source, "\n") {
		location := filepath.Join(dir, "types.go") + ":" + strconv.Itoa(i+1) + ":"
		if strings.Contains(line, "// want") {
			if !strings.Contains(err.Error(), location) {
				t.Errorf("应报告 %s: %v", location, err)
			}
		} else if strings.Contains(err.Error(), location) {
			t.Errorf("不应报告 %s: %v", location, err)
		}
	}
	if strings.Contains(err.Error(), "types_test.go") {
		t.Errorf("不应报告测试文件: %v", err)
	}
	if !strings.Contains(err.Error(), "excluded.go:3:") {
		t.Errorf("应报告被构建条件排除的文件: %v", err)
	}
}

// TestForceIgnoreComment 验证只有 any 豁免注释能豁免空接口。
func TestForceIgnoreComment(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "types.go"), `package example
// force ignore sharklint: any
var ignored interface{}

// force ignore sharklint: sql
var reported interface{}
`)

	err := any.Lint(dir)
	if err == nil || !strings.Contains(err.Error(), "types.go:6:") || strings.Contains(err.Error(), "types.go:3:") {
		t.Fatalf("应当只报告未忽略的空接口，实际得到 %v", err)
	}
}

// TestLintErrors 验证解析错误、目录不存在和非目录的错误报告。
func TestLintErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "broken.go"), "package example\nfunc broken( {\n")
	writeFile(t, filepath.Join(dir, "violation.go"), "package example\ntype Empty interface{}\n")
	err := any.Lint(dir)
	if err == nil || !strings.Contains(err.Error(), "解析") || !strings.Contains(err.Error(), "禁止使用 interface{}") {
		t.Fatalf("应当同时报告解析错误和违规，实际得到 %v", err)
	}
	if err := any.Lint(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("应当报告目录不存在，实际得到 %v", err)
	}
	if err := any.Lint(filepath.Join(dir, "violation.go")); err == nil || !strings.Contains(err.Error(), "不是目录") {
		t.Fatalf("应当报告目录错误，实际得到 %v", err)
	}
}
