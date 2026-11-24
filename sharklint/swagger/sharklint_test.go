package swagger_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xcodego/shark/sharklint/swagger"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func newFixture(t *testing.T, source string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), `module example.com/app

go 1.26

require github.com/gin-gonic/gin v0.0.0
replace github.com/gin-gonic/gin => ./gin
`)
	writeFile(t, filepath.Join(dir, "gin", "go.mod"), "module github.com/gin-gonic/gin\n\ngo 1.26\n")
	writeFile(t, filepath.Join(dir, "gin", "gin.go"), `package gin
type HandlerFunc func(*Context)
type Context struct{}
func (*Context) JSON(int, any) {}
func (*Context) ShouldBindJSON(any) error { return nil }
type Engine struct{}
type RouterGroup struct{}
func (*Engine) Group(string, ...HandlerFunc) *RouterGroup { return nil }
func (*Engine) GET(string, ...HandlerFunc) {}
func (*Engine) POST(string, ...HandlerFunc) {}
func (*Engine) Handle(string, string, ...HandlerFunc) {}
func (*Engine) Match([]string, string, ...HandlerFunc) {}
func (*RouterGroup) Group(string, ...HandlerFunc) *RouterGroup { return nil }
func (*RouterGroup) GET(string, ...HandlerFunc) {}
func (*RouterGroup) POST(string, ...HandlerFunc) {}
func (*RouterGroup) Handle(string, string, ...HandlerFunc) {}
`)
	writeFile(t, filepath.Join(dir, "main.go"), source)
	return dir
}

func TestLintAcceptsMatchingGinRouteAndSchemas(t *testing.T) {
	dir := newFixture(t, `package main
import (
	"github.com/gin-gonic/gin"
)

// @BasePath /api

type UserResponse struct {
	ID int `+"`json:\"id\"`"+` // User identifier.
}

type UserRequest struct {
	Name string `+"`json:\"name\"`"+` // User display name.
}

// GetUser returns a user.
// @Summary Get a user
// @Param id path int true "User identifier"
// @Param request body UserRequest true "User data"
// @Success 200 {object} UserResponse
// @Router /users/{id} [post]
func getUser(c *gin.Context) {
	c.JSON(200, UserResponse{})
}

func routes(router *gin.Engine) {
	router.Group("/api").POST("/users/:id", getUser)
}
`)
	if err := swagger.Lint(dir); err != nil {
		t.Fatalf("匹配的路由和 Swagger 文档不应报错: %v", err)
	}
}

func TestLintAllowsEmptyParamAndSuccessStructs(t *testing.T) {
	dir := newFixture(t, `package main
import "github.com/gin-gonic/gin"

// @Summary Accept empty payload
// @Param payload body struct{} true "Empty payload"
// @Success 200 {object} struct{}
// @Router /empty [post]
func empty(c *gin.Context) {
	c.JSON(200, struct{}{})
}

func routes(router *gin.Engine) {
	router.POST("/empty", empty)
}
`)
	if err := swagger.Lint(dir); err != nil {
		t.Fatalf("空结构体参数和响应应当通过检查: %v", err)
	}
}

func TestLintRequiresCommentsOnEmbeddedStructFields(t *testing.T) {
	dir := newFixture(t, `package main
import "github.com/gin-gonic/gin"

type Embedded struct {
	Missing int
}

type Response struct {
	Embedded
}

// @Summary Read embedded data
// @Success 200 {object} Response
// @Router /embedded [post]
func read(c *gin.Context) {
	c.JSON(200, Response{})
}

func routes(router *gin.Engine) {
	router.POST("/embedded", read)
}
`)
	err := swagger.Lint(dir)
	if err == nil ||
		!strings.Contains(err.Error(), "Swagger 结构体 Embedded 的字段 Missing 必须有注释") ||
		strings.Contains(err.Error(), "Swagger 结构体 Response 的字段") {
		t.Fatalf("应检查嵌入结构体的字段，但不要求嵌入成员自身注释: %v", err)
	}
}

