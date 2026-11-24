package redis_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	sharklint_redis "github.com/xcodego/shark/sharklint/redis"
)

func TestForbiddenCommands(t *testing.T) {
	dir := fixture(t)
	commands := []string{
		"KEYS", "FLUSHALL", "FLUSHDB", "CONFIG", "DEBUG", "SHUTDOWN", "SAVE",
		"BGSAVE", "BGREWRITEAOF", "REPLICAOF", "SLAVEOF", "SWAPDB", "MIGRATE",
		"MONITOR", "CLIENT KILL", "CLIENT PAUSE",
	}
	var source strings.Builder
	source.WriteString("package example\nimport (\"context\"; r \"github.com/redis/go-redis/v9\")\nfunc run(ctx context.Context, c *r.Client, key string) {\n")
	for _, command := range commands {
		tokens := strings.Split(command, " ")
		fmt.Fprintf(&source, "c.Do(ctx, %q", strings.ToLower(tokens[0]))
		for _, token := range tokens[1:] {
			fmt.Fprintf(&source, ", %q", strings.ToLower(token))
		}
		source.WriteString(")\n")
	}
	source.WriteString(`c.Do(ctx, "CLIENT", "LIST")
c.Do(ctx, "GET", key)
c.Do(ctx, []any{"CLIENT", "KILL"}...)
c.Do(ctx, []any{"CLIENT", "PAUSE"}...)
c.Do(ctx, []any{"CLIENT", "LIST"}...)
r.NewCmd(ctx, "CONFIG", "GET", "*")
// force ignore sharklint: redis
c.Do(ctx, "FLUSHALL")
c.Do(ctx, []any{1: "KILL", 0: "CLIENT"}...)
c.Do(ctx, []any{1: key, 0: "GET"}...)
}
`)
	writeFile(t, filepath.Join(dir, "main.go"), source.String())
	err := sharklint_redis.Lint(dir)
	if err == nil || strings.Contains(err.Error(), "加载 Go 包失败") {
		t.Fatalf("应当报告禁用命令，实际得到 %v", err)
	}
	for i, command := range commands {
		location := fmt.Sprintf("main.go:%d:", i+4)
		want := "禁止使用 Redis " + command + " 命令"
		if !strings.Contains(err.Error(), location) || !strings.Contains(err.Error(), want) {
			t.Errorf("应报告 %s %s，实际得到 %v", location, command, err)
		}
	}
	for _, line := range []int{20, 21, 24, 27, 29} {
		if strings.Contains(err.Error(), fmt.Sprintf("main.go:%d:", line)) {
			t.Errorf("不应报告第 %d 行，实际得到 %v", line, err)
		}
	}
	if !strings.Contains(err.Error(), "main.go:28:") {
		t.Errorf("应报告带索引的 CLIENT KILL 命令，实际得到 %v", err)
	}
	if got := strings.Count(err.Error(), "禁止使用 Redis "); got != len(commands)+4 {
		t.Errorf("违规数为 %d，期望为 %d：%v", got, len(commands)+4, err)
	}
}

func TestRealRedisMethods(t *testing.T) {
	err := sharklint_redis.Lint(filepath.Join("testdata", "realcommands"))
	if err == nil || strings.Contains(err.Error(), "加载 Go 包失败") {
		t.Fatalf("应当报告禁用命令，实际得到 %v", err)
	}
	commands := []string{
		"FLUSHALL", "FLUSHDB", "CONFIG", "CONFIG", "DEBUG", "SHUTDOWN", "SAVE",
		"BGSAVE", "BGREWRITEAOF", "REPLICAOF", "SLAVEOF", "SWAPDB", "MIGRATE",
		"MONITOR", "CLIENT KILL", "CLIENT KILL", "CLIENT PAUSE",
	}
	for i, command := range commands {
		want := fmt.Sprintf("main.go:%d:2: 禁止使用 Redis %s 命令", i+11, command)
		if !strings.Contains(err.Error(), want) {
			t.Errorf("应报告 %s，实际得到 %v", want, err)
		}
	}
	if got := strings.Count(err.Error(), "禁止使用 Redis "); got != len(commands) {
		t.Errorf("违规数为 %d，期望为 %d：%v", got, len(commands), err)
	}
}

