// Package panic 检查 Go 源码中直接调用内置 panic 的语句，允许入口 main 函数调用。
package panic

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var forceIgnorePattern = regexp.MustCompile(`force[ \t]+ignore[ \t]+sharklint[ \t]*:[ \t]*panic\b`)

// Violation 表示一处直接调用内置 panic 的位置。
type Violation struct {
	Position token.Position
}

func (v Violation) Error() string {
	return fmt.Sprintf("%s: 禁止直接调用 panic", v.Position)
}

// Lint 递归检查 dir 中所有非测试 .go 文件，不跟随符号链接。
func Lint(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("检查目录 %q 失败: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("检查目录 %q 失败: 不是目录", dir)
	}

	var errs []error
	err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			errs = append(errs, fmt.Errorf("遍历 %q 失败: %w", path, walkErr))
			return nil
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 ||
			filepath.Ext(path) != ".go" || strings.HasSuffix(filepath.Base(path), "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, parser.AllErrors|parser.ParseComments)
		if parseErr != nil {
			errs = append(errs, fmt.Errorf("解析 %q 失败: %w", path, parseErr))
		}
		if file != nil {
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				name, ok := ast.Unparen(call.Fun).(*ast.Ident)
				if !ok || name.Name != "panic" {
					return true
				}
				if isMainFunction(file, call.Pos()) {
					return true
				}
				position := fset.PositionFor(call.Pos(), false)
				if !hasForceIgnoreComment(file, fset, position.Line) {
					errs = append(errs, Violation{Position: position})
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func isMainFunction(file *ast.File, position token.Pos) bool {
	if file.Name.Name != "main" {
		return false
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == "main" && function.Recv == nil &&
			function.Body != nil && function.Body.Pos() <= position && position <= function.Body.End() {
			return true
		}
	}
	return false
}

func hasForceIgnoreComment(file *ast.File, fset *token.FileSet, callLine int) bool {
	for _, group := range file.Comments {
		if fset.PositionFor(group.End(), false).Line == callLine-1 &&
			forceIgnorePattern.MatchString(group.Text()) {
			return true
		}
	}
	return false
}
