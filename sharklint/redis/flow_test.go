package redis_test

import (
	"path/filepath"
	"testing"
)

// TestKeyDataFlowAndBoundaries 验证 key 数据流分析：变量传播、分支合并、切片与 map 的各类边界。
func TestKeyDataFlowAndBoundaries(t *testing.T) {
	dir := fixture(t)
	writeFile(t, filepath.Join(dir, "redis", "redis.go"), `package redis
import "context"
type Client struct{}
func (*Client) Get(ctx context.Context, key string) {}
func (*Client) Del(ctx context.Context, keys ...string) {}
func (*Client) MSet(ctx context.Context, values ...any) {}
func (*Client) Eval(ctx context.Context, script string, keys []string, args ...any) {}
func (*Client) Do(ctx context.Context, args ...any) {}
`)
	source := `package example
import ("context"; "fmt"; r "github.com/redis/go-redis/v9")
func run(ctx context.Context, c *r.Client, safe string, cond bool, count int) {
	k := safe
	if cond {
		k = "bad"
		c.Get(ctx, k) // want
	} else {
		c.Get(ctx, k)
	}
	c.Get(ctx, k) // want
	k = safe
	if cond {
		k = "bad"
	} else {
		k = safe
		c.Get(ctx, k)
	}
	c.Get(ctx, k) // want
	k = safe
	if cond { k = "bad"; return }
	c.Get(ctx, k)
	keys := []string{safe}
	copyKeys := keys
	c.Del(ctx, copyKeys...)
	copyKeys[0] = fmt.Sprint(1)
	c.Del(ctx, keys...) // want
	keys[0] = safe
	c.Del(ctx, copyKeys...)
	keys = append(keys, "bad")
	c.Del(ctx, keys...) // want
	keys = []string{safe}
	keys = append(keys, []string{"bad"}...)
	c.Eval(ctx, "script", keys) // want
	(*r.Client).Eval(c, ctx, "script", keys) // want
	values := []any{safe, "value"}
	c.MSet(ctx, values...)
	c.MSet(ctx, values)
	c.MSet(ctx, []any{safe, "value"}...)
	values = append(values, "bad", "value")
	c.MSet(ctx, values...) // want
	(*r.Client).MSet(c, ctx, values...) // want
	values[2] = safe
	c.MSet(ctx, values...)
	m := map[string]any{safe: "value"}
	m2 := m
	c.MSet(ctx, m2)
	m2["bad"] = safe
	c.MSet(ctx, m) // want
	c.Do(ctx, "EVAL", "script", 0, "value")
	c.Do(ctx, "EVAL", "script", 1, safe, "value")
	c.Do(ctx, "EVAL", "script", 1, "bad", "value") // want
	c.Do(ctx, "FCALL", "function", 2, safe, "bad", "value") // want
	c.Do(ctx, "FCALL", "function", 0, "value")
	c.Do(ctx, "EVAL", "script", count, "bad") // want
	c.Do(ctx, []any{"EVAL", "script", 1, safe, "value"}...)
	c.Do(ctx, []any{"FCALL", "function", 1, "bad", "value"}...) // want
	numKeys := 1
	c.Do(ctx, "EVAL", "script", numKeys, safe, "value")
	numKeys = 0
	c.Do(ctx, "EVAL", "script", numKeys, "value")
	(*r.Client).Do(c, ctx, "GET", "bad") // want
	c.Do(ctx, "EVAL", "script", -1, "bad")
	c.Do(ctx, "FCALL", "function", 1, safe, "value")
	c.Do(ctx, "EVAL", "script", 999999999, "bad") // want
	var extra []string
	extra = append(extra, safe)
	c.Del(ctx, extra...)
	extra = append(extra, "bad")
	c.Del(ctx, extra...) // want
	filled := make([]string, 1)
	filled[0] = "bad"
	c.Del(ctx, filled...) // want
	switch {
	case cond:
		k = "bad"
	default:
		k = safe
	}
	c.Get(ctx, k) // want
	for cond {
		k = "bad"
		break
	}
	c.Get(ctx, k) // want
	branchKeys := []string{safe}
	if cond { branchKeys[0] = "bad" }
	c.Del(ctx, branchKeys...) // want
	c.Del(ctx, append([]string{safe}, "bad")...) // want
	reused := make([]string, 1, 3)
	reused[0] = safe
	extended := append(reused, safe)
	extended[0] = "bad"
	c.Del(ctx, reused...) // want
	reallocated := []string{safe}
	detached := append(reallocated, safe)
	detached[0] = "bad"
	c.Del(ctx, reallocated...)
	c.Del(ctx, detached...) // want
	for _, key := range keys {
		c.Get(ctx, key) // want
	}
	for key := range m {
		c.Get(ctx, key) // want
	}
}
func parameters(ctx context.Context, c *r.Client, keys []string, values map[string]any) {
	c.Del(ctx, keys...)
	keys = append(keys, "bad")
	c.Del(ctx, keys...) // want
	values["bad"] = "value"
	c.MSet(ctx, values) // want
}
`
	writeFile(t, filepath.Join(dir, "main.go"), source)
	checkKeyDiagnostics(t, dir, source)
}

// TestLoopAppendTerminates 验证循环中 append 累积参数时数据流分析能够收敛。
func TestLoopAppendTerminates(t *testing.T) {
	dir := fixture(t)
	writeFile(t, filepath.Join(dir, "redis", "redis.go"), `package redis
import "context"
type Client struct{}
func (*Client) Del(ctx context.Context, keys ...string) {}
func (*Client) Do(ctx context.Context, args ...any) {}
`)
	source := `package example
import ("context"; r "github.com/redis/go-redis/v9")
func run(ctx context.Context, c *r.Client, safe string, cond bool) {
	keys := []string{safe}
	for cond {
		keys = append(keys, "bad")
	}
	c.Del(ctx, keys...) // want
	args := []any{"EVAL", "script", 0}
	for cond { args = append(args, "value") }
	c.Do(ctx, args...)
	args = []any{"EVAL", "script", 1, safe}
	for cond { args = append(args, "value") }
	c.Do(ctx, args...)
}
`
	writeFile(t, filepath.Join(dir, "main.go"), source)
	checkKeyDiagnostics(t, dir, source)
}
