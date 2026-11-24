// Package swagger checks that HTTP routes and their Swagger annotations agree.
package swagger

import (
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

var forceIgnorePattern = regexp.MustCompile(`force[ \t]+ignore[ \t]+sharklint[ \t]*:[ \t]*swagger\b`)
var routerPattern = regexp.MustCompile(`^@Router[ \t]+([^ \t]+)[ \t]+\[([A-Za-z]+)\]`)
var paramPattern = regexp.MustCompile(`^@Param[ \t]+([^ \t]+)[ \t]+([^ \t]+)[ \t]+([^ \t]+)[ \t]+([^ \t]+)[ \t]+"([^"]+)"`)
var successPattern = regexp.MustCompile(`^@Success[ \t]+([0-9]{3})[ \t]+\{([^}]+)\}[ \t]+([^ \t]+)`)

var httpMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD"}

// Violation 表示一处路由或 Swagger 注释不匹配。
type Violation struct {
	Position token.Position
	Message  string
}

func (v Violation) Error() string {
	return fmt.Sprintf("%s: %s", v.Position, v.Message)
}

type sourceFile struct {
	pkg  *packages.Package
	file *ast.File
}

type functionDoc struct {
	source     sourceFile
	decl       *ast.FuncDecl
	summary    bool
	routers    []routerDoc
	params     []paramDoc
	successes  []successDoc
	issues     []docIssue
	hasPostDoc bool
}

type docIssue struct {
	position token.Pos
	message  string
}

type routerDoc struct {
	path     string
	method   string
	position token.Pos
}

type paramDoc struct {
	name     string
	location string
	typeName string
	required bool
	position token.Pos
}

type successDoc struct {
	status   int
	category string
	typeName string
	position token.Pos
}

type route struct {
	path     string
	method   string
	handler  ast.Expr
	position token.Pos
	source   sourceFile
	ignored  bool
}

type typeDecl struct {
	source sourceFile
	spec   *ast.TypeSpec
}

// Lint 检查当前构建条件下的 Go 包，要求每条 POST Gin 或 net/http 路由都有匹配的 Swagger 文档。
func Lint(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("检查目录 %q 失败: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("检查目录 %q 失败: 不是目录", dir)
	}

	hasGoFile := false
	err = filepath.WalkDir(dir, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 &&
			filepath.Ext(filePath) == ".go" && !strings.HasSuffix(entry.Name(), "_test.go") {
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
			packages.NeedImports | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return fmt.Errorf("加载 Go 包失败: %w", err)
	}

	files := make([]sourceFile, 0)
	typeDecls := make(map[*types.TypeName]typeDecl)
	var errs []error
	for _, pkg := range pkgs {
		for _, pkgErr := range pkg.Errors {
			errs = append(errs, fmt.Errorf("加载 Go 包失败: %s", pkgErr))
		}
		for _, file := range pkg.Syntax {
			if strings.HasSuffix(pkg.Fset.PositionFor(file.Pos(), false).Filename, "_test.go") {
				continue
			}
			source := sourceFile{pkg: pkg, file: file}
			files = append(files, source)
			for _, decl := range file.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.TYPE {
					continue
				}
				for _, spec := range gen.Specs {
					typeSpec := spec.(*ast.TypeSpec)
					if obj, ok := pkg.TypesInfo.Defs[typeSpec.Name].(*types.TypeName); ok {
						typeDecls[obj] = typeDecl{source: source, spec: typeSpec}
					}
				}
			}
		}
	}

	functions := make(map[*types.Func]*functionDoc)
	ignoredDocs := make(map[*types.Func]bool)
	for _, source := range files {
		for _, decl := range source.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name == nil {
				continue
			}
			obj, _ := source.pkg.TypesInfo.Defs[fn.Name].(*types.Func)
			if obj == nil {
				continue
			}
			doc := parseFunctionDoc(source, fn)
			functions[obj] = doc
			ignoredDocs[obj] = hasForceIgnoreComment(source.file, source.pkg.Fset, source.pkg.Fset.PositionFor(fn.Pos(), false).Line)
		}
	}

	var bases []string
	for _, source := range files {
		bases = append(bases, basePaths(source.file)...)
	}

	groups := collectGroups(files)
	var routes []route
	for _, source := range files {
		ast.Inspect(source.file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			found, routeErrs := routesForCall(source, call, groups)
			errs = append(errs, routeErrs...)
			routes = append(routes, found...)
			return true
		})
	}

	postHandlers := make(map[*types.Func]bool)
	for _, current := range routes {
		if current.method == "POST" {
			if handler := routeHandler(current); handler != nil {
				postHandlers[handler] = true
			}
		}
	}
	for fn, doc := range functions {
		if ignoredDocs[fn] || (!postHandlers[fn] && !doc.hasPostDoc) {
			continue
		}
		for _, issue := range doc.issues {
			appendViolation(&errs, doc.source, issue.position, issue.message)
		}
	}

	matchedDocs := make(map[token.Pos]bool)
	registered := make(map[string]bool)
	for _, current := range routes {
		key := operationKey(current.method, current.path)
		registered[key] = true
		if current.ignored {
			continue
		}
		handler := routeHandler(current)
		doc := functions[handler]
		if doc == nil || doc.decl.Body == nil {
			appendViolation(&errs, current.source, current.position, "路由缺少可关联的 Swagger handler 文档；请将注释放在具名 handler 上")
			continue
		}
		if ignoredDocs[handler] {
			continue
		}

		matching := matchingRouterDocs(doc, current, bases)
		if len(doc.routers) == 0 {
			appendViolation(&errs, current.source, current.position, "路由缺少 @Router Swagger 文档")
			continue
		}
		if len(matching) == 0 {
			for _, router := range doc.routers {
				appendViolation(&errs, doc.source, router.position, fmt.Sprintf("@Router %s [%s] 与实际路由 %s [%s] 不匹配", router.path, router.method, current.path, strings.ToLower(current.method)))
				matchedDocs[router.position] = true
			}
			continue
		}
		for _, router := range matching {
			matchedDocs[router.position] = true
		}
		if !doc.summary {
			appendViolation(&errs, doc.source, doc.decl.Pos(), "Swagger 路由必须包含非空 @Summary")
		}
		if len(doc.successes) == 0 {
			appendViolation(&errs, doc.source, doc.decl.Pos(), "Swagger 路由必须包含 @Success 响应类型")
		}
		checkParams(doc, current, &errs)
		checkSchemas(doc, typeDecls, &errs)
		checkActualParams(doc, &errs)
		checkSuccessTypes(doc, &errs)
		checkActualResponses(doc, typeDecls, &errs)
	}

	for _, function := range functions {
		for _, doc := range function.routers {
			if doc.method != "POST" {
				continue
			}
			obj, _ := function.source.pkg.TypesInfo.Defs[function.decl.Name].(*types.Func)
			if ignoredDocs[obj] {
				continue
			}
			if matchedDocs[doc.position] {
				continue
			}
			isRegistered := registered[operationKey(doc.method, doc.path)]
			for _, base := range bases {
				isRegistered = isRegistered || registered[operationKey(doc.method, joinRoute(base, doc.path))]
			}
			if !isRegistered {
				appendViolation(&errs, function.source, doc.position, fmt.Sprintf("@Router %s [%s] 没有对应的实际路由", doc.path, strings.ToLower(doc.method)))
			}
		}
	}
	return errors.Join(uniqueErrors(errs)...)
}

