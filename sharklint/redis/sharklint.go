// Package redis 检查 Redis 客户端的禁用命令和 key。
package redis

import (
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/tools/go/packages"
)

var forceIgnorePattern = regexp.MustCompile(`force[ \t]+ignore[ \t]+sharklint[ \t]*:[ \t]*redis\b`)

// Violation 表示一处 Redis 禁用命令调用。
type Violation struct {
	Position token.Position
	Command  string
	Reason   string
}

func (v Violation) Error() string {
	if v.Reason != "" {
		return fmt.Sprintf("%s: %s", v.Position, v.Reason)
	}
	if v.Command == "KEYS" {
		return fmt.Sprintf("%s: 禁止使用 Redis KEYS 命令，请使用 SCAN", v.Position)
	}
	return fmt.Sprintf("%s: 禁止使用 Redis %s 命令", v.Position, v.Command)
}

// Lint 检查 dir 中当前构建条件下的非测试 Go 包。
// 目录必须在 Go 模块或工作区内；类型或加载错误会一并返回。
func Lint(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("检查目录 %q 失败: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("检查目录 %q 失败: 不是目录", dir)
	}

	// 空目录不需要处于模块中，也无需调用 go list。
	hasGoFile := false
	err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 &&
			filepath.Ext(path) == ".go" && !strings.HasSuffix(entry.Name(), "_test.go") {
			hasGoFile = true
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("遍历 %q 失败: %w", dir, err)
	}
	if !hasGoFile {
		return nil
	}

	cfg := &packages.Config{
		Dir: dir,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return fmt.Errorf("加载 Go 包失败: %w", err)
	}
	var errs []error
	for _, pkg := range pkgs {
		for _, pkgErr := range pkg.Errors {
			errs = append(errs, fmt.Errorf("加载 Go 包失败: %s", pkgErr))
		}
		for _, file := range pkg.Syntax {
			if strings.HasSuffix(pkg.Fset.PositionFor(file.Pos(), false).Filename, "_test.go") {
				continue
			}
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				command := forbiddenCall(call, pkg.TypesInfo)
				if command == "" {
					return true
				}
				position := pkg.Fset.PositionFor(call.Pos(), false)
				if !hasForceIgnoreComment(file, pkg.Fset, position.Line) {
					errs = append(errs, Violation{Position: position, Command: command})
				}
				return true
			})
			errs = append(errs, checkKeys(file, pkg.Fset, pkg.TypesInfo)...)
		}
	}
	return errors.Join(errs...)
}

func forbiddenCall(call *ast.CallExpr, info *types.Info) string {
	var fn *types.Func
	var selection *types.Selection
	switch fun := ast.Unparen(call.Fun).(type) {
	case *ast.SelectorExpr:
		fn, _ = info.Uses[fun.Sel].(*types.Func)
		selection = info.Selections[fun]
	case *ast.Ident:
		fn, _ = info.Uses[fun].(*types.Func)
	}
	if fn == nil || fn.Pkg() == nil {
		return ""
	}
	switch fn.Pkg().Path() {
	case "github.com/go-redis/redis", "github.com/redis/go-redis",
		"github.com/go-redis/redis/v7", "github.com/go-redis/redis/v8",
		"github.com/redis/go-redis/v9", "github.com/redis/rueidis/rueidiscompat":
		return goRedisCommand(call, info, fn, selection)
	case "github.com/gomodule/redigo/redis", "github.com/garyburd/redigo/redis":
		return redigoCommand(call, info, fn, selection)
	case "github.com/redis/rueidis/internal/cmds":
		return rueidisCommand(call, info, fn, selection)
	}
	return ""
}

func goRedisCommand(call *ast.CallExpr, info *types.Info, fn *types.Func, selection *types.Selection) string {
	signature, ok := fn.Type().(*types.Signature)
	if !ok {
		return ""
	}
	switch fn.Name() {
	case "Do", "DoRaw", "DoRawWriteTo":
		if selection == nil || !signature.Variadic() {
			return ""
		}
		index := signature.Params().Len() - 1
		if selection.Kind() == types.MethodExpr {
			index++
		}
		return rawCommand(call, info, index)
	default:
		if selection != nil {
			// Sentinel MONITOR configures a Sentinel master, not the Redis MONITOR command.
			if fn.Name() == "Monitor" && signature.Recv() != nil {
				recv := signature.Recv().Type()
				if ptr, ok := recv.(*types.Pointer); ok {
					recv = ptr.Elem()
				}
				if named, ok := types.Unalias(recv).(*types.Named); ok && named.Obj().Name() == "SentinelClient" {
					return ""
				}
			}
			return methodCommand(fn.Name())
		}
		if !strings.HasPrefix(fn.Name(), "New") || !strings.HasSuffix(fn.Name(), "Cmd") {
			return ""
		}
		if !signature.Variadic() {
			return ""
		}
		variadic, ok := signature.Params().At(signature.Params().Len() - 1).Type().(*types.Slice)
		if !ok || !types.Identical(variadic.Elem(), types.Universe.Lookup("any").Type()) {
			return ""
		}
		return rawCommand(call, info, signature.Params().Len()-1)
	}
}

func redigoCommand(call *ast.CallExpr, info *types.Info, fn *types.Func, selection *types.Selection) string {
	index := 0
	switch fn.Name() {
	case "Do", "Send":
		if selection == nil {
			return ""
		}
	case "DoContext", "DoWithTimeout":
		index = 1
		if selection == nil {
			index++
		}
	default:
		return ""
	}
	signature, ok := fn.Type().(*types.Signature)
	if !ok || signature.Params().Len() <= index ||
		!types.Identical(signature.Params().At(index).Type(), types.Typ[types.String]) {
		return ""
	}
	if selection != nil && selection.Kind() == types.MethodExpr {
		index++
	}
	return rawCommand(call, info, index)
}

