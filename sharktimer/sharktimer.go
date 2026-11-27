// Package sharktimer 提供基于 Redis ZSet 的单机定时器，精度约 1 秒。
//
// 任务存于 ZSet（key 为 {project}:timer:{name}-{id}，score 为到期毫秒时间戳），
// 回调只保存在进程内存中；单机轮询，不做分布式抢任务。
package sharktimer

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/xcodego/shark/sharksnowflake"

	"github.com/panjf2000/ants/v2"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/cast"
	"go.uber.org/zap"
)

// TimerRedis 定义定时器所需的 Redis 操作。
type TimerRedis interface {
	ZRangeByScoreWithScores(ctx context.Context, key string, opt *redis.ZRangeBy) *redis.ZSliceCmd
	ZRem(ctx context.Context, key string, members ...any) *redis.IntCmd
	ZAdd(ctx context.Context, key string, members ...redis.Z) *redis.IntCmd
}

// Client 使用 Redis ZSet 管理定时任务，先从 ZSet 删除到期任务再执行回调。
// 回调仅保存在内存中，进程重启后到期的任务走默认回调；需通过 NewTimer 创建。
type Client struct {
	ctx             context.Context
	timerCallback   sync.Map
	timerKey        string
	snowFlake       *sharksnowflake.Snowflake
	pool            *ants.Pool
	redis           TimerRedis
	logger          *zap.Logger
	defaultCallback func(timerId string) // 到期任务无已注册回调时调用
}

// NewTimer 创建定时器并启动后台轮询；取消 ctx 可停止轮询并释放协程池。
// Redis key 为 {project}:timer:{name}-{id}；协程池（容量 10）创建失败时 panic。
func NewTimer(ctx context.Context, project string, name string, id string, redis TimerRedis, logger *zap.Logger) *Client {
	pool, err := ants.NewPool(10)
	if err != nil {
		panic(fmt.Sprintf("sharktimer ants.NewPool: %v", err))
	}
	t := &Client{
		ctx:           ctx,
		redis:         redis,
		logger:        logger,
		timerCallback: sync.Map{},
		timerKey:      fmt.Sprintf("%v:timer:%v-%v", project, name, id),
		snowFlake:     sharksnowflake.New(),
		pool:          pool,
	}
	go t.processTimer()
	return t
}

// logError 记录带 timerKey 的错误日志；logger 为 nil 时忽略。
func (t *Client) logError(msg string, err error) {
	if t.logger == nil {
		return
	}
	t.logger.Error(msg, zap.String("key", t.timerKey), zap.Error(err))
}

// processTimer 每秒轮询 ZSet，每次最多取 100 个到期任务：
// 先 ZRem 成功再在协程池中执行回调，避免重复触发；回调 panic 会被恢复并记录日志。
func (t *Client) processTimer() {
	if t.redis == nil {
		return
	}
	safeCallback := func(cb func()) {
		defer func() {
			if r := recover(); r != nil {
				if t.logger != nil {
					t.logger.Error("sharktimer callback panic", zap.Any("panic", r), zap.String("key", t.timerKey))
				}
			}
		}()
		cb()
	}
	for {
		select {
		case <-t.ctx.Done():
			t.pool.Release()
			return
		default:
			cmd := t.redis.ZRangeByScoreWithScores(t.ctx, t.timerKey, &redis.ZRangeBy{
				Min:    "0",
				Max:    fmt.Sprintf("%v", time.Now().UnixMilli()),
				Offset: 0,
				Count:  100,
			})
			result, err := cmd.Result()
			if err != nil {
				if t.ctx.Err() != nil {
					t.pool.Release()
					return
				}
				t.logError("sharktimer ZRange failed", err)
				time.Sleep(time.Second)
				continue
			}
			if len(result) == 0 {
				time.Sleep(time.Second)
				continue
			}
			members := make([]any, 0, len(result))
			for _, v := range result {
				members = append(members, v.Member)
			}
			for {
				if t.ctx.Err() != nil {
					t.pool.Release()
					return
				}
				if err := t.redis.ZRem(t.ctx, t.timerKey, members...).Err(); err != nil {
					t.logError("sharktimer ZRem failed, retry", err)
					time.Sleep(time.Second)
					continue
				}
				break
			}
			for _, v := range result {
				cb, ok := t.timerCallback.LoadAndDelete(v.Member)
				if ok {
					if callback, ok := cb.(func()); ok {
						if err := t.pool.Submit(func() {
							safeCallback(callback)
						}); err != nil {
							t.logError("sharktimer pool.Submit failed", err)
						}
					}
				} else {
					if t.defaultCallback != nil {
						if err := t.pool.Submit(func() {
							safeCallback(func() {
								t.defaultCallback(cast.ToString(v.Member))
							})
						}); err != nil {
							t.logError("sharktimer pool.Submit failed", err)
						}
					}
				}
			}
		}
	}
}

// AddTimer 添加 durnation 后到期的定时任务并返回雪花算法生成的 ID；callback 为 nil 时到期使用默认回调。
func (t *Client) AddTimer(durnation time.Duration, callback func()) string {
	id := cast.ToString(t.snowFlake.Generate())
	timestamp := time.Now().Add(durnation).UnixMilli()
	t.redis.ZAdd(t.ctx, t.timerKey, redis.Z{Score: float64(timestamp), Member: id})
	if callback != nil {
		t.timerCallback.Store(id, callback)
	}
	return id
}

// RemoveTimer 取消指定的定时任务，同时清除内存中的回调。
func (t *Client) RemoveTimer(timerId string) {
	t.timerCallback.Delete(timerId)
	t.redis.ZRem(t.ctx, t.timerKey, timerId)
}

// AddTimeWithId 添加使用业务自定义 ID 的定时任务；同 ID 任务会被覆盖到期时间和回调。
func (t *Client) AddTimeWithId(timerId string, durnation time.Duration, callback func()) {
	timestamp := time.Now().Add(durnation).UnixMilli()
	t.redis.ZAdd(t.ctx, t.timerKey, redis.Z{Score: float64(timestamp), Member: timerId})
	if callback != nil {
		t.timerCallback.Store(timerId, callback)
	}
}

// DefaultCallback 设置默认回调，到期任务在内存中没有已注册回调时（如进程重启后）调用，参数为任务 ID。
func (t *Client) DefaultCallback(cb func(timerId string)) {
	t.defaultCallback = cb
}
