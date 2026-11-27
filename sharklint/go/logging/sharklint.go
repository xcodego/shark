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

// forceIgnorePattern 匹配 "force ignore sharklint: logging" 豁免标记。
var forceIgnorePattern = regexp.MustCompile(`force[ \t]+ignore[ \t]+sharklint[ \t]*:[ \t]*logging\b`)

// printFunctions 是 fmt 包中被视为直接打印日志的函数。
var printFunctions = map[string]struct{}{
	"Print": {}, "Println": {}, "Printf": {},
	"Fprint": {}, "Fprintln": {}, "Fprintf": {},
}

// zapGlobalFunctions 是 zap 包中访问或修改全局 Logger 的函数。
var zapGlobalFunctions = map[string]struct{}{
	"L": {}, "S": {}, "ReplaceGlobals": {}, "RedirectStdLog": {},
}

// zapConstructors 是 zap 包中直接创建 Logger 的构造函数。
var zapConstructors = map[string]struct{}{
	"New": {}, "NewDevelopment": {}, "NewExample": {},
	"NewNop": {}, "NewProduction": {},
}

// Violation 表示一处绕过注入式 zap.Logger 的日志调用。
type Violation struct {
	// Position 是违规调用或导入的位置。
	Position token.Position
	// Message 描述具体违规原因。
	Message string
}

// Error 返回带位置信息的违规描述。
func (v Violation) Error() string {
	return fmt.Sprintf("%s: %s", v.Position, v.Message)
}

// Lint 递归检查 dir 中所有非测试 .go 文件，不跟随符号链接。
// 检查禁止直接打印（fmt.Print*、内置 print/println）、导入标准库 log 或 log/slog、使用 zap 全局 Logger，
// 以及在业务代码中创建 Logger（仅 sharklog 包的 New 函数可以创建）。
// 可在违规行的上一行用 force ignore 注释豁免。
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

// checkFile 检查单个文件中的违规导入和调用, 结果追加到 errs。
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

// checkForbiddenLogImports 报告对 log 与 log/slog 的导入; sharkdb 的 binloglistener.go 因 go-mysql 需要 slog 适配而例外。
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

// isSlogAdapterFile 判断 file 是否为允许导入 log/slog 的 sharkdb/binloglistener.go。
func isSlogAdapterFile(path string, file *ast.File) bool {
	if file.Name.Name != "sharkdb" {
		return false
	}
	path = filepath.ToSlash(filepath.Clean(path))
	return path == "sharkdb/binloglistener.go" ||
		strings.HasSuffix(path, "/sharkdb/binloglistener.go")
}

// addViolation 在 call 所在行未被豁免时追加一条违规。
func addViolation(fset *token.FileSet, file *ast.File, call *ast.CallExpr, message string, errs *[]error) {
	position := fset.PositionFor(call.Pos(), false)
	if !hasForceIgnoreComment(file, fset, position.Line) {
		*errs = append(*errs, Violation{Position: position, Message: message})
	}
}

// isSharklintCommand 判断 path 是否为 sharklint 命令入口; 其 fmt 输出是面向用户的用法和诊断信息, 不属于日志。
func isSharklintCommand(path string) bool {
	path = filepath.ToSlash(filepath.Clean(path))
	return path == "sharklint/cmd/sharklint/main.go" ||
		strings.HasSuffix(path, "/sharklint/cmd/sharklint/main.go")
}

// isLoggerFactoryCall 判断 position 是否位于 sharklog 包的 New 函数体内, 只有这里允许创建 Logger。
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

// callName 是被调用函数的名称; 非限定调用时 packageName 为空。
type callName struct {
	packageName string
	function    string
}

// calledName 解析形如 pkg.Func 或 Func 的调用目标; 其他形式（如方法链）返回 false。
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

// importedNames 返回文件中 fmt 与 go.uber.org/zap 的本地引用名（含别名, "." 表示点导入, 忽略 _ 导入）。
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

// isPrintFunction 判断 name 是否为 fmt 的打印函数。
func isPrintFunction(name string) bool {
	_, ok := printFunctions[name]
	return ok
}

// isBuiltinPrintFunction 判断 name 是否为内置 print 或 println。
func isBuiltinPrintFunction(name string) bool {
	return name == "print" || name == "println"
}

// isZapGlobalFunction 判断 name 是否为 zap 的全局 Logger 相关函数。
func isZapGlobalFunction(name string) bool {
	_, ok := zapGlobalFunctions[name]
	return ok
}

// isZapConstructor 判断 name 是否为 zap 的 Logger 构造函数。
func isZapConstructor(name string) bool {
	_, ok := zapConstructors[name]
	return ok
}

// isZapConfigBuild 判断 call 是否为通过 zap.Config（配置构造函数或字面量）调用 Build 创建 Logger。
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

// isZapConfigConstructor 判断 name 是否为返回 zap.Config 的构造函数。
func isZapConfigConstructor(name string) bool {
	return name == "NewDevelopmentConfig" || name == "NewProductionConfig"
}

// hasForceIgnoreComment 判断 callLine 的上一行是否以豁免注释结尾。
func hasForceIgnoreComment(file *ast.File, fset *token.FileSet, callLine int) bool {
	for _, group := range file.Comments {
		if fset.PositionFor(group.End(), false).Line == callLine-1 &&
			forceIgnorePattern.MatchString(group.Text()) {
			return true
		}
	}
	return false
}
