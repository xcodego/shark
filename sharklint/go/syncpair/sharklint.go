// Package syncpair 检查 sync.Mutex、sync.RWMutex 的加解锁，以及函数内局部 sync.WaitGroup 的
// Add/Done 是否配对，并报告存在未释放退出路径、重复加锁或多余释放的位置。
package syncpair

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

	"golang.org/x/tools/go/cfg"
	"golang.org/x/tools/go/packages"
)

// ignorePattern 匹配 "force ignore sharklint: syncpair" 豁免标记。
var ignorePattern = regexp.MustCompile(`force[ \t]+ignore[ \t]+sharklint[ \t]*:[ \t]*syncpair\b`)

// Violation 表示一处可能不配对的同步操作。
type Violation struct {
	// Position 是问题操作（通常是未释放的加锁或 Add）的位置。
	Position token.Position
	// Message 描述具体问题。
	Message string
}

// Error 返回带位置信息的违规描述。
func (v Violation) Error() string { return fmt.Sprintf("%s: %s", v.Position, v.Message) }

// Lint 加载 dir 下当前构建条件中的非测试 Go 包, 对每个函数体和函数字面量做配对检查。
// 可在问题所在行的上一行用 force ignore 注释豁免; dir 下没有非测试 .go 文件时跳过。
func Lint(dir string) error {
	stat, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("检查目录 %q 失败: %w", dir, err)
	}
	if !stat.IsDir() {
		return fmt.Errorf("检查目录 %q 失败: 不是目录", dir)
	}
	hasGo := false
	err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
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
		if pkg.TypesInfo == nil {
			continue
		}
		for _, file := range pkg.Syntax {
			if strings.HasSuffix(pkg.Fset.PositionFor(file.Pos(), false).Filename, "_test.go") {
				continue
			}
			// 同一位置同一消息只报告一次（多条路径可能触发相同问题）。
			reported := make(map[string]bool)
			report := func(pos token.Pos, message string) {
				position := pkg.Fset.PositionFor(pos, false)
				key := position.String() + message
				if reported[key] || ignored(file, pkg.Fset, position.Line) {
					return
				}
				reported[key] = true
				errs = append(errs, Violation{position, message})
			}
			ast.Inspect(file, func(n ast.Node) bool {
				switch fn := n.(type) {
				case *ast.FuncDecl:
					if fn.Body != nil {
						checkBody(fn.Body, pkg.TypesInfo, report)
					}
				case *ast.FuncLit:
					checkBody(fn.Body, pkg.TypesInfo, report)
				}
				return true
			})
		}
	}
	return errors.Join(errs...)
}

// ignored 判断 line 的上一行是否以豁免注释结尾。
func ignored(file *ast.File, fset *token.FileSet, line int) bool {
	for _, group := range file.Comments {
		if fset.PositionFor(group.End(), false).Line == line-1 && ignorePattern.MatchString(group.Text()) {
			return true
		}
	}
	return false
}

// object 表示一个被跟踪的同步对象及其操作类别。
type object struct {
	key  string       // 对象（含字段路径）的标识
	root types.Object // 路径最左侧的变量
	kind string       // Mutex、RWMutex 或 WaitGroup
	mode string       // 锁的方向: read 或 write; WaitGroup 为空
}

// objectKey 为由标识符、字段选择、解引用和取地址组成的表达式生成稳定标识, 并返回最左侧的变量;
// 无法定位到变量时返回空值。
func objectKey(expr ast.Expr, info *types.Info) (string, types.Object) {
	switch e := ast.Unparen(expr).(type) {
	case *ast.Ident:
		obj := info.ObjectOf(e)
		if obj != nil {
			return fmt.Sprintf("%p", obj), obj
		}
	case *ast.SelectorExpr:
		key, root := objectKey(e.X, info)
		if field := info.Uses[e.Sel]; root != nil && field != nil {
			return fmt.Sprintf("%s/%p", key, field), root
		}
	case *ast.StarExpr:
		return objectKey(e.X, info)
	case *ast.UnaryExpr:
		if e.Op == token.AND {
			return objectKey(e.X, info)
		}
	}
	return "", nil
}

