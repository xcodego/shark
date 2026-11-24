package maprace_test

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/xcodego/shark/sharklint/go/maprace"
)

func TestConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "go.mod"), "module example.com/test\n\ngo 1.26\n")
	write(t, filepath.Join(dir, "main.go"), `package example
import "sync"
func bad() {
	m := map[string]int{}
	go func() {
		m["x"] = 1 // bad-write
	}()
	_ = m["x"] // bad-read
}
func readonly() {
	m := map[string]int{}
	go func() { _ = m["x"] }()
	_ = m["x"]
}
func safe() {
	m := map[string]int{}
	var mu sync.RWMutex
	go func() {
		mu.Lock()
		defer mu.Unlock()
		m["x"] = 1 // safe-write
	}()
	mu.RLock()
	_ = m["x"] // safe-read
	mu.RUnlock()
}
func differentLocks() {
	m := map[string]int{}
	var a, b sync.Mutex
	go func() { a.Lock(); m["x"] = 1; a.Unlock() }() // other-write
	b.Lock()
	delete(m, "x") // other-delete
	b.Unlock()
}
func partialLock(cond bool) {
	m := map[string]int{}
	var mu sync.Mutex
	go func() {
		if cond { mu.Lock() }
		m["x"]++ // conditional
	}()
}
func local() {
	go func() {
		m := map[string]int{}
		m["x"] = 1 // local
	}()
}
func ignore() {
	m := map[string]int{}
	go func() {
		// force ignore sharklint: maprace
		m["x"] = 1 // ignored
	}()
}
func rangeMap() {
	m := map[string]int{}
	go func() { m["x"] = 1 }() // range-write
	for range m {} // range-read
}
func readLockWrite() {
	m := map[string]int{}
	var mu sync.RWMutex
	go func() {
		mu.RLock()
		m["x"] = 1 // rlock-write
		mu.RUnlock()
	}()
}
func unlockedReader() {
	m := map[string]int{}
	var mu sync.Mutex
	go func() {
		mu.Lock()
		m["x"] = 1 // locked-write
		mu.Unlock()
	}()
	_ = len(m) // unlocked-len
}
func twoClosures() {
	m := map[string]int{}
	go func() { _ = m["x"] }() // async-read
	go func() { clear(m) }() // async-clear
}
func beforeLaunch() {
	m := map[string]int{}
	m["x"] = 1 // before-launch
	go func() { _ = m["x"] }()
}
func fields() {
	var s struct {
		m map[string]int
		mu sync.Mutex
	}
	go func() {
		s.mu.Lock()
		s.m["x"] = 1 // field-write
		s.mu.Unlock()
	}()
	s.mu.Lock()
	_ = s.m["x"] // field-read
	s.mu.Unlock()
}
`)
	err := maprace.Lint(dir)
	if err == nil {
		t.Fatal("应报告并发 map 访问")
	}
	var violation maprace.Violation
	if !errors.As(err, &violation) {
		t.Fatalf("应包含 Violation: %v", err)
	}
	source, errRead := os.ReadFile(filepath.Join(dir, "main.go"))
	if errRead != nil {
		t.Fatal(errRead)
	}
	lines := strings.Split(string(source), "\n")
	for i, line := range lines {
		if !strings.Contains(line, "// ") {
			continue
		}
		marker := strings.TrimSpace(strings.SplitN(line, "// ", 2)[1])
		if marker == "force ignore sharklint: maprace" {
			continue
		}
		want := map[string]bool{"bad-write": true, "bad-read": true, "other-write": true,
			"other-delete": true, "conditional": true, "range-write": true, "range-read": true,
			"rlock-write": true, "locked-write": true, "unlocked-len": true,
			"async-read": true, "async-clear": true}[marker]
		got := strings.Contains(err.Error(), filepath.Join(dir, "main.go")+fmtLine(i+1))
		if got != want {
			t.Errorf("%s: got diagnostic %v, want %v; errors: %v", marker, got, want, err)
		}
	}
}

func fmtLine(line int) string {
	return ":" + strconv.Itoa(line) + ":"
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestErrors(t *testing.T) {
	dir := t.TempDir()
	if err := maprace.Lint(dir); err != nil {
		t.Fatal(err)
	}
	if err := maprace.Lint(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("应报告不存在的目录: %v", err)
	}
}

func TestSafeGo(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "go.mod"), "module github.com/xcodego/shark\n\ngo 1.26\n")
	if err := os.Mkdir(filepath.Join(dir, "sharkapp"), 0755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "sharkapp", "app.go"), "package sharkapp\ntype App struct{}\nfunc (*App) SafeGo(fn func(...any)) {}\n")
	write(t, filepath.Join(dir, "main.go"), "package example\nimport \"github.com/xcodego/shark/sharkapp\"\nfunc run(app *sharkapp.App) {\n m := map[string]int{}\n app.SafeGo(func(...any) { m[\"x\"] = 1 })\n}\n")
	err := maprace.Lint(dir)
	if err == nil || !strings.Contains(err.Error(), "main.go:5:") {
		t.Fatalf("应检查 SafeGo 闭包: %v", err)
	}
}

func TestLoadError(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "go.mod"), "module example.com/bad\n\ngo 1.26\n")
	write(t, filepath.Join(dir, "bad.go"), "package bad\nfunc broken() { go func() { m[ }() }\n")
	if err := maprace.Lint(dir); err == nil || !strings.Contains(err.Error(), "加载 Go 包失败") {
		t.Fatalf("应报告语法错误: %v", err)
	}
}