func TestRawForbiddenCommands(t *testing.T) {
	dir := fixture(t)
	writeFile(t, filepath.Join(dir, "redis", "redis.go"), `package redis
import ("context"; "io")
type Client struct{}
func (*Client) DoRaw(context.Context, ...any) {}
func (*Client) DoRawWriteTo(context.Context, io.Writer, ...any) {}
`)
	source := `package example
import ("context"; "io"; r "github.com/redis/go-redis/v9")
func run(ctx context.Context, c *r.Client, w io.Writer, key string) {
	c.DoRaw(ctx, "FLUSHALL") // want
	c.DoRawWriteTo(ctx, w, "CONFIG", "GET", "*") // want
	(*r.Client).DoRaw(c, ctx, "KEYS", "*") // want
	(*r.Client).DoRawWriteTo(c, ctx, w, []any{"CLIENT", "KILL"}...) // want
	c.DoRaw(ctx, "GET", key)
	c.DoRawWriteTo(ctx, w, "GET", key)
	// force ignore sharklint: redis
	c.DoRaw(ctx, "FLUSHALL")
}`
	writeFile(t, filepath.Join(dir, "main.go"), source)
	err := sharklint_redis.Lint(dir)
	if err == nil || strings.Contains(err.Error(), "加载 Go 包失败") {
		t.Fatalf("应当报告禁用命令，实际得到 %v", err)
	}
	for i, line := range strings.Split(source, "\n") {
		location := fmt.Sprintf("main.go:%d:", i+1)
		if strings.Contains(line, "// want") != strings.Contains(err.Error(), location) {
			t.Errorf("诊断位置 %s 不符合预期：%v", location, err)
		}
	}
	if got := strings.Count(err.Error(), "禁止使用 Redis "); got != 4 {
		t.Errorf("违规数为 %d，期望为 4：%v", got, err)
	}
}

func TestRueidisBuilderCommands(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/app\n\ngo 1.26\n\nrequire github.com/redis/rueidis v1.0.0\nreplace github.com/redis/rueidis => ./client\n")
	writeFile(t, filepath.Join(dir, "client", "go.mod"), "module github.com/redis/rueidis\n\ngo 1.26\n")
	writeFile(t, filepath.Join(dir, "client", "builder.go"), "package rueidis\nimport \"github.com/redis/rueidis/internal/cmds\"\ntype Builder = cmds.Builder\n")
	methods := []string{
		"Keys", "Flushall", "Flushdb", "ConfigGet", "ConfigSet", "ConfigResetstat",
		"ConfigRewrite", "DebugObject", "DebugSegfault", "Shutdown", "Save", "Bgsave",
		"Bgrewriteaof", "Replicaof", "Slaveof", "Swapdb", "Migrate", "Monitor",
		"ClientKill", "ClientPause",
	}
	var builder, source strings.Builder
	builder.WriteString("package cmds\ntype Builder struct{}\n")
	source.WriteString("package example\nimport r \"github.com/redis/rueidis\"\nfunc run(b r.Builder) {\n")
	for _, method := range methods {
		fmt.Fprintf(&builder, "func (Builder) %s() {}\n", method)
		fmt.Fprintf(&source, "b.%s()\n", method)
	}
	builder.WriteString("func (Builder) ClientList() {}\n")
	source.WriteString("b.ClientList()\n}\n")
	writeFile(t, filepath.Join(dir, "client", "internal", "cmds", "builder.go"), builder.String())
	writeFile(t, filepath.Join(dir, "main.go"), source.String())
	err := sharklint_redis.Lint(dir)
	if err == nil || strings.Contains(err.Error(), "加载 Go 包失败") {
		t.Fatalf("应当报告禁用命令，实际得到 %v", err)
	}
	for i, method := range methods {
		if !strings.Contains(err.Error(), fmt.Sprintf("main.go:%d:", i+4)) {
			t.Errorf("应报告 %s，实际得到 %v", method, err)
		}
	}
	if got := strings.Count(err.Error(), "禁止使用 Redis "); got != len(methods) {
		t.Errorf("违规数为 %d，期望为 %d：%v", got, len(methods), err)
	}
}
