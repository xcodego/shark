package redis_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sharklint_redis "github.com/xcodego/shark/sharklint/redis"
)

// TestKeyConstruction 验证 go-redis 方法参数中直接字面量或构造字符串作为 key 的识别。
func TestKeyConstruction(t *testing.T) {
	dir := fixture(t)
	writeFile(t, filepath.Join(dir, "redis", "redis.go"), `package redis
import "context"
type Client struct{}
func (*Client) Get(ctx context.Context, key string) {}
func (*Client) Set(ctx context.Context, key string, value any) {}
func (*Client) HGet(ctx context.Context, key, field string) {}
func (*Client) Del(ctx context.Context, keys ...string) {}
func (*Client) Rename(ctx context.Context, key, newkey string) {}
func (*Client) MSet(ctx context.Context, values ...any) {}
func (*Client) Eval(ctx context.Context, script string, keys []string, args ...any) {}
func (*Client) Do(ctx context.Context, args ...any) {}
func NewCmd(ctx context.Context, args ...any) {}
`)
	source := `package example
import (
	"context"
	"fmt"
	"strconv"
	"strings"
	r "github.com/redis/go-redis/v9"
)
const externalKey = "external"
func key() string { return "hidden" }
func run(ctx context.Context, c *r.Client, safe string) {
	c.Get(ctx, "key") // want
	c.Get(ctx, "prefix:" + safe) // want
	c.Get(ctx, fmt.Sprintf("key:%s", safe)) // want
	c.Get(ctx, strings.Join([]string{safe}, ":")) // want
	c.Get(ctx, strconv.Itoa(42)) // want
	c.Get(ctx, string([]byte{65})) // want
	var builder strings.Builder
	builder.WriteString(safe)
	c.Get(ctx, builder.String()) // want
	k := fmt.Sprint(safe)
	copy := k
	c.Get(ctx, copy) // want
	k = safe
	c.Get(ctx, k)
	{
		k := "shadow"
		c.Get(ctx, k) // want
	}
	c.Get(ctx, k)
	c.Get(ctx, safe)
	c.Get(ctx, externalKey)
	c.Get(ctx, key())
	c.Set(ctx, safe, "value")
	c.HGet(ctx, safe, "field")
	c.Del(ctx, safe, "key") // want
	c.Del(ctx, []string{safe, "key"}...) // want
	c.Rename(ctx, safe, "key") // want
	c.MSet(ctx, safe, "value")
	c.MSet(ctx, "key", "value") // want
	c.MSet(ctx, map[string]any{safe: "value"})
	c.MSet(ctx, map[string]any{"key": safe}) // want
	c.MSet(ctx, []any{safe, "value"})
	c.MSet(ctx, []any{"key", "value"}) // want
	c.Eval(ctx, "return 1", []string{"key"}, "value") // want
	c.Do(ctx, "SET", safe, "value")
	c.Do(ctx, "GET", "key") // want
	c.Do(ctx, []any{"GET", "key"}...) // want
	c.Do(ctx, []any{1: "key", 0: "GET"}...) // want
	c.Do(ctx, "MSET", safe, "value", "key", "value") // want
	c.Do(ctx, "EVAL", "return 1", 1, safe, "value")
	c.Do(ctx, "EVAL", "return 1", 1, "key", "value") // want
	rawArgs := []any{"SET", "key", "value"}
	c.Do(ctx, rawArgs...) // want
	rawArgs = []any{"SET", safe, "value"}
	c.Do(ctx, rawArgs...)
	command := "GET"
	c.Do(ctx, command, "key") // want
	r.NewCmd(ctx, "GET", "key") // want
	(*r.Client).Get(c, ctx, "key") // want
	// force ignore sharklint: redis
	c.Get(ctx, "ignored")
}
`
	writeFile(t, filepath.Join(dir, "main.go"), source)
	checkKeyDiagnostics(t, dir, source)
}

// checkKeyDiagnostics 检查 dir 的 key 诊断：带“// want”的行必须被报告，其余行不得被报告。
func checkKeyDiagnostics(t *testing.T, dir, source string) {
	t.Helper()
	err := sharklint_redis.Lint(dir)
	if err == nil || strings.Contains(err.Error(), "加载 Go 包失败") {
		t.Fatalf("应当只报告 key 违规，实际得到 %v", err)
	}
	want := 0
	for i, line := range strings.Split(source, "\n") {
		location := fmt.Sprintf("main.go:%d:", i+1)
		if strings.Contains(line, "// want") {
			want++
			if !strings.Contains(err.Error(), location) {
				t.Errorf("应报告 %s：%v", location, err)
			}
		} else if strings.Contains(err.Error(), location) {
			t.Errorf("不应报告 %s：%v", location, err)
		}
	}
	if got := strings.Count(err.Error(), "禁止直接使用字符串或构造字符串作为 Redis key"); got != want {
		t.Errorf("违规数为 %d，期望 %d：%v", got, want, err)
	}
}

