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

// forceIgnorePattern 匹配 "force ignore sharklint: maprace" 豁免标记。
var forceIgnorePattern = regexp.MustCompile(`force[ \t]+ignore[ \t]+sharklint[ \t]*:[ \t]*maprace\b`)

// Violation 表示一处可能与异步闭包并发的 map 访问。
type Violation struct {
	// Position 是被访问 map 表达式的位置。
	Position token.Position
}

// Error 返回带位置信息的违规描述。
func (v Violation) Error() string {
	return fmt.Sprintf("%s: map 可能被并发访问且未使用同一互斥锁保护", v.Position)
}

// Lint 加载 dir 下当前构建条件中的非测试 Go 包, 检查被 go 语句或 sharkapp.SafeGo
// 直接启动的函数字面量所捕获的 map: 异步写入未持有写锁, 或与其他可能并发的访问
// （至少一方为写）没有被同一把互斥锁保护时报告违规。
// 可在访问所在行的上一行用 force ignore 注释豁免; dir 下没有非测试 .go 文件时跳过。
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

// lockset 记录当前持有的锁: key 为锁对象标识, true 表示独占锁（Lock）, false 表示读锁（RLock）。
type lockset map[string]bool

// access 描述对一个 map 的一次读或写访问。
type access struct {
	key    string       // map 变量（含字段路径）的标识
	root   types.Object // 路径最左侧的变量, 用于判断是否在闭包内声明
	pos    token.Pos
	write  bool
	locks  lockset   // 访问时持有的锁
	async  bool      // 是否发生在异步闭包内
	launch token.Pos // 异步闭包的启动位置
}

// checkFunction 收集函数体内所有 map 访问（含异步闭包内的）, 两两比较后返回违规。
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
			// 闭包内声明的变量不是捕获的共享值, 跳过。
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

// hasExclusive 判断是否持有至少一把独占锁。
func hasExclusive(locks lockset) bool {
	for _, exclusive := range locks {
		if exclusive {
			return true
		}
	}
	return false
}

// canOverlap 判断两次访问是否可能并发: 两个异步访问总是可能; 同步访问仅在异步闭包启动之后才可能与其重叠。
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

// protected 判断两次访问是否持有同一把锁且锁强度足够: 写方必须持有独占锁, 读方持读锁或独占锁均可。
func protected(a, b access) bool {
	for key, exclusive := range a.locks {
		if other, ok := b.locks[key]; ok && (!a.write || exclusive) && (!b.write || other) {
			return true
		}
	}
	return false
}

// isSafeGo 判断 call 是否为 sharkapp.SafeGo 方法调用, 它与 go 语句一样异步执行传入的闭包。
func isSafeGo(call *ast.CallExpr, info *types.Info) bool {
	sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !ok {
		return false
	}
	fn, ok := info.Uses[sel.Sel].(*types.Func)
	return ok && fn.Name() == "SafeGo" && fn.Pkg() != nil &&
		fn.Pkg().Path() == "github.com/xcodego/shark/sharkapp" && info.Selections[sel] != nil
}

// ignored 判断 line 的上一行是否以豁免注释结尾。
func ignored(file *ast.File, fset *token.FileSet, line int) bool {
	for _, group := range file.Comments {
		if fset.PositionFor(group.End(), false).Line == line-1 && forceIgnorePattern.MatchString(group.Text()) {
			return true
		}
	}
	return false
}
