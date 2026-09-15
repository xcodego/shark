package sharkhttp

import (
	"errors"
	"net/http"
	"runtime/debug"

	"github.com/lornshark/shark/sharkerror"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// WsUpgrader 是 WebSocket 连接升级器，用于将 HTTP 连接升级为 WebSocket。
// CheckOrigin 返回 true 表示允许所有来源的 WebSocket 连接。
// 如果需要限制来源，可以修改 CheckOrigin 的实现。
var WsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // 允许所有来源
	},
}

// corsMiddleware 是 CORS（跨域资源共享）中间件。
//
// 允许的配置:
//   - Origin: *（所有域名）
//   - Headers: Content-Type, x-token, Content-Length, X-Requested-With
//   - Methods: GET, POST
//   - Max-Age: 7200 秒（预检请求缓存 2 小时）
//
// 对于 OPTIONS 预检请求，直接返回 204 No Content。
//
// 使用示例:
//
//	// 通常由 sharkhttp.New 自动注册，也可以手动注册
//	router := gin.New()
//	router.Use(sharkhttp.CorsMiddleware())
//
// 返回:
//   - gin.HandlerFunc: CORS 中间件处理函数
func corsMiddleware() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		method := ctx.Request.Method

		// 设置 CORS 响应头
		ctx.Header("Access-Control-Allow-Origin", "*")
		ctx.Header("Access-Control-Allow-Headers", "Content-Type, x-token, Content-Length, X-Requested-With")
		ctx.Header("Access-Control-Allow-Methods", "GET,POST")
		ctx.Header("Access-Control-Expose-Headers", "Content-Length, Access-Control-Allow-Origin, Access-Control-Allow-Headers, Content-Type")
		ctx.Header("Access-Control-Max-Age", "7200")

		// OPTIONS 预检请求直接返回，不继续处理
		if method == "OPTIONS" {
			ctx.AbortWithStatus(http.StatusNoContent)
		}
		ctx.Next()
	}
}

// recoveryMiddleware 是 panic 恢复中间件。
//
// 功能:
//   - 捕获处理请求过程中发生的 panic，防止整个进程崩溃
//   - 记录 panic 信息、调用栈、请求路径和请求体到日志
//   - 返回 HTTP 500 状态码和 panic 内容
//
// 注意事项:
//   - 会在中间件执行前读取请求体（GetRawData 会消耗 Body），
//     读取后重新放回 Body 以便后续中间件/Handler 也能读取
//
// 参数:
//   - logger: zap 日志记录器，为 nil 时不记录日志
//
// 使用示例:
//
//	// 通常由 sharkhttp.New 自动注册，也可以手动注册
//	router := gin.New()
//	router.Use(sharkhttp.RecoveryMiddleware(logger))
//
// 返回:
//   - gin.HandlerFunc: panic 恢复中间件处理函数
func recoveryMiddleware(logger *zap.Logger) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		reqPath := ctx.Request.URL.Path

		defer func() {
			if r := recover(); r != nil {
				// 仅在 panic 时读取请求体（避免正常请求的额外开销）
				reqData, _ := ctx.GetRawData()
				// 记录详细的 panic 信息
				if logger != nil {
					logger.Error("panic",
						zap.Any("error", r),
						zap.String("stack", string(debug.Stack())),
						zap.String("path", reqPath),
						zap.ByteString("data", reqData),
					)
				}
				// 返回 500 状态码和 panic 信息
				ctx.JSON(http.StatusInternalServerError, map[string]any{"data": r})
				ctx.Abort()
			}
		}()
		ctx.Next()
	}
}

// errorMiddleware 是统一错误处理中间件。
//
// 在请求处理完成后（c.Next() 之后）检查是否有错误：
//   - 如果错误是 *sharkerror.Error 类型，则以 HTTP 200 + JSON 格式返回业务错误
//   - 如果是其他类型的错误，包装为 sharkerror.Error{Code: 1, Msg: "未知错误"} 返回
//
// 设计理念:
//
//	业务错误不应该使用 HTTP 错误状态码，而应该通过 JSON body 中的 code 字段区分。
//	这样前端可以统一处理响应，不需要解析 HTTP 状态码。
//
// 使用示例:
//
//	// 通常由 sharkhttp.New 自动注册，也可以手动注册
//	router := gin.New()
//	router.Use(sharkhttp.ErrorMiddleware())
//
//	// 在 Handler 中使用
//	router.GET("/api/user/:id", func(c *gin.Context) {
//	    user, err := findUser(c.Param("id"))
//	    if err != nil {
//	        c.Error(sharkerror.New(404, "用户不存在"))
//	        return
//	    }
//	    c.JSON(200, user)
//	})
//	// → 响应: {"code": 404, "msg": "用户不存在", "data": null}
//
// 返回:
//   - gin.HandlerFunc: 错误处理中间件
func errorMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		// 没有错误则直接返回
		if len(c.Errors) == 0 {
			return
		}

		// 获取最后一个错误
		var err error = c.Errors.Last().Err

		// 判断是否为业务错误（*sharkerror.Error）
		var e *sharkerror.Error
		if errors.As(err, &e) {
			// 业务错误：200 + JSON
			c.JSON(http.StatusOK, e)
			return
		}

		// 未知错误：包装为通用错误格式
		c.JSON(http.StatusOK, &sharkerror.Error{
			Code: 1,
			Msg:  "未知错误",
			Data: err.Error(),
		})
	}
}