func uniqueErrors(errs []error) []error {
	seen := make(map[string]bool, len(errs))
	result := make([]error, 0, len(errs))
	for _, err := range errs {
		if err == nil || seen[err.Error()] {
			continue
		}
		seen[err.Error()] = true
		result = append(result, err)
	}
	return result
}

func parseFunctionDoc(source sourceFile, decl *ast.FuncDecl) *functionDoc {
	doc := &functionDoc{source: source, decl: decl}
	if decl.Doc == nil {
		return doc
	}
	for _, line := range commentLines(decl.Doc) {
		switch {
		case strings.HasPrefix(line, "@Summary"):
			doc.summary = strings.TrimSpace(strings.TrimPrefix(line, "@Summary")) != ""
		case strings.HasPrefix(line, "@Router"):
			match := routerPattern.FindStringSubmatch(line)
			if match == nil {
				doc.issues = append(doc.issues, docIssue{position: decl.Doc.Pos(), message: "无效的 @Router 格式，期望 @Router /path [post]"})
				continue
			}
			method := strings.ToUpper(match[2])
			if method == "POST" {
				doc.hasPostDoc = true
			}
			doc.routers = append(doc.routers, routerDoc{
				path: match[1], method: method, position: decl.Doc.Pos(),
			})
		case strings.HasPrefix(line, "@Param"):
			match := paramPattern.FindStringSubmatch(line)
			if match == nil {
				doc.issues = append(doc.issues, docIssue{position: decl.Doc.Pos(), message: "无效的 @Param 格式或缺少字段说明"})
				continue
			}
			required, err := strconv.ParseBool(match[4])
			if err != nil {
				doc.issues = append(doc.issues, docIssue{position: decl.Doc.Pos(), message: "无效的 @Param required 值，期望 true 或 false"})
				continue
			}
			doc.params = append(doc.params, paramDoc{
				name: match[1], location: match[2], typeName: match[3],
				required: required, position: decl.Doc.Pos(),
			})
		case strings.HasPrefix(line, "@Success"):
			match := successPattern.FindStringSubmatch(line)
			if match == nil {
				doc.issues = append(doc.issues, docIssue{position: decl.Doc.Pos(), message: "无效的 @Success 格式，期望 @Success 200 {object} Response"})
				continue
			}
			status, _ := strconv.Atoi(match[1])
			doc.successes = append(doc.successes, successDoc{
				status: status, category: strings.ToLower(match[2]), typeName: match[3], position: decl.Doc.Pos(),
			})
		}
	}
	return doc
}

