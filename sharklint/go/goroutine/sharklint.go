// Package goroutine 检查 Go 源码中直接启动协程的语句。
package goroutine

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

// forceIgnorePattern 匹配 "force ignore sharklint: goroutine" 豁免标记。
var forceIgnorePattern = regexp.MustCompile(`force[ \t]+ignore[ \t]+sharklint[ \t]*:[ \t]*goroutine`)

// Violation 表示一处直接启动协程的违规位置。
type Violation struct {
	// Position 是 go 关键字所在位置。
	Position token.Position
}

// Error 返回带位置信息的违规描述。
func (v Violation) Error() string {
	return fmt.Sprintf("%s: 禁止直接使用 go 启动协程", v.Position)
}

// Lint 递归检查 dir 中所有非测试 .go 文件，包括被构建标签排除的文件。
// 检查不会跟随符号链接，返回所有发现的违规及文件系统或语法错误。
// 每条 go 语句均视为违规, 可在上一行用 force ignore 注释豁免。
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
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || filepath.Ext(path) != ".go" || strings.HasSuffix(filepath.Base(path), "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, parser.AllErrors|parser.ParseComments)
		if parseErr != nil {
			errs = append(errs, fmt.Errorf("解析 %q 失败: %w", path, parseErr))
		}
		if file != nil {
			ast.Inspect(file, func(node ast.Node) bool {
				if stmt, ok := node.(*ast.GoStmt); ok {
					position := fset.PositionFor(stmt.Go, false)
					if !hasForceIgnoreComment(file, fset, position.Line) {
						errs = append(errs, Violation{Position: position})
					}
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

// hasForceIgnoreComment 判断 go 语句所在行的上一行是否以豁免注释结尾。
func hasForceIgnoreComment(file *ast.File, fset *token.FileSet, statementLine int) bool {
	for _, group := range file.Comments {
		if fset.PositionFor(group.End(), false).Line == statementLine-1 &&
			forceIgnorePattern.MatchString(group.Text()) {
			return true
		}
	}
	return false
}
