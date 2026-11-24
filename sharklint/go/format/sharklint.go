// Package format 检查 Go 源码是否经过 gofmt 格式化。
package format

import (
	"errors"
	"fmt"
	"go/format"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
)

// Violation 表示一处未通过 gofmt 格式化的文件。
type Violation struct {
	Position token.Position
}

func (v Violation) Error() string {
	return fmt.Sprintf("%s: Go 文件未通过 gofmt 格式化", v.Position)
}

// Lint 递归检查 dir 中所有 .go 文件，包括测试文件和被构建标签排除的文件。
// 检查不会跟随符号链接，返回所有发现的违规及文件系统或格式错误。
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
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || filepath.Ext(path) != ".go" {
			return nil
		}

		source, readErr := os.ReadFile(path)
		if readErr != nil {
			errs = append(errs, fmt.Errorf("读取 %q 失败: %w", path, readErr))
			return nil
		}
		formatted, formatErr := format.Source(source)
		if formatErr != nil {
			errs = append(errs, fmt.Errorf("gofmt 检查 %q 失败: %w", path, formatErr))
			return nil
		}
		if position, ok := firstDifference(path, source, formatted); ok {
			errs = append(errs, Violation{Position: position})
		}
		return nil
	})
	if err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func firstDifference(path string, source, formatted []byte) (token.Position, bool) {
	offset := 0
	for offset < len(source) && offset < len(formatted) && source[offset] == formatted[offset] {
		offset++
	}
	if offset == len(source) && offset == len(formatted) {
		return token.Position{}, false
	}
	fset := token.NewFileSet()
	file := fset.AddFile(path, -1, len(source))
	file.SetLinesForContent(source)
	return fset.Position(file.Pos(offset)), true
}