func commentLines(group *ast.CommentGroup) []string {
	var lines []string
	for _, comment := range group.List {
		text := strings.TrimSpace(comment.Text)
		switch {
		case strings.HasPrefix(text, "//"):
			text = strings.TrimSpace(strings.TrimPrefix(text, "//"))
		case strings.HasPrefix(text, "/*"):
			text = strings.TrimSuffix(strings.TrimPrefix(text, "/*"), "*/")
		}
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "*"))
			if strings.HasPrefix(line, "@") {
				lines = append(lines, line)
			}
		}
	}
	return lines
}

func collectGroups(files []sourceFile) map[types.Object]ast.Expr {
	groups := make(map[types.Object]ast.Expr)
	for _, source := range files {
		ast.Inspect(source.file, func(node ast.Node) bool {
			switch statement := node.(type) {
			case *ast.ValueSpec:
				for i, name := range statement.Names {
					if i < len(statement.Values) && isGinGroupCall(statement.Values[i], source.pkg.TypesInfo) {
						if obj := source.pkg.TypesInfo.Defs[name]; obj != nil {
							groups[obj] = statement.Values[i]
						}
					}
				}
			case *ast.AssignStmt:
				if len(statement.Lhs) != len(statement.Rhs) {
					return true
				}
				for i, lhs := range statement.Lhs {
					if id, ok := ast.Unparen(lhs).(*ast.Ident); ok && isGinGroupCall(statement.Rhs[i], source.pkg.TypesInfo) {
						if obj := source.pkg.TypesInfo.Defs[id]; obj == nil {
							obj = source.pkg.TypesInfo.Uses[id]
							if obj != nil {
								groups[obj] = statement.Rhs[i]
							}
						} else {
							groups[obj] = statement.Rhs[i]
						}
					}
				}
			}
			return true
		})
	}
	return groups
}

func isGinGroupCall(expr ast.Expr, info *types.Info) bool {
	call, ok := ast.Unparen(expr).(*ast.CallExpr)
	if !ok {
		return false
	}
	fn, _ := calledFunc(call, info)
	return fn != nil && fn.Pkg() != nil && fn.Pkg().Path() == "github.com/gin-gonic/gin" && fn.Name() == "Group"
}