func rueidisCommand(call *ast.CallExpr, info *types.Info, fn *types.Func, selection *types.Selection) string {
	if selection == nil {
		return ""
	}
	signature, ok := fn.Type().(*types.Signature)
	if !ok || signature.Recv() == nil {
		return ""
	}
	receiver, ok := types.Unalias(signature.Recv().Type()).(*types.Named)
	if !ok {
		return ""
	}
	if receiver.Obj().Name() == "Arbitrary" && (fn.Name() == "Args" || fn.Name() == "Keys") {
		selector := ast.Unparen(call.Fun).(*ast.SelectorExpr)
		base, ok := ast.Unparen(selector.X).(*ast.CallExpr)
		if ok && arbitraryClient(base, info) {
			index := 0
			if selection.Kind() == types.MethodExpr {
				index++
			}
			sub := argumentString(call, info, index)
			if sub == "KILL" || sub == "PAUSE" {
				return "CLIENT " + sub
			}
		}
		return ""
	}
	if receiver.Obj().Name() != "Builder" {
		return ""
	}
	switch fn.Name() {
	case "Arbitrary":
		index := 0
		if selection.Kind() == types.MethodExpr {
			index++
		}
		return rawCommand(call, info, index)
	}
	return methodCommand(fn.Name())
}

func arbitraryClient(call *ast.CallExpr, info *types.Info) bool {
	selector, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Arbitrary" {
		return false
	}
	fn, ok := info.Uses[selector.Sel].(*types.Func)
	selection := info.Selections[selector]
	if !ok || fn.Pkg() == nil || fn.Pkg().Path() != "github.com/redis/rueidis/internal/cmds" || selection == nil {
		return false
	}
	index := 0
	if selection.Kind() == types.MethodExpr {
		index++
	}
	if argumentString(call, info, index) != "CLIENT" {
		return false
	}
	if call.Ellipsis.IsValid() && index == len(call.Args)-1 {
		literal, ok := ast.Unparen(call.Args[index]).(*ast.CompositeLit)
		return ok && len(literal.Elts) == 1
	}
	return len(call.Args) == index+1
}

func methodCommand(name string) string {
	switch strings.ToUpper(name) {
	case "KEYS", "FLUSHALL", "FLUSHDB", "SHUTDOWN", "SAVE", "BGSAVE",
		"BGREWRITEAOF", "REPLICAOF", "SLAVEOF", "SWAPDB", "MIGRATE", "MONITOR":
		return strings.ToUpper(name)
	case "FLUSHALLASYNC":
		return "FLUSHALL"
	case "FLUSHDBASYNC":
		return "FLUSHDB"
	case "SHUTDOWNSAVE", "SHUTDOWNNOSAVE":
		return "SHUTDOWN"
	case "CLIENTKILL", "CLIENTKILLBYFILTER":
		return "CLIENT KILL"
	case "CLIENTPAUSE":
		return "CLIENT PAUSE"
	case "CONFIGGET", "CONFIGSET", "CONFIGRESETSTAT", "CONFIGREWRITE":
		return "CONFIG"
	case "DEBUGOBJECT", "DEBUGSEGFAULT":
		return "DEBUG"
	}
	return ""
}

func rawCommand(call *ast.CallExpr, info *types.Info, index int) string {
	command := argumentString(call, info, index)
	switch command {
	case "KEYS", "FLUSHALL", "FLUSHDB", "CONFIG", "DEBUG", "SHUTDOWN", "SAVE",
		"BGSAVE", "BGREWRITEAOF", "REPLICAOF", "SLAVEOF", "SWAPDB", "MIGRATE", "MONITOR":
		return command
	case "CLIENT":
		sub := argumentString(call, info, index+1)
		if sub == "KILL" || sub == "PAUSE" {
			return "CLIENT " + sub
		}
	}
	return ""
}

func argumentString(call *ast.CallExpr, info *types.Info, index int) string {
	if call.Ellipsis.IsValid() && len(call.Args) > 0 && index >= len(call.Args)-1 {
		literal, ok := ast.Unparen(call.Args[len(call.Args)-1]).(*ast.CompositeLit)
		if !ok {
			return ""
		}
		index -= len(call.Args) - 1
		next := 0
		for _, element := range literal.Elts {
			if keyed, ok := element.(*ast.KeyValueExpr); ok {
				value := info.Types[keyed.Key].Value
				if value == nil || value.Kind() != constant.Int {
					return ""
				}
				key, ok := constant.Int64Val(value)
				if !ok || key < 0 || int64(int(key)) != key {
					return ""
				}
				next = int(key)
				element = keyed.Value
			}
			if next == index {
				return constantString(info, element)
			}
			next++
		}
		return ""
	}
	if index >= len(call.Args) {
		return ""
	}
	return constantString(info, call.Args[index])
}

func constantString(info *types.Info, expr ast.Expr) string {
	value := info.Types[expr].Value
	if value != nil && value.Kind() == constant.String {
		return strings.ToUpper(constant.StringVal(value))
	}
	return ""
}

func hasForceIgnoreComment(file *ast.File, fset *token.FileSet, line int) bool {
	for _, group := range file.Comments {
		if fset.PositionFor(group.End(), false).Line == line-1 &&
			forceIgnorePattern.MatchString(group.Text()) {
			return true
		}
	}
	return false
}
