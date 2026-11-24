// Package sharklint_sql 检查手写 SQL 和表名、字段名的使用。
package sql

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

var forceIgnorePattern = regexp.MustCompile(`force[ \t]+ignore[ \t]+sharklint[ \t]*:[ \t]*sql\b`)

// Violation 表示一处 SQL 使用规范违规。
type Violation struct {
	Position token.Position
	Reason   string
}

func (v Violation) Error() string {
	return fmt.Sprintf("%s: %s", v.Position, v.Reason)
}

// Lint 检查当前构建条件下的非测试 Go 包，报告包加载错误和违规。
func Lint(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("检查目录 %q 失败: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("检查目录 %q 失败: 不是目录", dir)
	}
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
			built := builtSQLVariables(file, pkg.TypesInfo)
			maps := mapVariables(file, pkg.TypesInfo)
			errs = append(errs, checkTransactions(file, pkg.Fset, pkg.TypesInfo)...)
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				reason := checkCall(call, pkg.TypesInfo, built, maps)
				if reason == "" {
					return true
				}
				position := pkg.Fset.PositionFor(call.Pos(), false)
				if !hasForceIgnoreComment(file, pkg.Fset, position.Line) {
					errs = append(errs, Violation{Position: position, Reason: reason})
				}
				return true
			})
		}
	}
	return errors.Join(errs...)
}

const (
	rawSQL = "禁止直接使用或拼接 SQL，请使用构建器"
	names  = "表名和字段名必须直接使用 app/gconst 定义的常量"
)

func calledFunc(call *ast.CallExpr, info *types.Info) (*types.Func, int) {
	switch fun := ast.Unparen(call.Fun).(type) {
	case *ast.SelectorExpr:
		fn, _ := info.Uses[fun.Sel].(*types.Func)
		offset := 0
		if selection := info.Selections[fun]; selection != nil && selection.Kind() == types.MethodExpr {
			offset = 1
		}
		return fn, offset
	case *ast.Ident:
		fn, _ := info.Uses[fun].(*types.Func)
		return fn, 0
	}
	return nil, 0
}

func checkCall(call *ast.CallExpr, info *types.Info, built, maps map[types.Object]bool) string {
	fn, offset := calledFunc(call, info)
	if fn == nil || fn.Pkg() == nil || offset > len(call.Args) {
		return ""
	}
	args := call.Args[offset:]
	switch fn.Pkg().Path() {
	case "database/sql":
		switch fn.Name() {
		case "Query", "QueryContext", "QueryRow", "QueryRowContext",
			"Exec", "ExecContext", "Prepare", "PrepareContext":
			sig, ok := fn.Type().(*types.Signature)
			if ok && sig.Recv() != nil {
				recv := types.Unalias(sig.Recv().Type())
				if ptr, ok := recv.(*types.Pointer); ok {
					recv = types.Unalias(ptr.Elem())
				}
				if named, ok := recv.(*types.Named); ok && named.Obj().Name() != "Stmt" {
					return rawSQL
				}
			}
		}
	case "gorm.io/gorm":
		switch fn.Name() {
		case "Raw", "Exec":
			return rawSQL
		case "Where", "Not", "Or", "Having", "Joins", "InnerJoins", "Expr":
			if len(args) > 0 && !conditionArg(args[0], info, built, maps) {
				return rawSQL
			}
		case "First", "Last", "Take", "Find", "Delete":
			if len(args) > 1 && !conditionArg(args[1], info, built, maps) {
				return rawSQL
			}
		case "Updates", "UpdateColumns", "Create", "CreateInBatches", "Attrs", "Assign":
			if len(args) > 0 {
				t := info.TypeOf(args[0])
				if t != nil {
					if ptr, ok := t.Underlying().(*types.Pointer); ok {
						t = ptr.Elem()
					}
					if _, ok := t.Underlying().(*types.Map); ok && !mapNames(args[0], info, maps) {
						return names
					}
				}
			}
		case "Table", "Select", "Distinct", "Omit", "Group", "Order", "Pluck", "Update", "UpdateColumn":
			if len(args) > 0 {
				end := len(args)
				if fn.Name() == "Table" || fn.Name() == "Group" || fn.Name() == "Order" ||
					fn.Name() == "Pluck" || fn.Name() == "Update" || fn.Name() == "UpdateColumn" {
					end = 1
				}
				for _, arg := range args[:end] {
					if (fn.Name() == "Select" || fn.Name() == "Distinct" || fn.Name() == "Group" || fn.Name() == "Order") &&
						builtVariable(arg, info, built) {
						continue
					}
					if !nameArg(arg, info) {
						return names
					}
				}
				if call.Ellipsis.IsValid() {
					return names
				}
			}
		}
	case "github.com/xcodego/shark/sharksql":
		switch fn.Name() {
		case "Case", "CaseAs", "When", "WhenAs":
			return rawSQL
		}
		indices, variadic := sqlNameArgs(fn.Name())
		for _, i := range indices {
			if i < len(args) && !nameArg(args[i], info) {
				return names
			}
		}
		if variadic >= 0 {
			for _, arg := range args[min(variadic, len(args)):] {
				if !nameArg(arg, info) {
					return names
				}
			}
			if call.Ellipsis.IsValid() {
				return names
			}
		}
	}
	return ""
}

