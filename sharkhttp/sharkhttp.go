// Package sharkhttp 提供了基于 Gin 框架的 HTTP 服务初始化。
//
// 封装了 Gin 引擎的创建、中间件注册和 Swagger 文档服务的配置，
// 为 Shark 应用提供开箱即用的 HTTP 服务能力。
//
// 内置中间件:
//   - recoveryMiddleware: panic 恢复中间件，防止服务崩溃
//   - corsMiddleware: CORS 跨域中间件，允许跨域请求
//   - errorMiddleware: 统一错误处理中间件，将 Gin 错误转为 sharkerror 格式
//
// 开发环境额外启用 Swagger 文档服务。
package sharkhttp

import (
	"context"
	"fmt"

	"github.com/gin-gonic/gin"
	swaggerfiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"go.uber.org/zap"
)

// New 创建并启动一个 Gin HTTP 服务。
//
// 配置说明:
//   - 禁用 Gin 控制台颜色输出（生产环境不需要）
//   - 设置为 ReleaseMode（关闭调试信息，提升性能）
//   - 依次注册三个内置中间件：panic 恢复 → CORS → 错误处理
//   - 开发环境（env == "dev"）自动注册 Swagger 路由
//   - 在后台 goroutine 中启动 HTTP 监听
//
// 使用示例:
//
//	// 创建 HTTP 服务（通常在 sharkapp.New 中自动调用）
//	router := sharkhttp.New(ctx, "dev", logger, 8080)
//
//	// 注册业务路由
//	router.GET("/api/users", func(c *gin.Context) {
//	    c.JSON(200, gin.H{"users": []string{"Alice", "Bob"}})
//	})
//
//	// 开发环境可访问 Swagger: http://localhost:8080/swagger/index.html
//
// 参数:
//   - ctx: 上下文（当前未使用，保留用于未来扩展）
//   - evn: 运行环境，"dev" 时启用 Swagger
//   - logger: zap 日志记录器，用于 panic 恢复中间件
//   - port: HTTP 监听端口
//
// 返回值:
//   - *gin.Engine: 已配置中间件和路由的 Gin 引擎实例
func New(ctx context.Context, evn string, logger *zap.Logger, port int) *gin.Engine {
	// 禁用控制台颜色，设置为生产模式
	gin.DisableConsoleColor()
	gin.SetMode(gin.ReleaseMode)

	// 创建不带默认中间件的 Gin 引擎（自定义中间件更可控）
	router := gin.New()

	// 注册内置中间件（按顺序执行）
	router.Use(recoveryMiddleware(logger)) // 1. panic 恢复
	router.Use(corsMiddleware())           // 2. CORS 跨域
	router.Use(errorMiddleware())          // 3. 统一错误处理

	// 开发环境注册 Swagger 文档路由
	// 访问地址: http://localhost:PORT/swagger/index.html
	if evn == "dev" {
		router.GET("swagger/*any", ginSwagger.WrapHandler(swaggerfiles.Handler))
	}

	// 异步启动 HTTP 服务（不阻塞当前 goroutine）
	go func() {
		if err := router.Run(":" + fmt.Sprint(port)); err != nil {
			logger.Error("http服务启动失败", zap.Error(err), zap.Int("port", port))
		}
	}()
	return router
}