func routesForCall(source sourceFile, call *ast.CallExpr, groups map[types.Object]ast.Expr) ([]route, []error) {
	fn, selection := calledFunc(call, source.pkg.TypesInfo)
	if fn == nil || fn.Pkg() == nil {
		return nil, nil
	}
	pkgPath := fn.Pkg().Path()
	offset := 0
	if selection != nil && selection.Kind() == types.MethodExpr {
		offset = 1
	}

	methods := []string(nil)
	pathIndex, handlerIndex := 0, 1
	staticRoute := false
	switch pkgPath {
	case "github.com/gin-gonic/gin":
		methods = ginMethods[fn.Name()]
		switch fn.Name() {
		case "Group":
			return nil, nil
		case "Handle":
			if len(call.Args) <= offset+2 {
				return nil, nil
			}
			method, ok := constantString(source.pkg.TypesInfo, call.Args[offset])
			if !ok {
				var errs []error
				appendViolation(&errs, source, call.Args[offset].Pos(), "Gin Handle 的 HTTP 方法必须是字符串常量")
				return nil, errs
			}
			methods = []string{strings.ToUpper(method)}
			pathIndex, handlerIndex = offset+1, offset+2
		case "Match":
			if len(call.Args) <= offset+1 {
				return nil, nil
			}
			methods = stringSlice(call.Args[offset], source.pkg.TypesInfo)
			if len(methods) == 0 {
				var errs []error
				appendViolation(&errs, source, call.Args[offset].Pos(), "Gin Match 的 HTTP 方法必须是字符串常量列表")
				return nil, errs
			}
			pathIndex, handlerIndex = offset+1, offset+2
		case "Static", "StaticFS":
			methods = []string{"GET", "HEAD"}
			pathIndex, handlerIndex = offset, len(call.Args)
			staticRoute = true
		case "StaticFile":
			methods = []string{"GET", "HEAD"}
			pathIndex, handlerIndex = offset, len(call.Args)
			staticRoute = true
		default:
			pathIndex, handlerIndex = offset, offset+1
		}
	case "net/http":
		if fn.Name() != "Handle" && fn.Name() != "HandleFunc" {
			return nil, nil
		}
		if selection != nil && !isServeMuxMethod(fn) {
			return nil, nil
		}
		pathIndex, handlerIndex = offset, offset+1
		methods = []string{"ANY"}
	default:
		return nil, nil
	}
	if pkgPath == "github.com/gin-gonic/gin" {
		methods = postMethods(methods)
	}
	if len(methods) == 0 {
		return nil, nil
	}
	if pathIndex >= len(call.Args) || handlerIndex >= len(call.Args) {
		return nil, nil
	}

	rawPath, ok := constantString(source.pkg.TypesInfo, call.Args[pathIndex])
	if !ok {
		var errs []error
		appendViolation(&errs, source, call.Args[pathIndex].Pos(), "路由路径必须是字符串常量，才能与 @Router 对应")
		return nil, errs
	}
	if pkgPath == "net/http" {
		if method, routePath, found := splitHTTPPattern(rawPath); found {
			methods = []string{method}
			rawPath = routePath
		}
		methods = postMethods(methods)
		if len(methods) == 0 {
			return nil, nil
		}
	}

	prefix := ""
	if pkgPath == "github.com/gin-gonic/gin" {
		var receiver ast.Expr
		if selection != nil && selection.Kind() == types.MethodExpr {
			if len(call.Args) > 0 {
				receiver = call.Args[0]
			}
		} else if selector, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr); ok {
			receiver = selector.X
		}
		prefix = groupPrefix(receiver, source.pkg.TypesInfo, groups, 0)
	}
	fullPath := normalizeRoute(joinRoute(prefix, rawPath))
	if pkgPath == "github.com/gin-gonic/gin" && (fn.Name() == "Static" || fn.Name() == "StaticFS") {
		fullPath = normalizeRoute(strings.TrimSuffix(fullPath, "/") + "/*filepath")
	}
	handler := call.Args[handlerIndex:]
	if len(handler) == 0 && !staticRoute {
		return nil, nil
	}
	var handlerExpr ast.Expr
	if len(handler) > 0 {
		handlerExpr = handler[len(handler)-1]
	}
	position := source.pkg.Fset.PositionFor(call.Pos(), false)
	ignored := hasForceIgnoreComment(source.file, source.pkg.Fset, position.Line)
	var found []route
	for _, method := range methods {
		found = append(found, route{path: fullPath, method: strings.ToUpper(method), handler: handlerExpr, position: call.Pos(), source: source, ignored: ignored})
	}
	return found, nil
}

func postMethods(methods []string) []string {
	for _, method := range methods {
		if strings.EqualFold(method, "POST") || strings.EqualFold(method, "ANY") {
			return []string{"POST"}
		}
	}
	return nil
}

var ginMethods = map[string][]string{
	"GET": {"GET"}, "POST": {"POST"}, "PUT": {"PUT"}, "PATCH": {"PATCH"},
	"DELETE": {"DELETE"}, "OPTIONS": {"OPTIONS"}, "HEAD": {"HEAD"},
	"Any": httpMethods,
}

func splitHTTPPattern(pattern string) (string, string, bool) {
	parts := strings.SplitN(pattern, " ", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", pattern, false
	}
	return strings.ToUpper(parts[0]), parts[1], true
}

func stringSlice(expr ast.Expr, info *types.Info) []string {
	literal, ok := ast.Unparen(expr).(*ast.CompositeLit)
	if !ok {
		return nil
	}
	var result []string
	for _, element := range literal.Elts {
		value, ok := constantString(info, element)
		if !ok {
			return nil
		}
		result = append(result, strings.ToUpper(value))
	}
	return result
}

func groupPrefix(expr ast.Expr, info *types.Info, groups map[types.Object]ast.Expr, depth int) string {
	if expr == nil || depth > 16 {
		return ""
	}
	switch value := ast.Unparen(expr).(type) {
	case *ast.Ident:
		obj := info.Uses[value]
		if group, ok := groups[obj]; ok {
			return groupPrefix(group, info, groups, depth+1)
		}
	case *ast.CallExpr:
		fn, _ := calledFunc(value, info)
		if fn == nil || fn.Pkg() == nil || fn.Pkg().Path() != "github.com/gin-gonic/gin" || fn.Name() != "Group" {
			return ""
		}
		selector, ok := ast.Unparen(value.Fun).(*ast.SelectorExpr)
		if !ok || len(value.Args) == 0 {
			return ""
		}
		prefix, ok := constantString(info, value.Args[0])
		if !ok {
			return ""
		}
		return joinRoute(groupPrefix(selector.X, info, groups, depth+1), prefix)
	}
	return ""
}

func calledFunc(call *ast.CallExpr, info *types.Info) (*types.Func, *types.Selection) {
	switch fun := ast.Unparen(call.Fun).(type) {
	case *ast.SelectorExpr:
		fn, _ := info.Uses[fun.Sel].(*types.Func)
		return fn, info.Selections[fun]
	case *ast.Ident:
		fn, _ := info.Uses[fun].(*types.Func)
		return fn, nil
	}
	return nil, nil
}