// sqlNameArgs 返回表名、字段名和别名参数的位置。
func sqlNameArgs(name string) ([]int, int) {
	switch name {
	case "Eq", "Neq", "Gt", "Gte", "Lt", "Lte", "Like", "NotLike", "LikeLeft", "LikeRight",
		"In", "NotIn", "IsNull", "IsNotNull", "Between", "FromTo",
		"Add", "Sub", "Mul", "Div", "Asc", "Desc", "Count", "Paren",
		"JsonSearchOne", "JsonContains", "JsonArrayAppendObject", "JsonArrayAppend",
		"JsonSetObject", "JsonSet", "JsonExtract", "JsonUnquote", "JsonRemove",
		"JsonArrayInsert", "JsonLength", "JsonKeys", "JsonType",
		"BitOr", "BitAnd", "BitXor", "BitNot", "BitShiftLeft", "BitShiftRight",
		"Negative", "LeftJoin", "InnerJoin", "IfNull":
		return []int{0}, -1
	case "EqCol", "NeqCol", "GtCol", "GteCol", "LtCol", "LteCol",
		"AddCol", "SubCol", "MulCol", "DivCol", "BitOrCol", "BitAndCol",
		"BitXorCol", "BitShiftLeftCol", "BitShiftRightCol", "Column", "As":
		return []int{0, 1}, -1
	case "AddColAs", "SubColAs", "MulColAs", "DivColAs", "ColumnAs":
		return []int{0, 1, 2}, -1
	case "IfNullAs":
		return []int{0, 2}, -1
	case "CastAs":
		return []int{0}, -1
	case "DateFormat":
		return []int{1}, -1
	case "GormAs":
		return []int{0}, -1
	case "Sum", "SumAs", "CountAs", "Avg", "AvgAs", "Max", "MaxAs", "Min", "MinAs", "Distinct", "Coalesce", "CoalesceAs":
		return nil, 0
	}
	return nil, -1
}

func nameArg(expr ast.Expr, info *types.Info) bool {
	expr = ast.Unparen(expr)
	switch e := expr.(type) {
	case *ast.Ident:
		return isGconst(info.Uses[e])
	case *ast.SelectorExpr:
		return isGconst(info.Uses[e.Sel])
	case *ast.CallExpr:
		fn, _ := calledFunc(e, info)
		return isBuilderFunc(fn)
	case *ast.CompositeLit:
		t := info.TypeOf(e)
		if t != nil {
			if _, ok := t.Underlying().(*types.Slice); !ok {
				return false
			}
			for _, elt := range e.Elts {
				if !nameArg(elt, info) {
					return false
				}
			}
			return true
		}
	}
	return false
}

func isGconst(obj types.Object) bool {
	c, ok := obj.(*types.Const)
	if !ok || c.Pkg() == nil {
		return false
	}
	path := c.Pkg().Path()
	return path == "app/gconst" || strings.HasSuffix(path, "/app/gconst")
}

func conditionArg(expr ast.Expr, info *types.Info, built, maps map[types.Object]bool) bool {
	expr = ast.Unparen(expr)
	if builtVariable(expr, info, built) {
		return true
	}
	if call, ok := expr.(*ast.CallExpr); ok {
		fn, _ := calledFunc(call, info)
		return isBuilderFunc(fn)
	}
	t := info.TypeOf(expr)
	if t == nil {
		return false
	}
	if ptr, ok := t.Underlying().(*types.Pointer); ok {
		if _, ok := ptr.Elem().Underlying().(*types.Map); ok {
			return mapNames(expr, info, maps)
		}
	}
	switch underlying := t.Underlying().(type) {
	case *types.Basic:
		return underlying.Info()&types.IsString == 0
	case *types.Map:
		return mapNames(expr, info, maps)
	case *types.Interface:
		return false
	}
	return true
}

func builtVariable(expr ast.Expr, info *types.Info, built map[types.Object]bool) bool {
	id, ok := ast.Unparen(expr).(*ast.Ident)
	return ok && built[info.Uses[id]]
}

func mapNames(expr ast.Expr, info *types.Info, maps map[types.Object]bool) bool {
	if unary, ok := ast.Unparen(expr).(*ast.UnaryExpr); ok && unary.Op == token.AND {
		expr = unary.X
	}
	if id, ok := ast.Unparen(expr).(*ast.Ident); ok {
		return maps[info.Uses[id]]
	}
	lit, ok := ast.Unparen(expr).(*ast.CompositeLit)
	if !ok {
		return false
	}
	t := info.TypeOf(lit)
	if t == nil {
		return false
	}
	if _, ok := t.Underlying().(*types.Map); !ok {
		return false
	}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok || !nameArg(kv.Key, info) {
			return false
		}
	}
	return true
}