func TestLintRecursesIntoImportedNestedStructs(t *testing.T) {
	dir := newFixture(t, `package main
import (
	"github.com/gin-gonic/gin"
	"example.com/app/models"
)

// @Summary Read nested data
// @Success 200 {object} models.Outer
// @Router /nested [post]
func read(c *gin.Context) {
	c.JSON(200, models.Outer{})
}

func routes(router *gin.Engine) {
	router.POST("/nested", read)
}
`)
	writeFile(t, filepath.Join(dir, "models", "models.go"), `package models
type Base struct {
	ID int // Identifier.
}
type Inner struct {
	Nested Deep // Nested value.
}
type Deep struct {
	Missing string
}
type Outer struct {
	Base
	Details Inner // Nested details.
}
`)
	err := swagger.Lint(dir)
	if err == nil || !strings.Contains(err.Error(), "Swagger 结构体 Deep 的字段 Missing 必须有注释") {
		t.Fatalf("应递归检查导入结构体的匿名及具名嵌套字段: %v", err)
	}
	if strings.Contains(err.Error(), "Swagger 结构体 Outer 的字段 Base") {
		t.Fatalf("嵌入成员 Base 本身不要求注释: %v", err)
	}
}

func TestLintResolvesImportedRequestAndResponseTypes(t *testing.T) {
	dir := newFixture(t, `package main
import (
	"github.com/gin-gonic/gin"
	types "example.com/app/models"
)

// @Summary Bind imported request
// @Param body body types.NextRoundReq true "Round request"
// @Success 200 {object} types.NextRoundResp
// @Router /round [post]
func nextRound(c *gin.Context) {
	var request types.NextRoundReq
	_ = c.ShouldBindJSON(&request)
	c.JSON(200, types.NextRoundResp{})
}

func routes(router *gin.Engine) {
	router.POST("/round", nextRound)
}
`)
	writeFile(t, filepath.Join(dir, "models", "models.go"), `package models
type NextRoundReq struct {
	Round int // Requested round.
}
type NextRoundResp struct {
	Accepted bool // Whether the round was accepted.
}
`)
	if err := swagger.Lint(dir); err != nil {
		t.Fatalf("应能解析同一模块中的导入结构体: %v", err)
	}
}

func TestLintAllowsSharkErrorsAlongsideSuccessResponses(t *testing.T) {
	dir := newFixture(t, `package main
import (
	"github.com/gin-gonic/gin"
	"github.com/xcodego/shark/sharkerror"
)

type GameSceneResp struct {
	Scene string // Current scene.
}

// @Summary Get the current scene
// @Success 200 {object} GameSceneResp
// @Router /scene [post]
func getScene(c *gin.Context) {
	if false {
		c.JSON(200, &sharkerror.Error{})
		return
	}
	c.JSON(200, GameSceneResp{})
}

func routes(router *gin.Engine) {
	router.POST("/scene", getScene)
}
`)
	writeFile(t, filepath.Join(dir, "go.mod"), `module example.com/app

go 1.26

require (
	github.com/gin-gonic/gin v0.0.0
	github.com/xcodego/shark v0.0.0
)
replace github.com/gin-gonic/gin => ./gin
replace github.com/xcodego/shark => ./shark
`)
	writeFile(t, filepath.Join(dir, "shark", "go.mod"), "module github.com/xcodego/shark\n\ngo 1.26\n")
	writeFile(t, filepath.Join(dir, "shark", "sharkerror", "error.go"), `package sharkerror
type Error struct {
	Code int
}
`)
	if err := swagger.Lint(dir); err != nil {
		t.Fatalf("sharkerror.Error 可作为 HTTP 200 错误响应并与成功响应并存: %v", err)
	}
}

func TestLintSupportsGinHandleMatchAndNetHTTPMux(t *testing.T) {
	dir := newFixture(t, `package main
import (
	"net/http"
	"github.com/gin-gonic/gin"
)

type Response struct {
	ID int // Identifier.
}

// @Summary Created
// @Success 201 {object} Response
// @Router /created [post]
func created(c *gin.Context) {
	c.JSON(201, Response{})
}

// @Summary Matched
// @Success 200 {object} Response
// @Router /matched [post]
func matched(c *gin.Context) {
	c.JSON(200, Response{})
}

// @Summary Read with the standard library
// @Param id path string true "Identifier"
// @Success 200 {object} Response
// @Router /items/{id} [post]
func readItem(w http.ResponseWriter, r *http.Request) {}

func routes(router *gin.Engine, mux *http.ServeMux) {
	router.Handle("POST", "/created", created)
	router.Match([]string{"POST"}, "/matched", matched)
	mux.HandleFunc("POST /items/{id}", readItem)
}
`)
	if err := swagger.Lint(dir); err != nil {
		t.Fatalf("Gin Handle/Match 和 net/http 路由应通过检查: %v", err)
	}
}