func isServeMuxMethod(fn *types.Func) bool {
	signature, ok := fn.Type().(*types.Signature)
	if !ok || signature.Recv() == nil {
		return false
	}
	receiver := types.Unalias(signature.Recv().Type())
	if pointer, ok := receiver.(*types.Pointer); ok {
		receiver = types.Unalias(pointer.Elem())
	}
	named, ok := receiver.(*types.Named)
	return ok && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == "net/http" && named.Obj().Name() == "ServeMux"
}

func routeHandler(current route) *types.Func {
	switch handler := ast.Unparen(current.handler).(type) {
	case *ast.Ident:
		fn, _ := current.source.pkg.TypesInfo.Uses[handler].(*types.Func)
		return fn
	case *ast.SelectorExpr:
		fn, _ := current.source.pkg.TypesInfo.Uses[handler.Sel].(*types.Func)
		return fn
	case *ast.CallExpr:
		if len(handler.Args) == 1 {
			return routeHandler(route{source: current.source, handler: handler.Args[0]})
		}
	}
	return nil
}

func matchingRouterDocs(doc *functionDoc, current route, bases []string) []routerDoc {
	var result []routerDoc
	for _, router := range doc.routers {
		if strings.ToUpper(router.method) == current.method &&
			pathsMatch(current.path, router.path, bases) {
			result = append(result, router)
		}
	}
	return result
}

func basePaths(file *ast.File) []string {
	var paths []string
	for _, group := range file.Comments {
		for _, line := range commentLines(group) {
			if strings.HasPrefix(line, "@BasePath") {
				fields := strings.Fields(line)
				if len(fields) > 1 {
					paths = append(paths, fields[1])
				}
			}
		}
	}
	return paths
}

func pathsMatch(actual, documented string, bases []string) bool {
	documented = normalizeRoute(documented)
	if actual == documented {
		return true
	}
	for _, base := range bases {
		if actual == normalizeRoute(joinRoute(base, documented)) {
			return true
		}
	}
	return false
}

func checkParams(doc *functionDoc, current route, errs *[]error) {
	expected := pathParams(current.path)
	found := make(map[string]bool)
	for _, param := range doc.params {
		if param.location == "path" {
			found[param.name] = true
			if !param.required {
				appendViolation(errs, doc.source, param.position, fmt.Sprintf("路径参数 %q 必须标记为 required=true", param.name))
			}
		}
		if !validParamLocation(param.location) {
			appendViolation(errs, doc.source, param.position, fmt.Sprintf("@Param %q 使用了无效位置 %q", param.name, param.location))
		}
		paramType := resolveType(param.typeName, doc.source)
		if !validSwaggerType(param.typeName, doc.source) ||
			(param.location != "body" && param.location != "formData" && !validParameterType(paramType)) {
			appendViolation(errs, doc.source, param.position, fmt.Sprintf("@Param %q 的类型 %q 无法解析", param.name, param.typeName))
		}
		if param.location == "body" && paramType == nil {
			appendViolation(errs, doc.source, param.position, fmt.Sprintf("body 参数 %q 必须引用有效的 Go 类型", param.name))
		}
	}
	expectedNames := sortedKeys(expected)
	for _, name := range expectedNames {
		if !found[name] {
			appendViolation(errs, doc.source, doc.decl.Pos(), fmt.Sprintf("路由路径参数 %q 缺少对应的 @Param %s path", name, name))
		}
	}
	for _, name := range sortedKeys(found) {
		if !expected[name] {
			appendViolation(errs, doc.source, doc.decl.Pos(), fmt.Sprintf("@Param %q 声明为 path 参数，但路由路径中不存在该参数", name))
		}
	}
}

func validParamLocation(location string) bool {
	switch location {
	case "path", "query", "header", "body", "formData", "cookie":
		return true
	default:
		return false
	}
}

func checkSchemas(doc *functionDoc, declarations map[*types.TypeName]typeDecl, errs *[]error) {
	seen := make(map[types.Type]bool)
	for _, param := range doc.params {
		if param.location == "body" {
			checkTypeFields(resolveType(param.typeName, doc.source), declarations, seen, errs)
		}
	}
	for _, success := range doc.successes {
		if typ := resolveType(success.typeName, doc.source); typ != nil {
			checkTypeFields(typ, declarations, seen, errs)
		}
	}
}

