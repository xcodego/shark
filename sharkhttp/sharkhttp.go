// Package sharkhttp 提供基于 Gin 的 HTTP 服务初始化，内置 recovery、CORS、错误处理三个中间件。
package sharkhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	swaggerfiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"go.uber.org/zap"
)

// Server 是已启动的 HTTP 服务，持有 Gin 引擎和底层 http.Server，可优雅关闭。
type Server struct {
	// Engine 是 Gin 引擎，供业务注册路由。
	Engine *gin.Engine
	srv    *http.Server
}

// NewServer 创建 Gin 引擎并异步监听 port，按序挂载 recovery、CORS、错误处理中间件。
// evn 为 "dev" 时注册 Swagger 路由；监听失败只记录日志。
// 返回的 Server 可调用 Shutdown 关闭。ctx 当前未使用。
func NewServer(ctx context.Context, evn string, logger *zap.Logger, port int) *Server {
	gin.DisableConsoleColor()
	gin.SetMode(gin.ReleaseMode)

	router := gin.New()

	router.Use(recoveryMiddleware(logger))
	router.Use(corsMiddleware())
	router.Use(errorMiddleware(logger))

	if evn == "dev" {
		router.GET("swagger/*any", ginSwagger.WrapHandler(swaggerfiles.Handler))
	}

	s := &Server{
		Engine: router,
		srv:    &http.Server{Addr: ":" + fmt.Sprint(port), Handler: router},
	}
	go func() {
		if err := s.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			if logger != nil {
				logger.Error("http服务启动失败", zap.Error(err), zap.Int("port", port))
			}
		}
	}()
	return s
}

// New 与 NewServer 相同，但只返回 Gin 引擎，无法关闭服务。需要关闭时请使用 NewServer。
func New(ctx context.Context, evn string, logger *zap.Logger, port int) *gin.Engine {
	return NewServer(ctx, evn, logger, port).Engine
}

// Shutdown 优雅关闭 HTTP 服务：停止接收新连接，等待在途请求处理完，ctx 超时则强制关闭剩余连接。
// 已被 Hijack 的连接（如 WebSocket）不在管理范围内，需另行关闭。
// 返回 ctx 超时错误时，连接已被强制关闭。可重复调用。
func (s *Server) Shutdown(ctx context.Context) error {
	err := s.srv.Shutdown(ctx)
	if err != nil {
		s.srv.Close()
	}
	return err
}
