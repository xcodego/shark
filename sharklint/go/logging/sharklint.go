// Package logging 检查 Go 源码是否通过注入的 zap.Logger 对象记录日志。
package logging

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
	"strconv"
	"strings"
)

var forceIgnorePattern = regexp.MustCompile(`force[ \t]+ignore[ \t]+sharklint[ \t]*:[ \t]*logging\b`)

var printFunctions = map[string]struct{}{
	"Print": {}, "Println": {}, "Printf": {},
	"Fprint": {}, "Fprintln": {}, "Fprintf": {},
}

var zapGlobalFunctions = map[string]struct{}{
	"L": {}, "S": {}, "ReplaceGlobals": {}, "RedirectStdLog": {},
}

var zapConstructors = map[string]struct{}{
	"New": {}, "NewDevelopment": {}, "NewExample": {},
	"NewNop": {}, "NewProduction": {},
}

// Violation 表示一处绕过注入式 zap.Logger 的日志调用。
type Violation struct {
	Position token.Position
	Message  string
}

func (v Violation) Error() string {
	return fmt.Sprintf("%s: %s", v.Position, v.Message)
}

// Lint 递归检查 dir 中所有非测试 .go 文件，不跟随符号链接。
// 检查禁止直接打印、使用标准库 log、zap 全局 Logger，以及在业务包中创建 Logger。
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
			checkFile(path, fset, file, &errs)
		}
		return nil
	})
	if err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func checkFile(path string, fset *token.FileSet, file *ast.File, errs *[]error) {
	checkForbiddenLogImports(fset, file, errs)
	fmtNames, zapNames := importedNames(file)

	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}

		name, ok := calledName(call.Fun)
		if !ok {
			if isZapConfigBuild(call, zapNames) && !isLoggerFactoryCall(path, file, call.Pos()) {
				addViolation(fset, file, call, "禁止在业务代码中创建 zap.Logger，请使用注入的 zap.Logger 对象", errs)
			}
			return true
		}

		message := ""
		switch {
		case (fmtNames[name.packageName] && isPrintFunction(name.function)) ||
			(name.packageName == "" && fmtNames["."] && isPrintFunction(name.function)) ||
			(name.packageName == "" && isBuiltinPrintFunction(name.function)):
			if isSharklintCommand(path) {
				return true
			}
			message = "禁止使用 fmt.Print* 直接打印日志，请使用注入的 zap.Logger 对象"
		case zapNames[name.packageName] && isZapGlobalFunction(name.function) ||
			(name.packageName == "" && zapNames["."] && isZapGlobalFunction(name.function)):
			message = "禁止使用 zap 全局 Logger，请使用注入的 zap.Logger 对象"
		case zapNames[name.packageName] && isZapConstructor(name.function) ||
			(name.packageName == "" && zapNames["."] && isZapConstructor(name.function)):
			if !isLoggerFactoryCall(path, file, call.Pos()) {
				message = "禁止在业务代码中创建 zap.Logger，请使用注入的 zap.Logger 对象"
			}
		}
		if message == "" {
			return true
		}

		position := fset.PositionFor(call.Pos(), false)
		if !hasForceIgnoreComment(file, fset, position.Line) {
			*errs = append(*errs, Violation{Position: position, Message: message})
		}
		return true
	})
}

func checkForbiddenLogImports(fset *token.FileSet, file *ast.File, errs *[]error) {
	checkPath := fset.Position(file.Pos()).Filename
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil || (importPath != "log" && importPath != "log/slog") {
			continue
		}
		if importPath == "log/slog" && isSlogAdapterFile(checkPath, file) {
			continue
		}
		position := fset.PositionFor(spec.Pos(), false)
		if !hasForceIgnoreComment(file, fset, position.Line) {
			*errs = append(*errs, Violation{
				Position: position,
				Message:  "禁止导入 log 或 log/slog，请使用注入的 zap.Logger 对象",
			})
		}
	}
}