func checkTypeFields(typ types.Type, declarations map[*types.TypeName]typeDecl, seen map[types.Type]bool, errs *[]error) {
	if typ == nil {
		return
	}
	typ = types.Unalias(typ)
	if isDecimalType(typ) {
		return
	}
	if seen[typ] {
		return
	}
	seen[typ] = true
	switch value := typ.(type) {
	case *types.Pointer:
		checkTypeFields(value.Elem(), declarations, seen, errs)
	case *types.Slice:
		checkTypeFields(value.Elem(), declarations, seen, errs)
	case *types.Array:
		checkTypeFields(value.Elem(), declarations, seen, errs)
	case *types.Map:
		checkTypeFields(value.Elem(), declarations, seen, errs)
	case *types.Named:
		declaration, ok := declarations[value.Obj()]
		if !ok {
			return
		}
		structure, ok := declaration.spec.Type.(*ast.StructType)
		if !ok {
			checkTypeFields(value.Underlying(), declarations, seen, errs)
			return
		}
		for _, field := range structure.Fields.List {
			if len(field.Names) > 0 && fieldComment(field) == "" {
				for _, name := range field.Names {
					appendViolation(errs, declaration.source, name.Pos(), fmt.Sprintf("Swagger 结构体 %s 的字段 %s 必须有注释", value.Obj().Name(), name.Name))
				}
			}
			fieldType := declaration.source.pkg.TypesInfo.TypeOf(field.Type)
			checkTypeFields(fieldType, declarations, seen, errs)
		}
	case *types.Struct:
		for i := 0; i < value.NumFields(); i++ {
			checkTypeFields(value.Field(i).Type(), declarations, seen, errs)
		}
	}
}

func isDecimalType(typ types.Type) bool {
	named, ok := dereference(typ).(*types.Named)
	return ok && named.Obj().Name() == "Decimal" && named.Obj().Pkg() != nil &&
		named.Obj().Pkg().Path() == "github.com/shopspring/decimal"
}

func checkSuccessTypes(doc *functionDoc, errs *[]error) {
	for _, success := range doc.successes {
		if success.status < 200 || success.status >= 300 {
			appendViolation(errs, doc.source, success.position, fmt.Sprintf("@Success 状态码 %d 必须是 2xx", success.status))
		}
		typ := resolveType(success.typeName, doc.source)
		if typ == nil {
			appendViolation(errs, doc.source, success.position, fmt.Sprintf("@Success 类型 %q 无法解析", success.typeName))
			continue
		}
		if !validSuccessCategory(success.category, typ) {
			appendViolation(errs, doc.source, success.position, fmt.Sprintf("@Success {%s} 与类型 %q 不匹配", success.category, success.typeName))
		}
	}
}

func fieldComment(field *ast.Field) string {
	var comments []string
	if field.Doc != nil {
		comments = append(comments, field.Doc.Text())
	}
	if field.Comment != nil {
		comments = append(comments, field.Comment.Text())
	}
	return strings.TrimSpace(strings.Join(comments, ""))
}

func checkActualResponses(doc *functionDoc, declarations map[*types.TypeName]typeDecl, errs *[]error) {
	if doc.decl.Body == nil {
		return
	}
	successStatuses := make(map[int]bool)
	hasGinJSONSuccessResponse := false
	ast.Inspect(doc.decl.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		fn, _ := calledFunc(call, doc.source.pkg.TypesInfo)
		if fn == nil || fn.Pkg() == nil || fn.Pkg().Path() != "github.com/gin-gonic/gin" ||
			(fn.Name() != "JSON" && fn.Name() != "IndentedJSON" && fn.Name() != "SecureJSON" &&
				fn.Name() != "PureJSON" && fn.Name() != "AsciiJSON") || len(call.Args) < 2 {
			return true
		}
		status, ok := constantInt(doc.source.pkg.TypesInfo, call.Args[0])
		if !ok || status < 200 || status >= 400 {
			return true
		}
		actual := doc.source.pkg.TypesInfo.TypeOf(call.Args[1])
		if actual == nil {
			return true
		}
		checkTypeFields(actual, declarations, make(map[types.Type]bool), errs)
		if isSharkErrorResponse(actual) {
			return true
		}
		hasGinJSONSuccessResponse = true
		successStatuses[status] = true
		for _, success := range doc.successes {
			if success.status == status {
				expected := resolveType(success.typeName, doc.source)
				if expected != nil && !sameSwaggerType(actual, expected) {
					appendViolation(errs, doc.source, success.position, fmt.Sprintf("@Success %d 声明类型 %q 与 Gin JSON 实际响应类型 %s 不一致", status, success.typeName, types.TypeString(actual, nil)))
				}
				return true
			}
		}
		appendViolation(errs, doc.source, call.Pos(), fmt.Sprintf("Gin 返回成功状态 %d，但缺少对应的 @Success 注释", status))
		return true
	})
	if hasGinJSONSuccessResponse {
		for _, success := range doc.successes {
			if success.status >= 200 && success.status < 300 && !successStatuses[success.status] {
				appendViolation(errs, doc.source, success.position, fmt.Sprintf("@Success %d 没有对应的 Gin JSON 成功响应", success.status))
			}
		}
	}
}

