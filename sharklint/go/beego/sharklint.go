// Package beego 检查 Go 源码是否导入 Beego 框架。
package beego

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var modulePaths = []string{
	"github.com/beego/beego",
	"github.com/astaxie/beego",
}

// Violation 表示导入 Beego 包的位置。
type Violation struct {
	Position   token.Position
	ImportPath string
}

func (v Violation) Error() string {
	return fmt.Sprintf("%s: 禁止导入 Beego 包 %q", v.Position, v.ImportPath)
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
		file, parseErr := parser.ParseFile(fset, path, nil, parser.AllErrors)
		if parseErr != nil {
			errs = append(errs, fmt.Errorf("解析 %q 失败: %w", path, parseErr))
		}
		if file != nil {
			errs = append(errs, checkImports(fset, file)...)
		}
		return nil
	})
	if err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func checkImports(fset *token.FileSet, file *ast.File) []error {
	var errs []error
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil || !isBeegoImport(importPath) {
			continue
		}
		errs = append(errs, Violation{
			Position:   fset.PositionFor(spec.Pos(), false),
			ImportPath: importPath,
		})
	}
	return errs
}

func isBeegoImport(importPath string) bool {
	for _, modulePath := range modulePaths {
		if importPath == modulePath || strings.HasPrefix(importPath, modulePath+"/") {
			return true
		}
	}
	return false
}
