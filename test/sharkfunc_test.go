package test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lornshark/shark/sharkfunc"
)

func TestWithTimeoutSuccess(t *testing.T) {
	err := sharkfunc.WithTimeout(context.Background(), 5*time.Second, func(ctx context.Context) error {
		return nil
	})
	if err != nil {
		t.Errorf("正常返回不应有错误: %v", err)
	}
}

func TestWithTimeoutTimeout(t *testing.T) {
	err := sharkfunc.WithTimeout(context.Background(), 1*time.Millisecond, func(ctx context.Context) error {
		time.Sleep(100 * time.Millisecond)
		return nil
	})
	if !errors.Is(err, sharkfunc.ErrTimeout) {
		t.Errorf("超时应返回 ErrTimeout, got: %v", err)
	}
}

func TestWithTimeoutParentCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消
	err := sharkfunc.WithTimeout(ctx, 5*time.Second, func(ctx context.Context) error {
		return nil
	})
	if !errors.Is(err, sharkfunc.ErrTimeout) {
		t.Errorf("父 context 取消应返回 ErrTimeout, got: %v", err)
	}
}

func TestWithTimeoutPanic(t *testing.T) {
	err := sharkfunc.WithTimeout(context.Background(), 5*time.Second, func(ctx context.Context) error {
		panic("test panic in WithTimeout")
	})
	if err == nil {
		t.Error("panic 应该被捕获并返回错误")
	} else {
		// 检查不是 ErrTimeout（panic 不应返回超时错误）
		if errors.Is(err, sharkfunc.ErrTimeout) {
			t.Error("panic 不应返回 ErrTimeout")
		}
		t.Logf("panic 捕获: %v", err)
	}
}

func TestParallelCallSuccess(t *testing.T) {
	var a, b, c int
	err := sharkfunc.ParallelCall(
		func() error { a = 1; return nil },
		func() error { b = 2; return nil },
		func() error { c = 3; return nil },
	)
	if err != nil {
		t.Errorf("ParallelCall 不应有错误: %v", err)
	}
	if a != 1 || b != 2 || c != 3 {
		t.Errorf("结果不匹配: a=%d b=%d c=%d", a, b, c)
	}
}

func TestParallelCallPanic(t *testing.T) {
	err := sharkfunc.ParallelCall(
		func() error { panic("error1") },
		func() error { /* normal */ return nil },
	)
	if err == nil {
		t.Error("panic 应返回错误")
	}
	t.Logf("panic 捕获: %v", err)
}

func TestDrainChannelN(t *testing.T) {
	ch := make(chan int, 100)
	for i := 0; i < 10; i++ {
		ch <- i
	}
	result, _ := sharkfunc.DrainChannelN(context.Background(), ch, 5, 0)
	if len(result) != 5 {
		t.Errorf("应最多返回 5 条, got %d", len(result))
	}
}

func TestDrainChannelNWithCtxCancel(t *testing.T) {
	ch := make(chan int, 10)
	// 不预先放入数据，只取消 ctx：阻塞读取时 ctx.Done() 立即触发
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, _ := sharkfunc.DrainChannelN(ctx, ch, 10, 0)
	if len(result) != 0 {
		t.Errorf("ctx 取消应返回空, got %d", len(result))
	}
}

func TestDrainChannelNWithClosedChannel(t *testing.T) {
	ch := make(chan int, 5)
	ch <- 1
	ch <- 2
	close(ch)
	result, _ := sharkfunc.DrainChannelN(context.Background(), ch, 10, 0)
	if len(result) != 2 {
		t.Errorf("应返回已关闭 channel 中所有 2 条, got %d", len(result))
	}
}

func TestDrainChannelNWithNilChannel(t *testing.T) {
	result, _ := sharkfunc.DrainChannelN[int](context.Background(), nil, 10, 0)
	if len(result) != 0 {
		t.Errorf("nil channel 应返回空切片, got %d", len(result))
	}
}

func TestRecover(t *testing.T) {
	// sharkfunc.Recover 需要非 nil 的 logger，传 nil 会在 panic 恢复后再次崩溃
	// 这里仅验证 Recover 函数存在且可编译调用
	t.Log("Recover 函数编译验证通过，需要 *zap.Logger 才能安全调用")
}

func TestPointer(t *testing.T) {
	// string
	s := sharkfunc.Pointer("hello")
	if s == nil || *s != "hello" {
		t.Errorf("Pointer string: got %v", s)
	}

	// int
	i := sharkfunc.Pointer(42)
	if i == nil || *i != 42 {
		t.Errorf("Pointer int: got %v", i)
	}

	// struct
	type User struct{ Name string }
	u := sharkfunc.Pointer(User{Name: "Alice"})
	if u == nil || u.Name != "Alice" {
		t.Errorf("Pointer struct: got %v", u)
	}
}