func isSlogAdapterFile(path string, file *ast.File) bool {
	if file.Name.Name != "sharkdb" {
		return false
	}
	path = filepath.ToSlash(filepath.Clean(path))
	return path == "sharkdb/binloglistener.go" ||
		strings.HasSuffix(path, "/sharkdb/binloglistener.go")
}

func addViolation(fset *token.FileSet, file *ast.File, call *ast.CallExpr, message string, errs *[]error) {
	position := fset.PositionFor(call.Pos(), false)
	if !hasForceIgnoreComment(file, fset, position.Line) {
		*errs = append(*errs, Violation{Position: position, Message: message})
	}
}

func isSharklintCommand(path string) bool {
	// This command's fmt calls produce user-facing usage and diagnostics, not logs.
	path = filepath.ToSlash(filepath.Clean(path))
	return path == "sharklint/cmd/sharklint/main.go" ||
		strings.HasSuffix(path, "/sharklint/cmd/sharklint/main.go")
}

func isLoggerFactoryCall(path string, file *ast.File, position token.Pos) bool {
	if file.Name.Name != "sharklog" || filepath.Base(filepath.Dir(path)) != "sharklog" {
		return false
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == "New" && function.Body != nil &&
			function.Body.Pos() <= position && position <= function.Body.End() {
			return true
		}
	}
	return false
}

type callName struct {
	packageName string
	function    string
}

func calledName(expression ast.Expr) (callName, bool) {
	switch function := expression.(type) {
	case *ast.SelectorExpr:
		if packageIdentifier, ok := function.X.(*ast.Ident); ok {
			return callName{packageName: packageIdentifier.Name, function: function.Sel.Name}, true
		}
	case *ast.Ident:
		return callName{function: function.Name}, true
	}
	return callName{}, false
}

func importedNames(file *ast.File) (fmtNames, zapNames map[string]bool) {
	fmtNames = make(map[string]bool)
	zapNames = make(map[string]bool)

	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		target := map[string]bool(nil)
		switch importPath {
		case "fmt":
			target = fmtNames
		case "go.uber.org/zap":
			target = zapNames
		default:
			continue
		}

		if spec.Name != nil {
			if spec.Name.Name == "." {
				target["."] = true
				continue
			}
			if spec.Name.Name == "_" {
				continue
			}
			target[spec.Name.Name] = true
			continue
		}
		target[filepath.Base(importPath)] = true
	}
	return fmtNames, zapNames
}

func isPrintFunction(name string) bool {
	_, ok := printFunctions[name]
	return ok
}

func isBuiltinPrintFunction(name string) bool {
	return name == "print" || name == "println"
}

func isZapGlobalFunction(name string) bool {
	_, ok := zapGlobalFunctions[name]
	return ok
}

func isZapConstructor(name string) bool {
	_, ok := zapConstructors[name]
	return ok
}

func isZapConfigBuild(call *ast.CallExpr, zapNames map[string]bool) bool {
	method, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || method.Sel.Name != "Build" {
		return false
	}
	switch receiver := method.X.(type) {
	case *ast.CallExpr:
		constructor, ok := calledName(receiver.Fun)
		if !ok || !isZapConfigConstructor(constructor.function) {
			return false
		}
		return zapNames[constructor.packageName] ||
			(constructor.packageName == "" && zapNames["."])
	case *ast.CompositeLit:
		switch configType := receiver.Type.(type) {
		case *ast.SelectorExpr:
			if configType.Sel.Name != "Config" {
				return false
			}
			packageIdentifier, ok := configType.X.(*ast.Ident)
			return ok && zapNames[packageIdentifier.Name]
		case *ast.Ident:
			return configType.Name == "Config" && zapNames["."]
		}
	}
	return false
}

func isZapConfigConstructor(name string) bool {
	return name == "NewDevelopmentConfig" || name == "NewProductionConfig"
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