func TestLintReportsRouteAndSwaggerMismatches(t *testing.T) {
	dir := newFixture(t, `package main
import "github.com/gin-gonic/gin"

type Response struct {
	ID int
}

type ActualRequest struct {
	Name string // Actual name.
}

type DocumentedRequest struct {
	ID int // Documented identifier.
}

// @Summary Wrong path
// @Success 200 {object} Response
// @Router /wrong [post]
func wrongPath(c *gin.Context) {
	c.JSON(200, Response{})
}

// @Summary Wrong success type
// @Success 200 {object} string
// @Router /success [post]
func wrongSuccess(c *gin.Context) {
	c.JSON(200, Response{})
}

// @Summary Bind request
// @Param request body DocumentedRequest true "Request data"
// @Success 200 {object} Response
// @Router /bind [post]
func wrongRequest(c *gin.Context) {
	var request ActualRequest
	_ = c.ShouldBindJSON(&request)
	c.JSON(200, Response{})
}

func routes(router *gin.Engine) {
	router.POST("/path", wrongPath)
	router.POST("/success", wrongSuccess)
	router.POST("/bind", wrongRequest)
	router.POST("/missing/:id", func(c *gin.Context) {})
}
`)
	err := swagger.Lint(dir)
	if err == nil {
		t.Fatal("应报告 Swagger 路由和结构体问题")
	}
	var violation swagger.Violation
	if !errors.As(err, &violation) {
		t.Fatalf("错误中应包含 Violation: %v", err)
	}
	for _, want := range []string{
		"@Router /wrong [POST] 与实际路由 /path [post] 不匹配",
		"@Success {object} 与类型 \"string\" 不匹配",
		"与 Gin JSON 实际响应类型 example.com/app.Response 不一致",
		"@Param body 类型与 Gin 绑定的实际类型 example.com/app.ActualRequest 不一致",
		"路由缺少可关联的 Swagger handler 文档",
		"Swagger 结构体 Response 的字段 ID 必须有注释",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误应包含 %q，实际为 %v", want, err)
		}
	}
}

func TestLintChecksPathParamsAndOrphanDocs(t *testing.T) {
	dir := newFixture(t, `package main
import "github.com/gin-gonic/gin"

type Response struct {
	ID int // Identifier.
}

// @Summary Get by ID
// @Success 200 {object} Response
// @Router /items/{id} [post]
func getItem(c *gin.Context) {
	c.JSON(200, Response{})
}

// @Summary Never registered
// @Success 200 {object} Response
// @Router /orphan [post]
func orphan(c *gin.Context) {}

func routes(router *gin.Engine) {
	router.POST("/items/:id", getItem)
}
`)
	err := swagger.Lint(dir)
	if err == nil ||
		!strings.Contains(err.Error(), `路由路径参数 "id" 缺少对应的 @Param id path`) ||
		!strings.Contains(err.Error(), "@Router /orphan [post] 没有对应的实际路由") {
		t.Fatalf("应报告缺失路径参数和未注册文档，实际为 %v", err)
	}
}

func TestLintIgnoresNonRoutersAndSupportsForceIgnore(t *testing.T) {
	dir := newFixture(t, `package main
type unrelated struct{}
func (unrelated) GET(string, func()) {}
func run() { unrelated{}.GET("/not-a-route", func() {}) }
`)
	if err := swagger.Lint(dir); err != nil {
		t.Fatalf("不应把无关的同名方法当成路由: %v", err)
	}

	writeFile(t, filepath.Join(dir, "main.go"), `package main
	import (
		"net/http"
		"github.com/gin-gonic/gin"
	)

	// @Summary Legacy GET
	// @Param malformed annotation
	// @Router /legacy [get]
	func legacy(c *gin.Context) {}

func routes(router *gin.Engine) {
	path := "/ignored"
	// force ignore sharklint: swagger
	router.POST(path, func(c *gin.Context) {})
	router.GET("/non-post", func(c *gin.Context) {})
		router.GET("/legacy", legacy)
		http.HandleFunc("GET /standard-get", func(http.ResponseWriter, *http.Request) {})
}
`)
	if err := swagger.Lint(dir); err != nil {
		t.Fatalf("应尊重 force ignore 注释: %v", err)
	}
}

func TestLintErrors(t *testing.T) {
	dir := t.TempDir()
	if err := swagger.Lint(dir); err != nil {
		t.Fatalf("空目录不应报错: %v", err)
	}
	if err := swagger.Lint(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("应报告目录不存在: %v", err)
	}
	file := filepath.Join(dir, "main.go")
	writeFile(t, file, "package broken\nfunc broken( {\n")
	if err := swagger.Lint(dir); err == nil || !strings.Contains(err.Error(), "加载 Go 包失败") {
		t.Fatalf("应报告包解析错误: %v", err)
	}
}
