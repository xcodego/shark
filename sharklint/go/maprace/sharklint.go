// Package maprace 检查异步闭包中共享 map 的访问。
package maprace

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/tools/go/packages"
)

var forceIgnorePattern = regexp.MustCompile(`force[ \t]+ignore[ \t]+sharklint[ \t]*:[ \t]*maprace\b`)

// Violation 表示一处可能与异步闭包并发的 map 访问。
type Violation struct {
	Position token.Position
}

func (v Violation) Error() string {
	return fmt.Sprintf("%s: map 可能被并发访问且未使用同一互斥锁保护", v.Position)
}

// Lint 检查当前构建条件下的非测试 Go 包。
func Lint(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("检查目录 %q 失败: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("检查目录 %q 失败: 不是目录", dir)
	}
	hasGo := false
	err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 &&
			filepath.Ext(path) == ".go" && !strings.HasSuffix(entry.Name(), "_test.go") {
			hasGo = true
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("遍历 %q 失败: %w", dir, err)
	}
	if !hasGo {
		return nil
	}
	pkgs, err := packages.Load(&packages.Config{
		Dir: dir,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
	}, "./...")
	if err != nil {
		return fmt.Errorf("加载 Go 包失败: %w", err)
	}
	var errs []error
	for _, pkg := range pkgs {
		for _, e := range pkg.Errors {
			errs = append(errs, fmt.Errorf("加载 Go 包失败: %s", e))
		}
		for _, file := range pkg.Syntax {
			if strings.HasSuffix(pkg.Fset.PositionFor(file.Pos(), false).Filename, "_test.go") {
				continue
			}
			for _, decl := range file.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
					errs = append(errs, checkFunction(file, fn.Body, pkg.Fset, pkg.TypesInfo)...)
				}
			}
		}
	}
	return errors.Join(errs...)
}

type lockset map[string]bool // true means an exclusive lock

type access struct {
	key    string
	root   types.Object
	pos    token.Pos
	write  bool
	locks  lockset
	async  bool
	launch token.Pos
}

func checkFunction(file *ast.File, body *ast.BlockStmt, fset *token.FileSet, info *types.Info) []error {
	var all []access
	all = append(all, collect(body, info)...)
	ast.Inspect(body, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		var call *ast.CallExpr
		switch n := n.(type) {
		case *ast.GoStmt:
			call = n.Call
		case *ast.CallExpr:
			if isSafeGo(n, info) {
				call = n
			}
		}
		if call == nil {
			return true
		}
		var lit *ast.FuncLit
		if fn, ok := ast.Unparen(call.Fun).(*ast.FuncLit); ok {
			lit = fn
		} else if isSafeGo(call, info) && len(call.Args) == 1 {
			lit, _ = ast.Unparen(call.Args[0]).(*ast.FuncLit)
		}
		if lit == nil {
			return true
		}
		for _, a := range collect(lit.Body, info) {
			// Values declared in the closure are not captured.
			if a.root == nil || a.root.Pos() >= lit.Pos() && a.root.Pos() < lit.End() {
				continue
			}
			a.async, a.launch = true, call.Pos()
			all = append(all, a)
		}
		return true
	})

	report := make(map[token.Pos]bool)
	for i, a := range all {
		if a.async && a.write && !hasExclusive(a.locks) {
			report[a.pos] = true
		}
		for _, b := range all[i+1:] {
			if a.key != b.key || !(a.write || b.write) || !canOverlap(a, b) || protected(a, b) {
				continue
			}
			report[a.pos], report[b.pos] = true, true
		}
	}
	var errs []error
	for _, a := range all {
		if !report[a.pos] {
			continue
		}
		delete(report, a.pos)
		position := fset.PositionFor(a.pos, false)
		if !ignored(file, fset, position.Line) {
			errs = append(errs, Violation{Position: position})
		}
	}
	return errs
}

func hasExclusive(locks lockset) bool {
	for _, exclusive := range locks {
		if exclusive {
			return true
		}
	}
	return false
}

func canOverlap(a, b access) bool {
	if a.async && b.async {
		return true
	}
	if a.async {
		return b.pos > a.launch
	}
	if b.async {
		return a.pos > b.launch
	}
	return false
}

func protected(a, b access) bool {
	for key, exclusive := range a.locks {
		if other, ok := b.locks[key]; ok && (!a.write || exclusive) && (!b.write || other) {
			return true
		}
	}
	return false
}

func isSafeGo(call *ast.CallExpr, info *types.Info) bool {
	sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !ok {
		return false
	}
	fn, ok := info.Uses[sel.Sel].(*types.Func)
	return ok && fn.Name() == "SafeGo" && fn.Pkg() != nil &&
		fn.Pkg().Path() == "github.com/xcodego/shark/sharkapp" && info.Selections[sel] != nil
}

func ignored(file *ast.File, fset *token.FileSet, line int) bool {
	for _, group := range file.Comments {
		if fset.PositionFor(group.End(), false).Line == line-1 && forceIgnorePattern.MatchString(group.Text()) {
			return true
		}
	}
	return false
}