// TestRedigoAndRueidisKeys 验证 redigo 与 rueidis 的 key 识别。
func TestRedigoAndRueidisKeys(t *testing.T) {
	tests := []struct {
		name, module, version, client, source string
	}{
		{
			"redigo", "github.com/gomodule/redigo", "v1.0.0",
			`package redis
import ("context"; "time")
type Conn interface { Do(string, ...any); Send(string, ...any) }
type ConnWithContext interface { DoContext(context.Context, string, ...any) }
type ConnWithTimeout interface { DoWithTimeout(time.Duration, string, ...any) }
func DoContext(Conn, context.Context, string, ...any) {}
func DoWithTimeout(Conn, time.Duration, string, ...any) {}
`,
			`package example
import ("context"; "fmt"; "strings"; "time"; r "github.com/gomodule/redigo/redis")
func run(ctx context.Context, c r.Conn, cc r.ConnWithContext, ct r.ConnWithTimeout, safe string) {
	c.Do("GET", "key") // want
	c.Send("GET", "key") // want
	c.Send("SET", safe, "value")
	c.Send("SET", fmt.Sprintf("key:%s", safe), "value") // want
	var builder strings.Builder
	c.Send("GET", builder.String()) // want
	key := fmt.Sprint(safe)
	c.Do("GET", key) // want
	cc.DoContext(ctx, "GET", "key") // want
	ct.DoWithTimeout(time.Second, "GET", "key") // want
	r.DoContext(c, ctx, "GET", "key") // want
	r.DoWithTimeout(c, time.Second, "GET", "key") // want
	r.Conn.Do(c, "GET", "key") // want
	r.Conn.Send(c, "GET", "key") // want
	r.ConnWithContext.DoContext(cc, ctx, "GET", "key") // want
	r.ConnWithTimeout.DoWithTimeout(ct, time.Second, "GET", "key") // want
	c.Send("SET", []any{"key", "value"}...) // want
	args := []any{"key", "value"}
	alias := args
	c.Do("SET", args...) // want
	c.Send("SET", alias...) // want
	cc.DoContext(ctx, "SET", args...) // want
	ct.DoWithTimeout(time.Second, "SET", args...) // want
	r.DoContext(c, ctx, "SET", args...) // want
	r.DoWithTimeout(c, time.Second, "SET", args...) // want
	args = []any{safe, "value"}
	c.Do("SET", args...)
	c.Send("SET", args...)
	cc.DoContext(ctx, "SET", args...)
	ct.DoWithTimeout(time.Second, "SET", args...)
	r.DoContext(c, ctx, "SET", args...)
	r.DoWithTimeout(c, time.Second, "SET", args...)
	command := "GET"
	c.Do(command, "key") // want
	c.Send(command, "key") // want
	c.Do("HSCAN", "key", 0) // want
	c.Do("EVAL", "return 1", 1, "key", "value") // want
	c.Do("EVAL", "return 1", 1, safe, "value")
	c.Do("PING", "message")
}`,
		},
		{
			"rueidis", "github.com/redis/rueidis", "v1.0.0",
			`package cmds
type Builder struct{}
type Get struct{}
type Arbitrary struct{}
func (Builder) Get() Get { return Get{} }
func (Get) Key(string) Get { return Get{} }
func (Builder) Arbitrary(...string) Arbitrary { return Arbitrary{} }
func (Arbitrary) Keys(...string) Arbitrary { return Arbitrary{} }
`,
			`package example
import r "github.com/redis/rueidis"
func run(b r.Builder, safe string) {
	b.Get().Key("key") // want
	b.Get().Key(safe)
	b.Arbitrary("GET").Keys("key") // want
	b.Arbitrary("GET", "key") // want
}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "go.mod"), fmt.Sprintf("module example.com/app\n\ngo 1.26\nrequire %s %s\nreplace %s => ./client\n", tc.module, tc.version, tc.module))
			writeFile(t, filepath.Join(dir, "client", "go.mod"), fmt.Sprintf("module %s\n\ngo 1.26\n", tc.module))
			if tc.name == "redigo" {
				writeFile(t, filepath.Join(dir, "client", "redis", "redis.go"), tc.client)
			} else {
				writeFile(t, filepath.Join(dir, "client", "internal", "cmds", "builder.go"), tc.client)
				writeFile(t, filepath.Join(dir, "client", "builder.go"), "package rueidis\nimport \"github.com/redis/rueidis/internal/cmds\"\ntype Builder = cmds.Builder\n")
			}
			writeFile(t, filepath.Join(dir, "main.go"), tc.source)
			checkKeyDiagnostics(t, dir, tc.source)
		})
	}
}

// TestRealRedisKeys 用真实 go-redis 类型的 testdata 验证 key 识别。
func TestRealRedisKeys(t *testing.T) {
	path := filepath.Join("testdata", "realkeys", "main.go")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	checkKeyDiagnostics(t, filepath.Dir(path), string(source))
}
