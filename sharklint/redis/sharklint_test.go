package redis_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sharklint_redis "github.com/xcodego/shark/sharklint/redis"
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

func TestOtherRedisClients(t *testing.T) {
	tests := []struct {
		name    string
		module  string
		version string
		files   map[string]string
		source  string
	}{
		{
			name: "go-redis v6", module: "github.com/go-redis/redis", version: "v6.0.0+incompatible",
			files: map[string]string{"redis.go": `package redis
	type Client struct{}
	func (*Client) Keys(string) {}
	func (*Client) Do(...any) {}
	func NewCmd(...any) {}
	`},
			source: `package example
	import r "github.com/go-redis/redis"
	func run(c *r.Client) {
		c.Keys("*") // want
		c.Do("KEYS", "*") // want
		(*r.Client).Do(c, "keys", "*") // want
		r.NewCmd("KEYS", "*") // want
		c.Do("SCAN", 0)
	}`,
		},
		{
			name: "go-redis v7", module: "github.com/go-redis/redis/v7", version: "v7.0.0",
			files: map[string]string{"redis.go": `package redis
	type Client struct{}
	func (*Client) Keys(string) {}
	func (*Client) Do(...any) {}
	func NewCmd(...any) {}
	`},
			source: `package example
	import r "github.com/go-redis/redis/v7"
	func run(c *r.Client) {
		c.Keys("*") // want
		c.Do("keys", "*") // want
		r.NewCmd("KEYS", "*") // want
	}`,
		},
		{
			name: "go-redis v8", module: "github.com/go-redis/redis/v8", version: "v8.0.0",
			files: map[string]string{"redis.go": `package redis
	import "context"
	type Client struct{}
	func (*Client) Keys(context.Context, string) {}
	func (*Client) Do(context.Context, ...any) {}
	func NewCmd(context.Context, ...any) {}
	`},
			source: `package example
	import ("context"; r "github.com/go-redis/redis/v8")
	func run(ctx context.Context, c *r.Client) {
		c.Keys(ctx, "*") // want
		c.Do(ctx, "keys", "*") // want
		r.NewCmd(ctx, "KEYS", "*") // want
	}`,
		},
		{
			name: "redigo", module: "github.com/gomodule/redigo", version: "v1.0.0",
			files: map[string]string{"redis/redis.go": `package redis
	import ("context"; "time")
	type Conn interface { Do(string, ...any); Send(string, ...any) }
	type ConnWithContext interface { DoContext(context.Context, string, ...any) }
	type ConnWithTimeout interface { DoWithTimeout(time.Duration, string, ...any) }
	type Script struct{}
	func (*Script) Do(Conn, ...any) {}
	func (*Script) DoContext(context.Context, Conn, ...any) {}
	func DoContext(Conn, context.Context, string, ...any) {}
	func DoWithTimeout(Conn, time.Duration, string, ...any) {}
	`},
			source: `package example
	import ("context"; "time"; r "github.com/gomodule/redigo/redis")
	func run(ctx context.Context, c r.Conn, cc r.ConnWithContext, ct r.ConnWithTimeout, s *r.Script) {
		c.Do("KEYS", "*") // want
		c.Send("keys", "*") // want
		c.Send("CLIENT", "PAUSE", 100) // want
		c.Do("CLIENT", "LIST")
		r.DoContext(c, ctx, "CONFIG", "GET", "*") // want
		r.Conn.Do(c, "KEYS", "*") // want
		cc.DoContext(ctx, "KEYS", "*") // want
		ct.DoWithTimeout(time.Second, "KEYS", "*") // want
		r.DoContext(c, ctx, "KEYS", "*") // want
		r.DoWithTimeout(c, time.Second, "KEYS", "*") // want
		s.Do(c, "KEYS")
		s.DoContext(ctx, c, "KEYS")
		c.Do("SCAN", 0)
	}`,
		},
		{
			name: "rueidis", module: "github.com/redis/rueidis", version: "v1.0.0",
			files: map[string]string{
				"builder.go": `package rueidis
	import "github.com/redis/rueidis/internal/cmds"
	type Builder = cmds.Builder
	`,
				"internal/cmds/builder.go": `package cmds
	type Builder struct{}
	type Keys struct{}
	type Arbitrary struct{}
	type Migrate struct{}
	type ClientKill struct{}
	type ConfigGet struct{}
	func (Builder) Keys() Keys { return Keys{} }
	func (Builder) Arbitrary(...string) Arbitrary { return Arbitrary{} }
	func (Builder) Migrate() Migrate { return Migrate{} }
	func (Builder) ClientKill() ClientKill { return ClientKill{} }
	func (Builder) ConfigGet() ConfigGet { return ConfigGet{} }
	func (Arbitrary) Keys(...string) Arbitrary { return Arbitrary{} }
	func (Arbitrary) Args(...string) Arbitrary { return Arbitrary{} }
	func (Migrate) Keys(...string) Migrate { return Migrate{} }
	`,
			},
			source: `package example
	import r "github.com/redis/rueidis"
	func run(b r.Builder, key string) {
		b.Keys() // want
		b.Arbitrary("KEYS", "*") // want
		r.Builder.Arbitrary(b, []string{"keys", "*"}...) // want
		b.Arbitrary("GET").Keys(key)
		b.Migrate().Keys(key) // want
		b.ClientKill() // want
		b.ConfigGet() // want
		b.Arbitrary("CLIENT", "PAUSE", "10") // want
		b.Arbitrary("CLIENT").Args("KILL") // want
		b.Arbitrary("CLIENT").Args("LIST")
		b.Arbitrary("CLIENT", "LIST").Args("KILL")
	}`,
		},
		{
			name: "rueidiscompat", module: "github.com/redis/rueidis/rueidiscompat", version: "v1.0.0",
			files: map[string]string{"adapter.go": `package rueidiscompat
	import "context"
	type Cmdable interface { Keys(context.Context, string) }
	type Pipeline struct{}
	func (*Pipeline) Keys(context.Context, string) {}
	func (*Pipeline) Do(context.Context, ...any) {}
	`},
			source: `package example
	import ("context"; r "github.com/redis/rueidis/rueidiscompat")
	func run(ctx context.Context, c r.Cmdable, p *r.Pipeline) {
		c.Keys(ctx, "*") // want
		p.Keys(ctx, "*") // want
		p.Do(ctx, "KEYS", "*") // want
		p.Do(ctx, "SCAN", 0)
	}`,
		},
	}
	legacyGoRedis := tests[0]
	legacyGoRedis.name = "redis/go-redis v6"
	legacyGoRedis.module = "github.com/redis/go-redis"
	legacyGoRedis.source = strings.ReplaceAll(legacyGoRedis.source, "github.com/go-redis/redis", legacyGoRedis.module)
	tests = append(tests, legacyGoRedis)
	legacyRedigo := tests[3]
	legacyRedigo.name = "garyburd redigo"
	legacyRedigo.module = "github.com/garyburd/redigo"
	legacyRedigo.source = strings.ReplaceAll(legacyRedigo.source, "github.com/gomodule/redigo", legacyRedigo.module)
	tests = append(tests, legacyRedigo)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "go.mod"), fmt.Sprintf("module example.com/app\n\ngo 1.26\n\nrequire %s %s\nreplace %s => ./client\n", tc.module, tc.version, tc.module))
			writeFile(t, filepath.Join(dir, "client", "go.mod"), fmt.Sprintf("module %s\n\ngo 1.26\n", tc.module))
			for name, content := range tc.files {
				writeFile(t, filepath.Join(dir, "client", name), content)
			}
			writeFile(t, filepath.Join(dir, "main.go"), tc.source)
			err := sharklint_redis.Lint(dir)
			if err == nil || strings.Contains(err.Error(), "加载 Go 包失败") {
				t.Fatalf("应当只报告违规，实际得到 %v", err)
			}
			wantCount := 0
			for i, line := range strings.Split(tc.source, "\n") {
				location := fmt.Sprintf("main.go:%d:", i+1)
				if strings.Contains(line, "// want") {
					wantCount++
					if !strings.Contains(err.Error(), location) {
						t.Errorf("应当报告 %s，实际得到 %v", location, err)
					}
				} else if strings.Contains(err.Error(), location) {
					t.Errorf("不应报告 %s，实际得到 %v", location, err)
				}
			}
			if got := strings.Count(err.Error(), "禁止使用 Redis "); got != wantCount {
				t.Errorf("违规数为 %d，期望为 %d：%v", got, wantCount, err)
			}
		})
	}
}
func fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), `module example.com/app

go 1.26

require github.com/redis/go-redis/v9 v9.0.0
replace github.com/redis/go-redis/v9 => ./redis
`)
	writeFile(t, filepath.Join(dir, "redis", "go.mod"), "module github.com/redis/go-redis/v9\n\ngo 1.26\n")
	writeFile(t, filepath.Join(dir, "redis", "redis.go"), `package redis
import "context"
type Client struct{}
func (*Client) Keys(context.Context, string) {}
func (*Client) Do(context.Context, ...any) {}
func NewCmd(context.Context, ...any) {}
`)
	return dir
}

