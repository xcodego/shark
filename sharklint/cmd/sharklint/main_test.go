package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRun 验证参数错误、违规、目录缺失与通过时的退出码和输出。
func TestRun(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.go")
	if err := os.WriteFile(file, []byte("package main\nfunc main() { go f() }\n"), 0644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		args []string
		code int
		want string
	}{
		{"usage", nil, 2, "用法:"},
		{"violation", []string{dir}, 1, file + ":2:15:"},
		{"missing", []string{filepath.Join(dir, "missing")}, 1, "检查目录"},
		{"clean", []string{t.TempDir()}, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			if code := run(tc.args, &stderr); code != tc.code {
				t.Errorf("退出码为 %d，期望为 %d", code, tc.code)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Errorf("输出为 %q，应当包含 %q", stderr.String(), tc.want)
			}
		})
	}
}

// TestRunRedisKeys 验证入口会运行 Redis KEYS 命令检查。
func TestRunRedisKeys(t *testing.T) {
	var stderr bytes.Buffer
	if code := run([]string{filepath.Join("..", "..", "redis", "testdata", "real")}, &stderr); code != 1 {
		t.Errorf("退出码为 %d，期望为 1", code)
	}

	if !strings.Contains(stderr.String(), "禁止使用 Redis KEYS 命令") {
		t.Errorf("输出应包含 Redis KEYS 违规，实际得到 %q", stderr.String())
	}
}

// TestRunSQL 验证入口会运行 SQL 使用规范检查。
func TestRunSQL(t *testing.T) {
	var stderr bytes.Buffer
	if code := run([]string{filepath.Join("..", "..", "sql", "testdata", "real")}, &stderr); code != 1 {
		t.Errorf("退出码为 %d，期望为 1", code)
	}

	if !strings.Contains(stderr.String(), "禁止直接使用或拼接 SQL") ||
		!strings.Contains(stderr.String(), "app/gconst") {
		t.Errorf("输出应包含 SQL 和字段名违规，实际得到 %q", stderr.String())
	}
}

