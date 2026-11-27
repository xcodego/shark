// Package sharkhttp 提供基于 Gin 的 HTTP 服务初始化，内置 recovery、CORS、错误处理三个中间件。
package sharkhttp

import (
	"context"
	"fmt"

	"github.com/gin-gonic/gin"
	swaggerfiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"go.uber.org/zap"
)

// New 创建 Gin 引擎并异步监听 port，按序挂载 recovery、CORS、错误处理中间件。
// evn 为 "dev" 时注册 Swagger 路由；监听失败只记录日志。
func New(ctx context.Context, evn string, logger *zap.Logger, port int) *gin.Engine {
	gin.DisableConsoleColor()
	gin.SetMode(gin.ReleaseMode)

	router := gin.New()

	router.Use(recoveryMiddleware(logger))
	router.Use(corsMiddleware())
	router.Use(errorMiddleware(logger))

	if evn == "dev" {
		router.GET("swagger/*any", ginSwagger.WrapHandler(swaggerfiles.Handler))
	}

	go func() {
		if err := router.Run(":" + fmt.Sprint(port)); err != nil {
			if logger != nil {
				logger.Error("http服务启动失败", zap.Error(err), zap.Int("port", port))
			}
		}
	}()
	return router
}