func TestLint(t *testing.T) {
	dir := fixture(t)
	writeFile(t, filepath.Join(dir, "main.go"), `package example
import (
	"context"
	r "github.com/redis/go-redis/v9"
)
type other struct{}
func (other) Keys(string) {}
func (other) Do(context.Context, ...any) {}
func run(ctx context.Context, client *r.Client, o other) {
	const command = "KeYs"
	client.Keys(ctx, "*")
	client.Do(ctx, command, "*")
	(*r.Client).Do(client, ctx, "KEYS", "*")
	client.Do(ctx, []any{"KEYS", "*"}...)
	r.NewCmd(ctx, "keys", "*")
	// force ignore sharklint: redis
	client.Keys(ctx, "*")
	o.Keys("*")
	o.Do(ctx, "KEYS", "*")
	client.Do(ctx, "SCAN", 0)
}
`)
	writeFile(t, filepath.Join(dir, "main_test.go"), `package example
import ("context"; r "github.com/redis/go-redis/v9")
func ignored(c *r.Client) { c.Keys(context.Background(), "*") }
`)
	writeFile(t, filepath.Join(dir, "excluded.go"), `//go:build never

package example
import ("context"; r "github.com/redis/go-redis/v9")
func ignored(c *r.Client) { c.Keys(context.Background(), "*") }
`)
	err := sharklint_redis.Lint(dir)
	if err == nil {
		t.Fatal("应当报告 Redis KEYS 违规")
	}
	for _, line := range []string{":11:", ":12:", ":13:", ":14:", ":15:"} {
		if !strings.Contains(err.Error(), "main.go"+line) {
			t.Errorf("应当报告 main.go%s，实际得到 %v", line, err)
		}
	}
	for _, unwanted := range []string{":17:", ":18:", ":19:", ":20:", "main_test.go", "excluded.go"} {
		if strings.Contains(err.Error(), unwanted) {
			t.Errorf("不应报告 %s，实际得到 %v", unwanted, err)
		}
	}
	var violation sharklint_redis.Violation
	if !errors.As(err, &violation) {
		t.Errorf("错误 %v 中应当包含 Violation", err)
	}
}

