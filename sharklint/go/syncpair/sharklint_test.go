package syncpair_test

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/xcodego/shark/sharklint/go/syncpair"
)

// TestPairs 按源码中的行尾标记逐行校验锁与 WaitGroup 配对问题的报告与不报告。
func TestPairs(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "go.mod"), "module example.com/test\n\ngo 1.26\n")
	path := filepath.Join(dir, "main.go")
	source := `package example
import "sync"
func mutex(b bool) {
	var mu sync.Mutex
	mu.Lock() // missing-unlock
	if b { return }
	mu.Unlock()
}
func correct(b bool) {
	var mu sync.Mutex
	mu.Lock()
	defer mu.Unlock()
	if b { return }
}
func split(b bool) {
	var mu sync.Mutex
	mu.Lock()
	if b { mu.Unlock() } else { mu.Unlock() }
}
func tryLock() {
	var mu sync.Mutex
	if mu.TryLock() { defer mu.Unlock() }
}
func extra() {
	var mu sync.Mutex
	mu.Unlock() // extra-unlock
}
func rw() {
	var mu sync.RWMutex
	mu.RLock() // wrong-lock
	mu.Unlock() // wrong-unlock
}
func deferredTwice() {
	var mu sync.Mutex
	mu.Lock() // double-release
	defer mu.Unlock()
	mu.Unlock()
}
func doubleLock() {
	var mu sync.Mutex
	mu.Lock()
	mu.Lock() // double-lock
	mu.Unlock()
	mu.Unlock()
}
func panicPath(b bool) {
	var mu sync.Mutex
	mu.Lock() // panic-unlock
	if b { panic("error") }
	mu.Unlock()
}
func deferredPanic() {
	var mu sync.Mutex
	mu.Lock()
	defer mu.Unlock()
	panic("error")
}
func async(b bool) {
	var wg sync.WaitGroup
	wg.Add(1) // missing-done
	go func() {
		if b { return }
		wg.Done()
	}()
	wg.Wait()
}
func safe(b bool) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if b { return }
	}()
	wg.Wait()
}
func repeated(xs []int) {
	var wg sync.WaitGroup
	for range xs {
		wg.Add(1)
		go func() { defer wg.Done() }()
	}
	wg.Wait()
}
func direct(b bool) {
	var wg sync.WaitGroup
	wg.Add(2) // missing-one
	wg.Done()
	if b { wg.Done() }
}
func balancedCount() {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done() }()
	go func() { wg.Add(-1) }()
	wg.Wait()
}
func waitBeforeDone() {
	var wg sync.WaitGroup
	wg.Add(1) // late-done
	wg.Wait()
	wg.Done()
}
func workerExtra() {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { wg.Done(); wg.Done() }() // extra-worker-done
}
func workerWithoutAdd() {
	var wg sync.WaitGroup
	go func() { wg.Done() }() // worker-without-add
}
func extraDone() {
	var wg sync.WaitGroup
	wg.Done() // extra-done
}
func external(wg *sync.WaitGroup, mu *sync.Mutex) {
	wg.Done()
	mu.Unlock()
}
func delegated(wg *sync.WaitGroup) { wg.Done() }
func pass() {
	var wg sync.WaitGroup
	wg.Add(1)
	go delegated(&wg)
	wg.Wait()
}
func workerDelegates() {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { delegated(&wg) }()
	wg.Wait()
}
func workerAdds() {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { wg.Add(1); wg.Done(); wg.Done() }()
	wg.Wait()
}
func deferredClosure() {
	var wg sync.WaitGroup
	wg.Add(1)
	defer func() { wg.Done() }()
}
func ignore() {
	var mu sync.Mutex
	// force ignore sharklint: syncpair
	mu.Lock() // ignored
}
type other struct{}
func (other) Lock() {}
func unrelated() { var x other; x.Lock() }
`
	write(t, path, source)
	err := syncpair.Lint(dir)
	if err == nil {
		t.Fatal("应报告未配对的同步操作")
	}
	var violation syncpair.Violation
	if !errors.As(err, &violation) {
		t.Fatalf("应包含 Violation: %v", err)
	}
	want := map[string]bool{
		"missing-unlock": true, "extra-unlock": true, "wrong-lock": true,
		"wrong-unlock": true, "double-release": true, "missing-done": true,
		"missing-one": true, "extra-done": true, "extra-worker-done": true, "worker-without-add": true,
		"double-lock": true, "panic-unlock": true, "late-done": true,
	}
	expectedLines := make(map[int]bool)
	for i, line := range strings.Split(source, "\n") {
		if !strings.Contains(line, "// ") {
			continue
		}
		marker := strings.TrimSpace(strings.SplitN(line, "// ", 2)[1])
		if marker == "force ignore sharklint: syncpair" {
			continue
		}
		got := strings.Contains(err.Error(), path+":"+strconv.Itoa(i+1)+":")
		if got != want[marker] {
			t.Errorf("%s: got diagnostic %v, want %v; errors: %v", marker, got, want[marker], err)
		}
		if want[marker] {
			expectedLines[i+1] = true
		}
	}
	for line := range strings.Split(source, "\n") {
		if strings.Contains(err.Error(), path+":"+strconv.Itoa(line+1)+":") && !expectedLines[line+1] {
			t.Errorf("第 %d 行不应报告: %v", line+1, err)
		}
	}
}

// TestSafeGo 验证 sharkapp.SafeGo 启动的闭包与 go 语句同样被分析。
func TestSafeGo(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "go.mod"), "module github.com/xcodego/shark\n\ngo 1.26\n")
	if err := os.Mkdir(filepath.Join(dir, "sharkapp"), 0755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "sharkapp", "app.go"), "package sharkapp\ntype App struct{}\nfunc (*App) SafeGo(fn func()) {}\n")
	write(t, filepath.Join(dir, "main.go"), `package example
import ("sync"; "github.com/xcodego/shark/sharkapp")
func run(app *sharkapp.App, b bool) {
	var wg sync.WaitGroup
	wg.Add(1)
	app.SafeGo(func() { if b { return }; wg.Done() })
	wg.Wait()
}
`)
	err := syncpair.Lint(dir)
	if err == nil || !strings.Contains(err.Error(), "main.go:5:") {
		t.Fatalf("应检查 SafeGo 闭包中遗漏 Done 的路径: %v", err)
	}
}

// TestErrors 验证空目录通过, 以及目录不存在和包加载错误的报告。
func TestErrors(t *testing.T) {
	dir := t.TempDir()
	if err := syncpair.Lint(dir); err != nil {
		t.Fatal(err)
	}
	if err := syncpair.Lint(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("应报告不存在的目录: %v", err)
	}
	write(t, filepath.Join(dir, "go.mod"), "module example.com/test\n\ngo 1.26\n")
	write(t, filepath.Join(dir, "bad.go"), "package bad\nfunc broken( {\n")
	if err := syncpair.Lint(dir); err == nil || !strings.Contains(err.Error(), "加载 Go 包失败") {
		t.Fatalf("应报告语法错误: %v", err)
	}
}

// write 把 text 写入 path。
func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}
