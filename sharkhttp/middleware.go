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
// 由 sharkhttp.New 自动注册，未导出。
//
// 使用示例（业务只需走 sharkhttp.New，不必自己挂中间件）:
//
//	router := sharkhttp.New(ctx, "dev", logger, 8080)
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

		// OPTIONS 预检请求直接返回，不继续进入后续中间件和业务 handler
		if method == "OPTIONS" {
			ctx.AbortWithStatus(http.StatusNoContent)
			return
		}
		ctx.Next()
	}
}

// recoveryMiddleware 是 panic 恢复中间件。
//
// 功能:
//   - 捕获处理请求过程中发生的 panic，防止整个进程崩溃
//   - 记录 panic 信息、调用栈、请求路径和请求体到日志
//   - 对客户端返回 HTTP 500 和固定文案，不回传 panic 内容
//
// 参数:
//   - logger: zap 日志记录器，为 nil 时不记录日志
//
// 由 sharkhttp.New 自动注册，未导出。
func recoveryMiddleware(logger *zap.Logger) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		reqPath := ctx.Request.URL.Path
		// handler 会消耗 Body；必须先拷贝再塞回去，panic 时才能记到原始请求体。
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

// errorMiddleware 是统一错误处理中间件。
//
// 在请求处理完成后（c.Next() 之后）检查是否有错误：
//   - 如果错误是 *sharkerror.Error 类型，则以 HTTP 200 + JSON 返回 code/msg/Data（WithData）；
//     WithErr / WithErrWrap 的底层错误只打日志，不进响应
//   - 如果是其他类型的错误，打日志，对客户端返回固定的 sharkerror.Error{Code: 1, Msg: "未知错误"}，不回传底层错误文本
//
// 设计理念:
//
//	业务错误不应该使用 HTTP 错误状态码，而应该通过 JSON body 中的 code 字段区分。
//	这样前端可以统一处理响应，不需要解析 HTTP 状态码。
//
// 由 sharkhttp.New 自动注册，未导出。
//
// 使用示例:
//
//	router := sharkhttp.New(ctx, "dev", logger, 8080)
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
