// Package sharkfunc 提供了一些通用的函数式编程工具函数，
// 包括超时控制、并行执行、channel 批量读取、panic 恢复以及泛型指针转换等功能。
// 这些工具函数旨在简化常见的并发和错误处理模式，减少样板代码。
package sharkfunc

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// ErrTimeout 是 WithTimeout 函数在指定时间内未完成调用时返回的超时错误。
// 调用方可以通过 errors.Is(err, sharkfunc.ErrTimeout) 来判断是否为超时错误。
var ErrTimeout = errors.New("sharkfunc: timeout")

// WithTimeout 在指定的超时时间内执行函数 fn，如果超时则返回 ErrTimeout。
//
// 参数:
//   - parent: 父级 context，用于传递取消信号和超时控制。WithTimeout 会在其基础上
//     创建一个带有超时的子 context 并传入 fn。如果 parent 被取消，fn 也会随之终止。
//   - timeout: 最大等待时间。如果在此时长内 fn 未返回，则返回 ErrTimeout。
//   - fn: 需要执行的函数，接收一个带超时的 context。fn 应当定期检查 ctx.Done()
//     以便在超时或取消时及时退出。
//
// 返回值:
//   - 如果 fn 在超时前正常完成，返回 fn 的错误（可能为 nil）。
//   - 如果超时或 parent 被取消，返回 ErrTimeout。
//   - 如果 fn 内部发生 panic，返回包含 panic 信息和调用栈的错误，不会导致程序崩溃。
//
// 使用示例:
//
//	err := sharkfunc.WithTimeout(ctx, 5*time.Second, func(ctx context.Context) error {
//	    // 执行耗时操作，定期检查 ctx.Done()
//	    return doSomething(ctx)
//	})
//	if errors.Is(err, sharkfunc.ErrTimeout) {
//	    // 处理超时
//	}
func WithTimeout(parent context.Context, timeout time.Duration, fn func(context.Context) error) error {
	// 基于 parent 创建一个带超时时间的子 context
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	// 使用带缓冲的 channel 接收结果，避免 goroutine 泄漏
	// 缓冲大小为 1，确保 goroutine 在没人接收时也能正常退出
	ch := make(chan error, 1)

	// 在新的 goroutine 中执行 fn
	go func() {
		var err error
		defer func() {
			// 捕获 fn 中可能发生的 panic，防止整个程序崩溃
			if r := recover(); r != nil {
				// 使用 select+default 的非阻塞发送，
				// 如果 ch 已满（接收方已超时离开），则丢弃 panic 信息避免 goroutine 阻塞
				select {
				case ch <- fmt.Errorf("panic: %v\n%s", r, debug.Stack()):
				default:
				}
				return
			}
			// 正常完成时发送结果，同样使用非阻塞方式
			select {
			case ch <- err:
			default:
			}
		}()
		// 实际执行用户传入的函数
		err = fn(ctx)
	}()

	// 等待 fn 完成或超时
	select {
	case err := <-ch:
		// fn 在超时前完成，直接返回其结果
		return err
	case <-ctx.Done():
		// 超时或 parent context 被取消
		// 再尝试一次非阻塞读取，以防 fn 恰好在此时完成
		// （避免将实际结果误判为超时）
		select {
		case err := <-ch:
			return err
		default:
			return ErrTimeout
		}
	}
}