func isSharkErrorResponse(typ types.Type) bool {
	named, ok := dereference(types.Unalias(typ)).(*types.Named)
	return ok && named.Obj().Name() == "Error" && named.Obj().Pkg() != nil &&
		named.Obj().Pkg().Path() == "github.com/xcodego/shark/sharkerror"
}

func checkActualParams(doc *functionDoc, errs *[]error) {
	if doc.decl.Body == nil {
		return
	}
	ast.Inspect(doc.decl.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		fn, _ := calledFunc(call, doc.source.pkg.TypesInfo)
		if fn == nil || fn.Pkg() == nil || fn.Pkg().Path() != "github.com/gin-gonic/gin" ||
			(fn.Name() != "ShouldBind" && fn.Name() != "ShouldBindJSON" &&
				fn.Name() != "Bind" && fn.Name() != "BindJSON" && fn.Name() != "ShouldBindBodyWith") ||
			len(call.Args) == 0 {
			return true
		}
		actual := doc.source.pkg.TypesInfo.TypeOf(call.Args[0])
		if actual == nil {
			return true
		}
		var bodyParams []paramDoc
		for _, param := range doc.params {
			if param.location == "body" {
				bodyParams = append(bodyParams, param)
			}
		}
		if len(bodyParams) == 0 {
			appendViolation(errs, doc.source, call.Pos(), "Gin 请求体绑定缺少对应的 @Param body 类型")
			return true
		}
		for _, param := range bodyParams {
			if expected := resolveType(param.typeName, doc.source); expected != nil && sameSwaggerType(actual, expected) {
				return true
			}
		}
		appendViolation(errs, doc.source, bodyParams[0].position, fmt.Sprintf("@Param body 类型与 Gin 绑定的实际类型 %s 不一致", types.TypeString(dereference(types.Unalias(actual)), nil)))
		return true
	})
}

func validSuccessCategory(category string, typ types.Type) bool {
	typ = dereference(types.Unalias(typ))
	switch category {
	case "object":
		switch types.Unalias(typ).Underlying().(type) {
		case *types.Struct, *types.Map, *types.Interface:
			return true
		}
	case "array":
		switch types.Unalias(typ).Underlying().(type) {
		case *types.Slice, *types.Array:
			return true
		}
	case "string":
		basic, ok := types.Unalias(typ).Underlying().(*types.Basic)
		return ok && basic.Info()&types.IsString != 0
	case "integer":
		basic, ok := types.Unalias(typ).Underlying().(*types.Basic)
		return ok && basic.Info()&types.IsInteger != 0
	case "number":
		basic, ok := types.Unalias(typ).Underlying().(*types.Basic)
		return ok && basic.Info()&types.IsFloat != 0
	case "boolean":
		basic, ok := types.Unalias(typ).Underlying().(*types.Basic)
		return ok && basic.Info()&types.IsBoolean != 0
	}
	return false
}

func validParameterType(typ types.Type) bool {
	if typ == nil {
		return false
	}
	typ = types.Unalias(typ)
	switch value := typ.Underlying().(type) {
	case *types.Basic:
		return value.Info()&(types.IsString|types.IsBoolean|types.IsInteger|types.IsFloat) != 0
	case *types.Slice:
		basic, ok := types.Unalias(value.Elem()).Underlying().(*types.Basic)
		return ok && basic.Info()&(types.IsString|types.IsBoolean|types.IsInteger|types.IsFloat) != 0
	case *types.Array:
		basic, ok := types.Unalias(value.Elem()).Underlying().(*types.Basic)
		return ok && basic.Info()&(types.IsString|types.IsBoolean|types.IsInteger|types.IsFloat) != 0
	default:
		return false
	}
}

func validSwaggerType(typeName string, source sourceFile) bool {
	if typeName == "file" {
		return true
	}
	return resolveType(typeName, source) != nil
}

func resolveType(typeName string, source sourceFile) types.Type {
	expr, err := parser.ParseExpr(typeName)
	if err != nil {
		return nil
	}
	return resolveTypeExpr(expr, source)
}