// method 在 call 是 sync.Mutex/RWMutex/WaitGroup 的方法调用时, 返回接收者对象及方法名;
// 否则返回零值和空串。
func method(call *ast.CallExpr, info *types.Info) (object, string) {
	sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !ok || info.Selections[sel] == nil || info.Selections[sel].Kind() != types.MethodVal {
		return object{}, ""
	}
	fn, ok := info.Uses[sel.Sel].(*types.Func)
	if !ok || fn.Pkg() == nil || fn.Pkg().Path() != "sync" {
		return object{}, ""
	}
	sig, ok := fn.Type().(*types.Signature)
	if !ok || sig.Recv() == nil {
		return object{}, ""
	}
	recv := types.Unalias(sig.Recv().Type())
	if ptr, ok := recv.(*types.Pointer); ok {
		recv = types.Unalias(ptr.Elem())
	}
	named, ok := recv.(*types.Named)
	if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != "sync" {
		return object{}, ""
	}
	kind := named.Obj().Name()
	if kind != "Mutex" && kind != "RWMutex" && kind != "WaitGroup" {
		return object{}, ""
	}
	key, root := objectKey(sel.X, info)
	return object{key: key, root: root, kind: kind}, fn.Name()
}

// inspectBody 遍历 body 但不进入嵌套的函数字面量（它们由 Lint 单独检查）。
func inspectBody(body *ast.BlockStmt, visit func(ast.Node)) {
	ast.Inspect(body, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		visit(n)
		return true
	})
}

// graph 构建 body 的控制流图; 内置 panic 调用视为不会返回, 其后的路径不计入退出路径。
func graph(body *ast.BlockStmt, info *types.Info) *cfg.CFG {
	return cfg.New(body, func(call *ast.CallExpr) bool {
		id, ok := ast.Unparen(call.Fun).(*ast.Ident)
		return !ok || info.Uses[id] != types.Universe.Lookup("panic")
	})
}

// checkBody 先收集 body 内出现过的锁/WaitGroup 对象, 再对每个对象沿控制流图逐条路径检查配对。
func checkBody(body *ast.BlockStmt, info *types.Info, report func(token.Pos, string)) {
	objects := make(map[string]object)
	acquired := make(map[string]bool)
	tryLocks := make(map[string]bool)
	inspectBody(body, func(n ast.Node) {
		var worker *ast.FuncLit
		switch n := n.(type) {
		case *ast.GoStmt:
			worker, _ = ast.Unparen(n.Call.Fun).(*ast.FuncLit)
		case *ast.CallExpr:
			if isSafeGo(n, info) && len(n.Args) == 1 {
				worker, _ = ast.Unparen(n.Args[0]).(*ast.FuncLit)
			}
		}
		if worker != nil {
			inspectBody(worker.Body, func(n ast.Node) {
				if call, ok := n.(*ast.CallExpr); ok {
					obj, name := method(call, info)
					if obj.root != nil && obj.kind == "WaitGroup" && (name == "Add" || name == "Done") {
						objects[obj.key+":"] = obj
					}
				}
			})
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return
		}
		obj, name := method(call, info)
		if obj.root == nil {
			return
		}
		if name == "TryLock" || name == "TryRLock" {
			tryLocks[obj.key] = true
			return
		}
		if name == "RLock" || name == "RUnlock" {
			obj.mode = "read"
		} else if name == "Lock" || name == "Unlock" {
			obj.mode = "write"
		} else if obj.kind != "WaitGroup" || (name != "Add" && name != "Done") {
			return
		}
		key := obj.key + ":" + obj.mode
		objects[key] = obj
		if name == "Lock" || name == "RLock" || name == "Add" {
			acquired[key] = true
		}
	})
	g := graph(body, info)
	for key, obj := range objects {
		if obj.kind != "WaitGroup" && tryLocks[obj.key] {
			continue
		}
		if obj.kind == "WaitGroup" {
			// 非局部变量、字段、或逃逸（传入其他函数等）的 WaitGroup 可能由别的函数完成,
			// 这里无法分析, 跳过。
			if obj.root.Pos() < body.Pos() || obj.root.Pos() >= body.End() || strings.Contains(obj.key, "/") ||
				waitGroupEscapes(body, obj, info) {
				continue
			}
		}
		local := obj.root.Pos() > body.Pos() && obj.root.Pos() < body.End()
		analyze(g, obj, info, local || acquired[key], report)
	}
}

// limit 限制计数的上限, 保证循环中状态空间有限、分析能够收敛。
const limit = 64

// state 是沿某条路径到达某点时的配对状态。
type state struct {
	count, deferred int       // 尚未释放的次数 / 已登记的延迟释放次数
	origin          token.Pos // 最早未释放的加锁或 Add 的位置, 用于报告
}

// work 是控制流图遍历的工作项: 以 state 进入 block。
type work struct {
	block *cfg.Block
	state state
}