// ParallelCall 并行执行多个无参函数，等待所有函数执行完毕后返回。
//
// 参数:
//   - funcs: 需要并行执行的无参函数列表。
//
// 返回值:
//   - 如果所有函数都正常完成（无 panic），返回 nil。
//   - 如果任意一个函数发生 panic，返回包含 panic 信息的 error。
//     注意：即使有 panic，也会等待所有函数执行完成后再返回错误。
//
// 注意事项:
//   - 函数之间是独立的，没有顺序保证。
//   - 每个函数都在独立的 goroutine 中运行。
//   - panic 不会导致程序崩溃，会被捕获并转换为 error 返回。
//
// 使用示例:
//
//	err := sharkfunc.ParallelCall(
//	    func() { loadUsers() },
//	    func() { loadOrders() },
//	    func() { loadProducts() },
//	)
//	if err != nil {
//	    // 处理 panic 错误
//	}
func ParallelCall(funcs ...func() error) error {
	var wg sync.WaitGroup

	// 使用带缓冲的 channel 收集各 goroutine 的 panic 错误
	// 缓冲大小等于函数数量，确保所有 goroutine 都能无阻塞地发送
	ch := make(chan error, len(funcs))

	for _, fn := range funcs {
		if fn == nil {
			continue
		}
		wg.Add(1)
		// 通过闭包参数传递 fn，避免循环变量捕获问题
		go func(f func() error) {
			defer wg.Done()
			defer func() {
				// 捕获 panic 并转换为 error 发送到 channel
				if r := recover(); r != nil {
					ch <- fmt.Errorf("panic: %v\n%s", r, debug.Stack())
				}
			}()
			err := f()
			if err != nil {
				ch <- err
			}
		}(fn)
	}

	// 等待所有 goroutine 完成
	wg.Wait()
	// 关闭 channel，使后续的 range 循环能够正常结束
	close(ch)
	errs := make([]error, 0, len(funcs))
	for err := range ch {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// DrainChannelN 从 channel 中批量读取最多 size 条数据。
//
// 该函数采用"先阻塞取一条，再非阻塞尽量多取"的策略，既能保证至少拿到一条数据
// （在有数据的情况下），又能在没有更多数据时立即返回，避免无限阻塞。
//
// 类型参数:
//   - T: channel 中元素的类型，支持任意类型。
//
// 参数:
//   - ctx: 用于超时和取消控制。当 ctx 被取消时，函数立即返回已读取的数据。
//   - ch: 只读 channel，从中读取数据。如果为 nil，直接返回空切片。
//   - size: 期望读取的最大条数。如果 <= 0，直接返回空切片。
//
// 返回值:
//   - 读取到的数据切片，长度不超过 size。以下情况会提前返回：
//   - ctx 被取消（Done）
//   - channel 被关闭
//   - channel 中没有更多立即可用的数据
//
// 使用示例:
//
//	data := sharkfunc.DrainChannelN(ctx, msgCh, 100)
//	// data 最多包含 100 条消息，不会因等待更多数据而阻塞
func DrainChannelN[T any](ctx context.Context, ch <-chan T, size int, timeout time.Duration) ([]T, error) {
	// 预分配容量为 size 的切片，减少扩容开销
	var result []T = make([]T, 0, size)

	// 边界检查：nil channel 或无效 size 直接返回空结果
	if ch == nil || size <= 0 {
		return result, nil
	}

	// 阶段一：阻塞等待第一条数据
	// 这是唯一会阻塞的读取，确保在有数据时至少拿到一条
	select {
	case <-ctx.Done():
		// context 已取消，立即返回
		return result, ctx.Err()
	case v, ok := <-ch:
		if !ok {
			// channel 已关闭，返回空结果
			return result, errors.New("channel closed")
		}
		result = append(result, v)
	}

	start := time.Now()
	// 阶段二：非阻塞地尽量多读取数据
	// 使用 select+default 实现非阻塞读取，有数据就拿，没数据就返回
	for len(result) < size {
		select {
		case <-ctx.Done():
			// context 取消，返回已读取的数据
			return result, ctx.Err()
		case v, ok := <-ch:
			if !ok {
				// channel 关闭，返回已读取的数据
				return result, errors.New("channel closed")
			}
			result = append(result, v)
		default:
			if timeout > 0 && time.Since(start) <= timeout {
				sleepTime := time.Second
				if timeout-time.Since(start) < sleepTime {
					sleepTime = timeout - time.Since(start)
				}
				time.Sleep(sleepTime)
				continue
			}
			// channel 中没有立即可用的数据，返回已读取的数据
			// 避免因等待更多数据而无限阻塞
			return result, nil
		}
	}

	return result, nil
}

// Recover 从 panic 中恢复并记录错误日志。
//
// 该函数应当在 defer 语句中调用，用于捕获当前 goroutine 中的 panic，
// 防止 panic 向上传播导致整个程序崩溃，同时将 panic 信息和调用栈记录到日志中。
//
// 参数:
//   - logger: zap 日志记录器，用于输出 panic 信息。如果为 nil，不会记录日志。
//   - name: 标识名称，用于在日志中标识 panic 发生的位置或模块。
//
// 使用示例:
//
//	func worker() {
//	    defer sharkfunc.Recover(logger, "worker")
//	    // 可能 panic 的代码
//	}
func Recover(logger *zap.Logger, name string) {
	if r := recover(); r != nil {
		// 分配 4KB 的缓冲区用于获取调用栈信息
		buf := make([]byte, 4096)
		// 获取当前 goroutine 的调用栈，false 表示只获取当前 goroutine 的栈
		n := runtime.Stack(buf, false)
		// 记录包含名称、panic 内容和调用栈的错误日志
		logger.Error(fmt.Sprintf("panic: %s %v\n%s", name, r, string(buf[:n])))
	}
}

// Pointer 返回任意类型值的指针。
//
// 这是一个泛型辅助函数，简化了获取值指针的操作，避免在代码中频繁写 &v。
// 常用于需要指针参数的场景，如可选字段、protobuf 消息等。
//
// 类型参数:
//   - T: 任意类型。
//
// 参数:
//   - v: 需要获取指针的值。
//
// 返回值:
//   - 指向 v 的指针。
//
// 使用示例:
//
//	name := sharkfunc.Pointer("hello")  // *string
//	age := sharkfunc.Pointer(25)        // *int
func Pointer[T any](v T) *T {
	return &v
}

func FormatToRFC3330(s *string) *string {
	if s == nil || *s == "" {
		return nil
	}
	t, err := time.Parse(time.DateTime, *s)
	if err != nil {
		return nil
	}
	str := t.Format(time.RFC3339)
	return &str
}

func FormatRFC3330WithLocation(s *string, loc *time.Location) *string {
	if s == nil || *s == "" {
		return nil
	}
	t, err := time.ParseInLocation(time.DateTime, *s, loc)
	if err != nil {
		return nil
	}
	str := t.Format(time.RFC3339)
	return &str
}

func FormatRFC3330WithFormat(s *string, format string) *string {
	if s == nil || *s == "" {
		return nil
	}
	t, err := time.Parse(format, *s)
	if err != nil {
		return nil
	}
	str := t.Format(time.RFC3339)
	return &str
}

func FormatToRFC3330WithFormatAndLocation(s *string, format string, loc *time.Location) *string {
	if s == nil || *s == "" {
		return nil
	}
	t, err := time.ParseInLocation(format, *s, loc)
	if err != nil {
		return nil
	}
	str := t.Format(time.RFC3339)
	return &str
}

func MakeKey(args ...any) string {
	var b strings.Builder
	for _, arg := range args {
		s := fmt.Sprint(arg)
		b.WriteString(strconv.Itoa(len(s)))
		b.WriteByte(':')
		b.WriteString(s)
		b.WriteByte('|')
	}
	return b.String()
}
