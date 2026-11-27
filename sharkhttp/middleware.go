package sharkhttp

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"runtime/debug"

	"github.com/xcodego/shark/sharkerror"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// WsUpgrader 是 WebSocket 升级器，不校验 Origin（允许任意跨域来源）。
var WsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// corsMiddleware 返回 CORS 中间件：允许任意 Origin，仅放行 GET/POST，OPTIONS 预检直接返回 204。
func corsMiddleware() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		method := ctx.Request.Method

		ctx.Header("Access-Control-Allow-Origin", "*")
		ctx.Header("Access-Control-Allow-Headers", "Content-Type, x-token, Content-Length, X-Requested-With")
		ctx.Header("Access-Control-Allow-Methods", "GET,POST")
		ctx.Header("Access-Control-Expose-Headers", "Content-Length, Access-Control-Allow-Origin, Access-Control-Allow-Headers, Content-Type")
		ctx.Header("Access-Control-Max-Age", "7200")

		if method == "OPTIONS" {
			ctx.AbortWithStatus(http.StatusNoContent)
			return
		}
		ctx.Next()
	}
}

// recoveryMiddleware 返回 panic 恢复中间件：记录错误、调用栈、路径和请求体，并向客户端返回固定的内部错误。
func recoveryMiddleware(logger *zap.Logger) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		reqPath := ctx.Request.URL.Path
		// 先读出请求体用于 panic 日志，再还原，保证后续处理仍可读取。
		reqData, _ := ctx.GetRawData()
		ctx.Request.Body = io.NopCloser(bytes.NewReader(reqData))

		defer func() {
			if r := recover(); r != nil {
				if logger != nil {
					logger.Error("panic",
						zap.Any("error", r),
						zap.String("stack", string(debug.Stack())),
						zap.String("path", reqPath),
						zap.ByteString("data", reqData),
					)
				}
				ctx.JSON(http.StatusInternalServerError, &sharkerror.Error{
					Code: 1,
					Msg:  "内部错误",
				})
				ctx.Abort()
			}
		}()
		ctx.Next()
	}
}

// errorMiddleware 返回统一错误处理中间件，处理最后一个 c.Errors：
// 业务错误(*sharkerror.Error)返回 code/msg/data，底层错误只打日志不进响应；
// 未知错误打日志，对客户端只返回固定文案。响应状态码均为 200。
func errorMiddleware(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		if len(c.Errors) == 0 {
			return
		}

		var err error = c.Errors.Last().Err

		var e *sharkerror.Error
		if errors.As(err, &e) {
			if logger != nil && e.Unwrap() != nil {
				logger.Error("http business error",
					zap.Error(e.Unwrap()),
					zap.Int("code", e.Code),
					zap.String("path", c.Request.URL.Path),
					zap.String("method", c.Request.Method),
				)
			}
			c.JSON(http.StatusOK, &sharkerror.Error{
				Code: e.Code,
				Msg:  e.Msg,
				Data: e.Data,
			})
			return
		}

		if logger != nil {
			logger.Error("http unknown error",
				zap.Error(err),
				zap.String("path", c.Request.URL.Path),
				zap.String("method", c.Request.Method),
			)
		}
		c.JSON(http.StatusOK, &sharkerror.Error{
			Code: 1,
			Msg:  "未知错误",
		})
	}
}
