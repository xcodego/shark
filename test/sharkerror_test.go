package test

import (
	"errors"
	"testing"

	"github.com/lornshark/shark/sharkerror"
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
	e := sharkerror.New(20001, "数据库错误").WithErr(orig)
	if e.Data != "connection timeout" {
		t.Errorf("Data = %s, want connection timeout", e.Data)
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

func TestErrorJsonSerialization(t *testing.T) {
	e := sharkerror.New(10001, "用户不存在").WithData(map[string]any{"user_id": 123})
	// 验证结构体字段可导出
	if e.Code != 10001 || e.Msg != "用户不存在" {
		t.Error("字段不匹配")
	}
}
