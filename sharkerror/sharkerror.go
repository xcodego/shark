// Package sharkerror 提供了统一的业务错误类型。
//
// Error 是一个轻量级的错误结构体，包含错误码、错误消息和可选的附加数据，
// 主要设计目标:
//   - 统一 API 错误格式（code + msg + data），方便前端统一处理
//   - 支持 errors.Is 判断（基于 Code 比较），适用于错误分级处理
//   - 链式调用 WithXxx 方法追加附加信息，不修改原实例（不可变性）
//
// JSON 序列化后格式:
//
//	{
//	  "code": 10001,
//	  "msg": "用户不存在",
//	  "data": null
//	}
//
// 使用示例:
//
//	var ErrUserNotFound = sharkerror.New(10001, "用户不存在")
//
//	// 判断错误类型
//	if errors.Is(err, ErrUserNotFound) {
//	    // 处理用户不存在
//	}
//
//	// 附加数据
//	err := ErrUserNotFound.WithData(map[string]any{"user_id": 123})
//	// 附加原始错误
//	err := someFunc() // 返回 *Error
//	err = err.WithErr(originalErr)
package sharkerror

import (
	"strconv"
)

// Error 是 Shark 框架的统一业务错误类型。
//
// 字段说明:
//   - Code: 业务错误码，用于前端或调用方判断错误类型
//   - Msg: 错误描述信息，面向用户或开发者的可读文本
//   - Data: 附加数据，可存放任意类型的上下文信息（如请求参数、调试信息等）
type Error struct {
	Code int    `json:"code"` // 业务错误码
	Msg  string `json:"msg"`  // 错误描述
	Data any    `json:"data"` // 附加数据
}

// Error 实现 error 接口，返回 "code=xxx msg=xxx" 格式的错误字符串。
func (e *Error) Error() string {
	return "code=" + strconv.Itoa(e.Code) + " msg=" + e.Msg
}

// Is 实现 errors.Is 比较接口。
//
// 基于 Code 判断两个 Error 是否相等，而非基于指针或消息内容。
// 这使得预先定义的错误变量（如 ErrUserNotFound）可以作为目标
// 与运行时生成（附加了 Data/Err 等）的错误实例进行匹配。
//
// 参数:
//   - target: 要比较的目标错误
//
// 返回值:
//   - 两个 Error 的 Code 相同时返回 true
func (e *Error) Is(target error) bool {
	if target == nil {
		return false
	}
	t, ok := target.(*Error)
	return ok && e.Code == t.Code
}

// WithData 创建新的 Error 实例并附加自定义数据。
//
// 原 Error 不会被修改（不可变性设计），返回的是全新实例。
//
// 参数:
//   - data: 任意类型的附加数据，会序列化为 JSON
//
// 返回:
//   - 包含新 Data 的 Error 副本
//
// 示例:
//
//	err := ErrUserNotFound.WithData(map[string]any{"user_id": 123})
//	// Code=10001, Msg="用户不存在", Data={"user_id": 123}
func (e *Error) WithData(data any) *Error {
	return &Error{
		Code: e.Code,
		Msg:  e.Msg,
		Data: data,
	}
}

// WithErr 创建新的 Error 实例并将原始错误的文本存入 Data 字段。
//
// 适用于包装底层错误（如数据库查询错误）并透传给上层。
//
// 参数:
//   - err: 原始错误，其 Error() 文本会被存入 Data
//
// 返回:
//   - Data 为原始错误文本的 Error 副本
//
// 示例:
//
//	dbErr := errors.New("connection timeout")
//	bizErr := ErrDBError.WithErr(dbErr)
//	// Code=20001, Msg="数据库错误", Data="connection timeout"
func (e *Error) WithErr(err error) *Error {
	return &Error{
		Code: e.Code,
		Msg:  e.Msg,
		Data: err.Error(),
	}
}

// WithErrWrap 创建新的 Error 实例，保留原始错误类型用于 errors.Is/As 穿透。
//
// 与 WithErr 的区别：WithErrWrap 将原始 error 作为 Data 存储，
// 上游可通过 errors.Is/As 继续判断底层错误类型。
//
// 参数:
//   - err: 原始错误，完整的 error 实例存入 Data（JSON 序列化时自动调用 Error()）
//
// 返回:
//   - Data 为原始 error 的错误副本，errors.Is/As 可穿透
//
// 示例:
//
//	dbErr := &mysql.MySQLError{Number: 1062, Message: "Duplicate entry"}
//	bizErr := ErrDBError.WithErrWrap(dbErr)
//	// errors.Is(bizErr, ErrDBError) → true
//	// var mysqlErr *mysql.MySQLError; errors.As(bizErr, &mysqlErr) → true
func (e *Error) WithErrWrap(err error) *Error {
	return &Error{
		Code: e.Code,
		Msg:  e.Msg,
		Data: err,
	}
}

// WithMsg 创建新的 Error 实例并替换消息内容。
//
// 用于在保留错误码和附加数据的同时，自定义错误消息。
//
// 参数:
//   - msg: 新的错误消息
//
// 返回:
//   - Msg 更新后的 Error 副本
//
// 示例:
//
//	err := ErrParamInvalid.WithMsg("用户名不能为空")
func (e *Error) WithMsg(msg string) *Error {
	return &Error{
		Code: e.Code,
		Msg:  msg,
		Data: e.Data,
	}
}

// New 创建一个新的业务错误。
//
// 参数:
//   - code: 业务错误码（建议按模块分段，如 10xxx 用户模块, 20xxx 订单模块）
//   - msg: 错误描述信息
//
// 返回:
//   - 初始化好的 Error 实例
//
// 示例:
//
//	var ErrUserNotFound = sharkerror.New(10001, "用户不存在")
//	var ErrOrderExpired = sharkerror.New(20001, "订单已过期")
func New(code int, msg string) *Error {
	return &Error{
		Code: code,
		Msg:  msg,
	}
}