// 仅认可本文件内由字面量初始化、且所有索引赋值均使用常量键的 map。
func mapVariables(file *ast.File, info *types.Info) map[types.Object]bool {
	maps := make(map[types.Object]bool)
	seen := make(map[types.Object]bool)
	record := func(id *ast.Ident, value ast.Expr) {
		obj := info.Defs[id]
		if obj == nil {
			obj = info.Uses[id]
		}
		if obj == nil {
			return
		}
		if _, ok := obj.Type().Underlying().(*types.Map); !ok {
			return
		}
		if seen[obj] {
			maps[obj] = false
		} else {
			maps[obj] = value != nil && mapNames(value, info, nil)
			seen[obj] = true
		}
	}
	var invalid []types.Object
	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.AssignStmt:
			for _, rhs := range n.Rhs {
				if id, ok := ast.Unparen(rhs).(*ast.Ident); ok {
					if obj := info.Uses[id]; obj != nil {
						if _, ok := obj.Type().Underlying().(*types.Map); ok {
							invalid = append(invalid, obj)
						}
					}
				}
			}
			for i, lhs := range n.Lhs {
				if index, ok := ast.Unparen(lhs).(*ast.IndexExpr); ok {
					if id, ok := ast.Unparen(index.X).(*ast.Ident); ok && !nameArg(index.Index, info) {
						invalid = append(invalid, info.Uses[id])
					}
					continue
				}
				if id, ok := lhs.(*ast.Ident); ok {
					var value ast.Expr
					if n.Tok == token.DEFINE || n.Tok == token.ASSIGN {
						if len(n.Lhs) == len(n.Rhs) {
							value = n.Rhs[i]
						}
					}
					record(id, value)
				}
			}
		case *ast.ValueSpec:
			for _, value := range n.Values {
				if id, ok := ast.Unparen(value).(*ast.Ident); ok {
					if obj := info.Uses[id]; obj != nil {
						if _, ok := obj.Type().Underlying().(*types.Map); ok {
							invalid = append(invalid, obj)
						}
					}
				}
			}
			for i, id := range n.Names {
				var value ast.Expr
				if len(n.Names) == len(n.Values) {
					value = n.Values[i]
				}
				record(id, value)
			}
		}
		return true
	})
	for _, obj := range invalid {
		maps[obj] = false
	}
	return maps
}

// 只接受唯一一次赋值来自构建器的变量，避免把后续重新赋值的 SQL 当作安全结果。
func builtSQLVariables(file *ast.File, info *types.Info) map[types.Object]bool {
	built := make(map[types.Object]bool)
	seen := make(map[types.Object]bool)
	record := func(id *ast.Ident, safe bool) {
		obj := info.Defs[id]
		if obj == nil {
			obj = info.Uses[id]
		}
		if obj == nil {
			return
		}
		if seen[obj] {
			built[obj] = false
		} else {
			built[obj] = safe
			seen[obj] = true
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.AssignStmt:
			for i, lhs := range n.Lhs {
				id, ok := lhs.(*ast.Ident)
				if !ok {
					continue
				}
				safe := false
				if n.Tok == token.ASSIGN || n.Tok == token.DEFINE {
					if len(n.Rhs) == 1 && i == 0 {
						safe = isBuilderCall(n.Rhs[0], info)
					} else if len(n.Rhs) == len(n.Lhs) {
						safe = isBuilderCall(n.Rhs[i], info)
					}
				}
				record(id, safe)
			}
		case *ast.ValueSpec:
			for i, id := range n.Names {
				safe := len(n.Values) == 1 && i == 0 && isBuilderCall(n.Values[0], info)
				if len(n.Values) == len(n.Names) {
					safe = isBuilderCall(n.Values[i], info)
				}
				record(id, safe)
			}
		case *ast.IncDecStmt:
			if id, ok := n.X.(*ast.Ident); ok {
				record(id, false)
			}
		}
		return true
	})
	return built
}

func isBuilderCall(expr ast.Expr, info *types.Info) bool {
	call, ok := ast.Unparen(expr).(*ast.CallExpr)
	if !ok {
		return false
	}
	fn, _ := calledFunc(call, info)
	return isBuilderFunc(fn)
}

func isBuilderFunc(fn *types.Func) bool {
	if fn == nil || fn.Pkg() == nil || fn.Pkg().Path() != "github.com/xcodego/shark/sharksql" {
		return false
	}
	if fn.Name() == "Build" || fn.Name() == "Where" {
		return true
	}
	indices, variadic := sqlNameArgs(fn.Name())
	return len(indices) != 0 || variadic >= 0
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