func TestLintErrors(t *testing.T) {
	dir := fixture(t)
	writeFile(t, filepath.Join(dir, "broken.go"), "package example\nfunc broken( {\n")
	if err := sharklint_redis.Lint(dir); err == nil || !strings.Contains(err.Error(), "加载 Go 包失败") {
		t.Fatalf("应当报告加载错误，实际得到 %v", err)
	}

	if err := sharklint_redis.Lint(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("应当报告目录不存在，实际得到 %v", err)
	}
	if err := sharklint_redis.Lint(filepath.Join(dir, "go.mod")); err == nil || !strings.Contains(err.Error(), "不是目录") {
		t.Fatalf("应当报告目录错误，实际得到 %v", err)
	}
	if err := sharklint_redis.Lint(t.TempDir()); err != nil {
		t.Fatalf("空目录不应报错，实际得到 %v", err)
	}
}

func TestRealRedisTypes(t *testing.T) {
	err := sharklint_redis.Lint(filepath.Join("testdata", "real"))
	if err == nil {
		t.Fatal("应当报告 Redis KEYS 违规")
	}
	for _, line := range []string{":10:", ":11:", ":12:", ":13:", ":14:", ":15:", ":16:", ":18:"} {
		if !strings.Contains(err.Error(), "main.go"+line) {
			t.Errorf("应当报告 main.go%s，实际得到 %v", line, err)
		}
	}
	if strings.Contains(err.Error(), "加载 Go 包失败") || strings.Contains(err.Error(), "main.go:17:") {
		t.Fatalf("不应报告加载错误或 SCAN，实际得到 %v", err)
	}
}