// TestRunAny 验证入口会运行 interface{} 检查。
func TestRunAny(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.go")
	if err := os.WriteFile(file, []byte("package main\ntype Empty interface{}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	if code := run([]string{dir}, &stderr); code != 1 {
		t.Errorf("退出码为 %d，期望为 1", code)
	}
	if !strings.Contains(stderr.String(), "禁止使用 interface{}，请使用 any") {
		t.Errorf("输出应包含空接口违规，实际得到 %q", stderr.String())
	}
}

// TestRunPanic 验证入口会运行 panic 检查, 且 main 函数内的 panic 不报告。
func TestRunPanic(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/project\n\ngo 1.26\n"), 0644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "main.go")
	if err := os.WriteFile(file, []byte(`package main

func main() {
	panic("allowed")
}

func helper() {
	panic("bad")
}
`), 0644); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	if code := run([]string{dir}, &stderr); code != 1 {
		t.Errorf("退出码为 %d，期望为 1", code)
	}
	if !strings.Contains(stderr.String(), "禁止直接调用 panic") ||
		!strings.Contains(stderr.String(), file+":8:2:") {
		t.Errorf("输出应包含 main 辅助函数中的 panic 违规及位置，实际得到 %q", stderr.String())
	}
	if strings.Contains(stderr.String(), file+":4:") {
		t.Errorf("main 函数中的 panic 不应违规，实际得到 %q", stderr.String())
	}
}

// TestRunMapRace 验证入口会运行异步闭包 map 访问检查。
func TestRunMapRace(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/project\n\ngo 1.26\n"), 0644); err != nil {
		t.Fatal(err)
	}

	file := filepath.Join(dir, "main.go")
	if err := os.WriteFile(file, []byte("package example\nfunc run() {\n m := map[string]int{}\n go func() { m[\"x\"] = 1 }()\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if code := run([]string{dir}, &stderr); code != 1 {
		t.Errorf("退出码为 %d，期望为 1", code)
	}
	if !strings.Contains(stderr.String(), "map 可能被并发访问且未使用同一互斥锁保护") {
		t.Errorf("输出应包含 map 并发访问违规，实际得到 %q", stderr.String())
	}
}

// TestRunSyncPair 验证入口会运行同步操作配对检查。
func TestRunSyncPair(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/project\n\ngo 1.26\n"), 0644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "main.go")
	if err := os.WriteFile(file, []byte("package example\nimport \"sync\"\nfunc run() {\n var mu sync.Mutex\n mu.Lock()\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if code := run([]string{dir}, &stderr); code != 1 {
		t.Errorf("退出码为 %d，期望为 1", code)
	}
	if !strings.Contains(stderr.String(), file+":5:") ||
		!strings.Contains(stderr.String(), "Mutex 存在退出路径未配对的同步操作") {
		t.Errorf("输出应包含同步操作违规，实际得到 %q", stderr.String())
	}
}

// TestRunFormat 验证入口会运行 gofmt 格式检查。
func TestRunFormat(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.go")
	if err := os.WriteFile(file, []byte("package main\nfunc main( ){ }\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	if code := run([]string{dir}, &stderr); code != 1 {
		t.Errorf("退出码为 %d，期望为 1", code)
	}
	if !strings.Contains(stderr.String(), "Go 文件未通过 gofmt 格式化") {
		t.Errorf("输出应包含 gofmt 违规，实际得到 %q", stderr.String())
	}
}

// TestRunVet 验证入口会运行 go vet, 且诊断可定位到文件。
func TestRunVet(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/project\n\ngo 1.26\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nimport \"fmt\"\nfunc main() { fmt.Printf(\"%d\", \"text\") }\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	if code := run([]string{dir}, &stderr); code != 1 {
		t.Errorf("退出码为 %d，期望为 1", code)
	}
	if !strings.Contains(stderr.String(), filepath.Join(dir, "main.go")+":3:") ||
		strings.Contains(stderr.String(), "go vet ./... 检查") ||
		!strings.Contains(stderr.String(), "format") {
		t.Errorf("输出应包含可定位到文件的 go vet 诊断，实际得到 %q", stderr.String())
	}
}

// TestRunUnreachableOnce 验证不可达代码只被报告一次（vet 与 unreachable 检查不重复）。
func TestRunUnreachableOnce(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/project\n\ngo 1.26\n"), 0644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "main.go")
	if err := os.WriteFile(file, []byte(`package main
import "fmt"
func NewBinlogEvent(table any, data map[string]any) {
	fmt.Println("%v")
	return
	fmt.Println("table:", table, "data:", data)
}
`), 0644); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	if code := run([]string{dir}, &stderr); code != 1 {
		t.Errorf("退出码为 %d，期望为 1", code)
	}
	output := stderr.String()
	if count := strings.Count(output, "unreachable code"); count != 1 {
		t.Errorf("不可达代码应只报告一次，实际报告 %d 次：%q", count, output)
	}
	if !strings.Contains(output, file+":6:") {
		t.Errorf("应报告不可达语句所在行，实际得到 %q", output)
	}
}

// TestRunRedisCommands 验证入口会运行 Redis 禁用命令检查。
func TestRunRedisCommands(t *testing.T) {
	var stderr bytes.Buffer
	if code := run([]string{filepath.Join("..", "..", "redis", "testdata", "realcommands")}, &stderr); code != 1 {
		t.Errorf("退出码为 %d，期望为 1", code)
	}
	if !strings.Contains(stderr.String(), "禁止使用 Redis FLUSHALL 命令") ||
		!strings.Contains(stderr.String(), "禁止使用 Redis CLIENT PAUSE 命令") {
		t.Errorf("输出应包含 Redis 禁用命令，实际得到 %q", stderr.String())
	}
}
