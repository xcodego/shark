package test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/xcodego/shark/sharkerror"
)

func TestErrorNew(t *testing.T) {
	e := sharkerror.New(10001, "用户不存在")
	if e.Code != 10001 {
		t.Errorf("Code = %d, want 10001", e.Code)
	}
	if e.Msg != "用户不存在" {
		t.Errorf("Msg = %s, want 用户不存在", e.Msg)
	}
	if e.Data != nil {
		t.Errorf("Data should be nil, got %v", e.Data)
	}
}

func TestErrorError(t *testing.T) {
	e := sharkerror.New(10001, "用户不存在")
	if e.Error() != "code=10001 msg=用户不存在" {
		t.Errorf("Error() = %s", e.Error())
	}
}

func TestErrorIs(t *testing.T) {
	e1 := sharkerror.New(10001, "用户不存在")
	e2 := sharkerror.New(10001, "用户已删除")
	if !errors.Is(e1, e2) {
		t.Error("相同 Code 的 Error 应该 Is 为 true")
	}
	e3 := sharkerror.New(20001, "订单已过期")
	if errors.Is(e1, e3) {
		t.Error("不同 Code 的 Error 应该 Is 为 false")
	}
}

func TestErrorWithData(t *testing.T) {
	e := sharkerror.New(10001, "用户不存在")
	e2 := e.WithData(map[string]any{"user_id": 123})
	if e2.Code != 10001 {
		t.Errorf("Code = %d", e2.Code)
	}
	data, ok := e2.Data.(map[string]any)
	if !ok || data["user_id"] != 123 {
		t.Errorf("Data = %v", e2.Data)
	}
	// 原实例不应被修改
	if e.Data != nil {
		t.Error("原实例不应被修改")
	}
}

func TestErrorWithErr(t *testing.T) {
	orig := errors.New("connection timeout")
	base := sharkerror.New(20001, "数据库错误").WithData("keep")
	e := base.WithErr(orig)
	if e.Data != "keep" {
		t.Errorf("WithErr 不应改 Data, got %v", e.Data)
	}
	if !errors.Is(e, orig) {
		t.Error("WithErr 应能 errors.Is 到底层错误")
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "connection timeout") {
		t.Errorf("JSON 不应包含底层错误: %s", b)
	}
}

func TestErrorWithErrNil(t *testing.T) {
	base := sharkerror.New(20001, "数据库错误").WithData("keep")
	got := base.WithErr(nil)
	if got != base {
		t.Error("WithErr(nil) 应返回原实例")
	}
	if got.Data != "keep" {
		t.Errorf("WithErr(nil) 不应改 Data, got %v", got.Data)
	}
}

func TestErrorWithMsg(t *testing.T) {
	e := sharkerror.New(10001, "用户不存在").WithData(map[string]any{"id": 1})
	e2 := e.WithMsg("用户名不能为空")
	if e2.Msg != "用户名不能为空" {
		t.Errorf("Msg = %s", e2.Msg)
	}
	if e2.Data == nil {
		t.Error("WithMsg 应该保留 Data")
	}
}

func TestErrorIsNil(t *testing.T) {
	e := sharkerror.New(10001, "test")
	if errors.Is(e, nil) {
		t.Error("should not Is nil")
	}
}

func TestErrorWithErrWrapUnwrap(t *testing.T) {
	orig := &fakeMySQLError{Number: 1062, Message: "Duplicate entry"}
	base := sharkerror.New(20001, "数据库错误")
	e := base.WithData(map[string]any{"table": "users"}).WithErrWrap(orig)

	if e.Data == nil {
		t.Fatal("WithErrWrap 不应覆盖 Data")
	}
	if !errors.Is(e, base) {
		t.Error("errors.Is 应按 Code 命中业务错误")
	}
	var got *fakeMySQLError
	if !errors.As(e, &got) {
		t.Fatal("errors.As 应穿透到底层错误")
	}
	if got.Number != 1062 {
		t.Errorf("Number = %d", got.Number)
	}
	e2 := e.WithMsg("重复键")
	if !errors.As(e2, &got) {
		t.Error("WithMsg 应保留 cause")
	}
}

type fakeMySQLError struct {
	Number  uint16
	Message string
}

func (e *fakeMySQLError) Error() string { return e.Message }

func TestErrorWithErrWrapJSONOmitsCause(t *testing.T) {
	e := sharkerror.New(20001, "数据库错误").WithErrWrap(errors.New("secret"))
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "secret") {
		t.Errorf("JSON 不应包含底层错误: %s", b)
	}
}
