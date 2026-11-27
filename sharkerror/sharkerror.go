// Package sharkerror 提供统一的业务错误类型。
package sharkerror

import (
	"strconv"
)

// Error 是包含错误码、消息和附加数据的业务错误。
type Error struct {
	Code  int    `json:"code"` // 业务错误码
	Msg   string `json:"msg"`  // 错误描述
	Data  any    `json:"data"` // 附加数据
	cause error  `json:"-"`    // 底层错误，不序列化
}

// clone 复制错误，使 WithXxx 方法不修改原实例（可安全用作全局预定义错误）。
func (e *Error) clone() *Error {
	return &Error{
		Code:  e.Code,
		Msg:   e.Msg,
		Data:  e.Data,
		cause: e.cause,
	}
}

// Unwrap 返回关联的底层错误。
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Error 返回包含错误码和消息的错误字符串。
func (e *Error) Error() string {
	return "code=" + strconv.Itoa(e.Code) + " msg=" + e.Msg
}

// Is 实现 errors.Is 的匹配：target 为 *Error 且错误码相同时视为同一错误。
func (e *Error) Is(target error) bool {
	if target == nil {
		return false
	}
	t, ok := target.(*Error)
	return ok && e.Code == t.Code
}

// WithData 返回附加指定数据的新错误实例。
func (e *Error) WithData(data any) *Error {
	n := e.clone()
	n.Data = data
	return n
}

// WithErr 返回关联底层错误的新实例；err 为 nil 时返回原实例。
func (e *Error) WithErr(err error) *Error {
	if err == nil {
		return e
	}
	n := e.clone()
	n.cause = err
	return n
}

// WithErrWrap 返回关联底层错误的新实例，兼容旧调用。
// 与 WithErr 不同，err 为 nil 时也返回新实例（底层错误被清空）。
func (e *Error) WithErrWrap(err error) *Error {
	n := e.clone()
	n.cause = err
	return n
}

// WithMsg 返回替换消息后的新错误实例。
func (e *Error) WithMsg(msg string) *Error {
	n := e.clone()
	n.Msg = msg
	return n
}

// New 创建带错误码和消息的业务错误。
func New(code int, msg string) *Error {
	return &Error{
		Code: code,
		Msg:  msg,
	}
}