// analyze 沿控制流图遍历 obj 的所有路径, 在退出点报告未释放或多余的延迟释放。
// checkRelease 为 false 时不报告多余/未配对的释放（对象可能在本函数外加锁）。
func analyze(g *cfg.CFG, obj object, info *types.Info, checkRelease bool, report func(token.Pos, string)) {
	queue := []work{{block: g.Blocks[0]}}
	seen := make(map[work]bool)
	for len(queue) > 0 {
		w := queue[0]
		queue = queue[1:]
		if seen[w] || !w.block.Live {
			continue
		}
		seen[w] = true
		states := []state{w.state}
		for _, node := range w.block.Nodes {
			var next []state
			for _, s := range states {
				next = append(next, transfer(node, s, obj, info, checkRelease, report)...)
			}
			states = next
		}
		if len(w.block.Succs) == 0 {
			for _, s := range states {
				if s.count > s.deferred && s.origin.IsValid() {
					report(s.origin, missingMessage(obj))
				}
				if s.deferred > s.count && checkRelease && s.origin.IsValid() {
					report(s.origin, obj.kind+" 存在多余的延迟释放操作")
				}
			}
		}
		for _, succ := range w.block.Succs {
			for _, s := range states {
				queue = append(queue, work{succ, s})
			}
		}
	}
}

// transfer 计算 node 对 obj 状态的影响并返回后继状态; 同时在重复加锁、未配对释放、
// Wait 前仍有未完成的 Add 时直接报告。
func transfer(node ast.Node, s state, obj object, info *types.Info, checkRelease bool, report func(token.Pos, string)) []state {
	var call *ast.CallExpr
	deferred := false
	switch n := node.(type) {
	case *ast.ExprStmt:
		call, _ = ast.Unparen(n.X).(*ast.CallExpr)
	case *ast.DeferStmt:
		call, deferred = n.Call, true
	case *ast.GoStmt:
		if obj.kind == "WaitGroup" {
			if lit, ok := ast.Unparen(n.Call.Fun).(*ast.FuncLit); ok {
				return applyWorker(s, lit, obj, info, report)
			}
		}
	}
	if call == nil {
		return []state{s}
	}
	if obj.kind == "WaitGroup" && !deferred && isSafeGo(call, info) && len(call.Args) == 1 {
		if lit, ok := ast.Unparen(call.Args[0]).(*ast.FuncLit); ok {
			return applyWorker(s, lit, obj, info, report)
		}
	}
	got, name := method(call, info)
	if got.key != obj.key {
		return []state{s}
	}
	if name == "Wait" && obj.kind == "WaitGroup" && s.count > 0 && s.origin.IsValid() {
		report(s.origin, missingMessage(obj))
	}
	delta := 0
	switch name {
	case "Lock":
		if obj.mode == "write" {
			delta = 1
		}
	case "RLock":
		if obj.mode == "read" {
			delta = 1
		}
	case "Unlock":
		if obj.mode == "write" {
			delta = -1
		}
	case "RUnlock":
		if obj.mode == "read" {
			delta = -1
		}
	case "Done":
		if obj.kind == "WaitGroup" {
			delta = -1
		}
	case "Add":
		if obj.kind == "WaitGroup" && len(call.Args) == 1 {
			if v := info.Types[call.Args[0]].Value; v != nil {
				if n, ok := constant.Int64Val(v); ok && n >= -limit && n <= limit {
					delta = int(n)
				}
			}
		}
	}
	if delta > 0 && !deferred {
		if obj.mode == "write" && s.count > 0 {
			report(call.Pos(), obj.kind+" 已加锁，不能重复加锁")
		}
		if s.count == 0 {
			s.origin = call.Pos()
		}
		s.count = min(limit, s.count+delta)
	} else if delta < 0 {
		if deferred {
			s.deferred = min(limit, s.deferred-delta)
			if !s.origin.IsValid() {
				s.origin = call.Pos()
			}
		} else {
			if s.count < -delta && checkRelease {
				report(call.Pos(), obj.kind+" 存在未配对的释放操作")
			}
			s.count = max(0, s.count+delta)
			if s.count == 0 && s.deferred == 0 {
				s.origin = token.NoPos
			}
		}
	}
	return []state{s}
}

// missingMessage 返回退出路径上存在未配对操作时的报告文案。
func missingMessage(obj object) string {
	if obj.kind == "WaitGroup" {
		return "WaitGroup 在 Wait 或退出时存在未配配的 Add"
	}
	return obj.kind + " 存在退出路径未配对的同步操作"
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