func resolveTypeExpr(expr ast.Expr, source sourceFile) types.Type {
	switch value := expr.(type) {
	case *ast.Ident:
		if obj := source.pkg.Types.Scope().Lookup(value.Name); obj != nil {
			if typeName, ok := obj.(*types.TypeName); ok {
				return typeName.Type()
			}
			return nil
		}
		if obj := types.Universe.Lookup(value.Name); obj != nil {
			return obj.Type()
		}
	case *ast.SelectorExpr:
		alias, ok := value.X.(*ast.Ident)
		if !ok {
			return nil
		}
		for _, spec := range source.file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue
			}
			var importedName string
			var importPkg *types.Package
			if spec.Name != nil {
				importedName = spec.Name.Name
				if pkgName, ok := source.pkg.TypesInfo.Defs[spec.Name].(*types.PkgName); ok {
					importPkg = pkgName.Imported()
				}
			} else if pkgName, ok := source.pkg.TypesInfo.Implicits[spec].(*types.PkgName); ok {
				importedName = pkgName.Name()
				importPkg = pkgName.Imported()
			}
			if importedName != alias.Name {
				continue
			}
			if importPkg == nil {
				if imported := source.pkg.Imports[importPath]; imported != nil {
					importPkg = imported.Types
				}
			}
			if importPkg != nil {
				if obj, ok := importPkg.Scope().Lookup(value.Sel.Name).(*types.TypeName); ok {
					return obj.Type()
				}
			}
		}
	case *ast.StarExpr:
		if element := resolveTypeExpr(value.X, source); element != nil {
			return types.NewPointer(element)
		}
	case *ast.ArrayType:
		element := resolveTypeExpr(value.Elt, source)
		if element == nil {
			return nil
		}
		if value.Len == nil {
			return types.NewSlice(element)
		}
		length, ok := constantInt(source.pkg.TypesInfo, value.Len)
		if !ok {
			literal, isLiteral := ast.Unparen(value.Len).(*ast.BasicLit)
			if isLiteral {
				parsed, err := strconv.ParseInt(literal.Value, 0, 64)
				if err == nil {
					length, ok = int(parsed), parsed >= 0 && int64(int(parsed)) == parsed
				}
			}
		}
		if !ok || length < 0 {
			return nil
		}
		return types.NewArray(element, int64(length))
	case *ast.MapType:
		key := resolveTypeExpr(value.Key, source)
		element := resolveTypeExpr(value.Value, source)
		if key != nil && element != nil {
			return types.NewMap(key, element)
		}
	case *ast.StructType:
		if len(value.Fields.List) == 0 {
			return types.NewStruct(nil, nil)
		}
	case *ast.InterfaceType:
		return types.NewInterfaceType(nil, nil).Complete()
	}
	return nil
}

func sameSwaggerType(actual, expected types.Type) bool {
	actual = dereference(types.Unalias(actual))
	expected = dereference(types.Unalias(expected))
	if types.Identical(actual, expected) {
		return true
	}
	return types.Identical(actual.Underlying(), expected.Underlying())
}

func dereference(typ types.Type) types.Type {
	for {
		pointer, ok := typ.(*types.Pointer)
		if !ok {
			return typ
		}
		typ = types.Unalias(pointer.Elem())
	}
}

func pathParams(routePath string) map[string]bool {
	result := make(map[string]bool)
	for _, segment := range strings.Split(routePath, "/") {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			name := strings.TrimSuffix(strings.TrimPrefix(segment, "{"), "}")
			if before, ok := strings.CutSuffix(name, "..."); ok {
				name = before
			}
			result[name] = true
		}
	}
	return result
}

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func normalizeRoute(routePath string) string {
	routePath = strings.TrimSpace(routePath)
	if routePath == "" {
		return "/"
	}
	parts := strings.Split(routePath, "/")
	for i, segment := range parts {
		if len(segment) > 1 && (segment[0] == ':' || segment[0] == '*') {
			parts[i] = "{" + strings.TrimSuffix(segment[1:], "...") + "}"
		} else if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "...}") {
			parts[i] = strings.TrimSuffix(segment, "...}")
			parts[i] += "}"
		}
	}
	result := path.Clean(strings.Join(parts, "/"))
	if !strings.HasPrefix(result, "/") {
		result = "/" + result
	}
	return result
}

func joinRoute(prefix, routePath string) string {
	return "/" + strings.Trim(strings.TrimSpace(prefix)+"/"+strings.TrimSpace(routePath), "/")
}

func operationKey(method, routePath string) string {
	return strings.ToUpper(method) + " " + normalizeRoute(routePath)
}

func constantString(info *types.Info, expr ast.Expr) (string, bool) {
	value := info.Types[expr].Value
	if value == nil || value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(value), true
}

func constantInt(info *types.Info, expr ast.Expr) (int, bool) {
	value := info.Types[expr].Value
	if value == nil || value.Kind() != constant.Int {
		return 0, false
	}
	number, ok := constant.Int64Val(value)
	return int(number), ok && int64(int(number)) == number
}

func appendViolation(errs *[]error, source sourceFile, position token.Pos, message string) {
	if !position.IsValid() {
		position = source.file.Pos()
	}
	line := source.pkg.Fset.PositionFor(position, false).Line
	if hasForceIgnoreComment(source.file, source.pkg.Fset, line) {
		return
	}
	*errs = append(*errs, Violation{Position: source.pkg.Fset.PositionFor(position, false), Message: message})
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
