// Package sharktimer 提供基于 Redis ZSet 的轻量级单机定时器。
//
// 设计特点：
//  1. 单机部署，不依赖分布式协调（同一 project+name+id 视为同一实例的 ZSet）
//  2. 基于 Redis ZSet 存储定时任务（score = 到期时间戳毫秒数，member = 定时器 ID）
//  3. Redis key 格式：{project}:timer:{name}-{id}（name 必须进入 key，避免同项目不同服务抢同一把 ZSet）
//  4. 定时器误差约 ±1 秒（轮询间隔为 1 秒）
//  5. 回调在 ZRem 成功之后才执行。ZRem 失败会打日志并重试直到成功，避免漏删导致下一轮重复触发
//  6. 回调函数通过 ants 协程池异步执行，避免阻塞定时检查线程
//  7. 使用 Snowflake 算法生成唯一定时器 ID，支持高并发创建
//
// 典型使用场景：
//   - 订单超时取消（30 分钟后取消未支付订单）
//   - 延迟消息推送
//   - 定时缓存刷新
//   - 会话超时管理
//
// 工作流程：
//  1. 业务方调用 AddTimer 向 Redis ZSet 添加一个定时任务
//  2. 后台轮询扫描已到期任务，ZRem 成功后才执行回调（ZRem 失败打日志并重试直到成功）
//  3. 若任务在触发前被 RemoveTimer 删除，则不会执行
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

// TimerRedis 定义了定时器所需的 Redis 操作接口。
// 使用者可以传入 *redis.Client 或自定义的 mock 实现进行测试。
//
// 实现该接口需要以下方法：
//   - ZRangeByScoreWithScores: 按分数范围查询（获取到期任务）
//   - ZRem: 删除成员（移除已触发的定时任务）
//   - ZAdd: 添加成员（注册新的定时任务）
type TimerRedis interface {
	ZRangeByScoreWithScores(ctx context.Context, key string, opt *redis.ZRangeBy) *redis.ZSliceCmd
	ZRem(ctx context.Context, key string, members ...interface{}) *redis.IntCmd
	ZAdd(ctx context.Context, key string, members ...redis.Z) *redis.IntCmd
}

// Timer 是基于 Redis ZSet 实现的内存级定时器。
//
// 核心机制：
//   - Redis ZSet 的 score 存储任务到期的时间戳（毫秒），member 存储定时器 ID
//   - 后台轮询线程每秒扫描一次已到期任务
//   - 回调函数存入 sync.Map，触发时通过 ants 协程池异步执行
//
// 注意：
//   - 该定时器不持久化回调函数（存储于内存 sync.Map），进程重启后未触发的定时器回调将丢失
//   - 如需在重启后恢复未到期任务，需要额外实现回调的持久化逻辑
//   - 定时精度约为 1 秒，不适合毫秒级精度的场景
//
// 零值 Timer 不可直接使用，必须通过 NewTimer() 创建。
type Timer struct {
	ctx             context.Context           // 上下文，用于控制定时器生命周期（通过 ctx.Done() 优雅退出）
	timerCallback   sync.Map                  // 定时器 ID → 回调函数的映射（内存存储，进程重启丢失）
	timerKey        string                    // Redis ZSet 的 key，格式：{project}:timer:{name}-{id}
	snowFlake       *sharksnowflake.Snowflake // Snowflake 实例，用于生成唯一定时器 ID
	pool            *ants.Pool                // 协程池，用于异步执行回调函数，避免阻塞轮询线程
	redis           TimerRedis                // Redis 接口，用于读写定时任务
	logger          *zap.Logger               // 由 NewTimer 传入；为 nil 时 Redis 错误不打日志
	defaultCallback func(timerId string)      // 默认回调，当定时器触发但未找到对应回调时执行
}

// NewTimer 创建一个新的 Timer 实例并启动后台轮询线程。
//
// 参数：
//   - ctx: 上下文，用于优雅停止定时器（ctx 取消后轮询线程退出）
//   - project: 项目名，用作 Redis key 前缀
//   - name: 服务名，写入 Redis key，避免同项目下不同服务共用一把 ZSet
//   - id: 实例标识，同一服务多实例用不同 id 区分
//   - redis: Redis 客户端接口（如 go-redis 的 *redis.Client）
//   - logger: zap 日志记录器（可为 nil）
//
// 使用示例：
//
//	timer := sharktimer.NewTimer(ctx, "myproject", "order-timer", "instance-1", rdb, logger)
//
//	// 30 分钟后取消订单
//	timer.AddTimer(30*time.Minute, func() {
//	    fmt.Println("订单超时，执行取消逻辑")
//	    // orderService.Cancel(orderId)
//	})
func NewTimer(ctx context.Context, project string, name string, id string, redis TimerRedis, logger *zap.Logger) *Timer {
	pool, err := ants.NewPool(10)
	if err != nil {
		panic(fmt.Sprintf("sharktimer ants.NewPool: %v", err))
	}
	t := &Timer{
		ctx:           ctx,
		redis:         redis,
		logger:        logger,
		timerCallback: sync.Map{},
		timerKey:      fmt.Sprintf("%v:timer:%v-%v", project, name, id),
		snowFlake:     sharksnowflake.NewSnowflake(),
		pool:          pool,
	}
	go t.processTimer()
	return t
}

func (t *Timer) logError(msg string, err error) {
	if t.logger == nil {
		return
	}
	t.logger.Error(msg, zap.String("key", t.timerKey), zap.Error(err))
}

// processTimer 是后台轮询线程的核心函数。
//
// 工作流程：
//  1. 每秒执行一次轮询，从 Redis ZSet 中查询已到期的定时任务
//  2. ZRem 删除已到期任务；失败则打日志并重试直到成功（成功前不执行回调）
//  3. 在 sync.Map 中查找对应的回调函数
//  4. 通过 ants 协程池异步执行回调（panic 会被 recover 捕获，不会导致轮询线程崩溃）
//  5. 若未找到回调函数，则调用 DefaultCallback（如果已设置）
//
// 退出条件：ctx.Done() 关闭时，释放协程池并退出。
func (t *Timer) processTimer() {
	if t.redis == nil {
		return
	}
	// safeCallback 包装回调函数，捕获 panic 防止轮询线程崩溃
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
			// 上下文取消：释放协程池资源后退出
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
			members := make([]interface{}, 0, len(result))
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
			// 遍历每个到期任务，查找并执行回调
			for _, v := range result {
				// 从 sync.Map 中取出并删除回调函数
				cb, ok := t.timerCallback.LoadAndDelete(v.Member)
				if ok {
					// 找到了回调函数：通过协程池异步执行
					if callback, ok := cb.(func()); ok {
						if err := t.pool.Submit(func() {
							safeCallback(callback)
						}); err != nil {
							t.logError("sharktimer pool.Submit failed", err)
						}
					}
				} else {
					// 未找到回调函数：执行默认回调（如果已设置）
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

// AddTimer 添加一个定时任务，在指定延迟后触发回调。
//
// 参数：
//   - durnation: 延迟时长（从当前时间开始计时）
//   - callback: 到期时执行的回调函数（可选，若为 nil 则触发时执行 DefaultCallback）
//
// 返回值：
//   - string: 定时器 ID（可用于 RemoveTimer 提前取消）
//
// 使用示例：
//
//	timer := sharktimer.NewTimer(ctx, "myproject", "order-timer", "inst-1", rdb, logger)
//
//	// 30 分钟后取消指定订单
//	timerId := timer.AddTimer(30*time.Minute, func() {
//	    fmt.Println("订单 #12345 已超时，自动取消")
//	    orderService.Cancel("12345")
//	})
//	fmt.Println("定时器 ID:", timerId)
//
//	// 如果用户在 30 分钟内主动取消，可以移除定时器
//	// timer.RemoveTimer(timerId)
func (t *Timer) AddTimer(durnation time.Duration, callback func()) string {
	// 使用 Snowflake 生成全局唯一定时器 ID
	id := cast.ToString(t.snowFlake.Generate())
	// 计算到期时间戳（毫秒）：当前时间 + 延迟时长
	timestamp := time.Now().Add(durnation).UnixMilli()
	// 将任务写入 Redis ZSet（score = 到期时间戳，member = 定时器 ID）
	t.redis.ZAdd(t.ctx, t.timerKey, redis.Z{Score: float64(timestamp), Member: id})
	if callback != nil {
		// 将回调函数存入内存映射
		t.timerCallback.Store(id, callback)
	}
	return id
}

// RemoveTimer 根据定时器 ID 删除一个尚未触发的定时任务。
//
// 如果定时器 ID 不存在（已触发或从未创建），则不做任何操作。
//
// 参数：
//   - timerId: AddTimer 返回的定时器 ID
//
// 使用示例：
//
//	// 创建定时器
//	timerId := timer.AddTimer(10*time.Minute, func() {
//	    fmt.Println("10 分钟后的任务")
//	})
//
//	// 在任务触发前取消（例如用户提前完成了操作）
//	timer.RemoveTimer(timerId)
//	fmt.Println("定时器已取消")
func (t *Timer) RemoveTimer(timerId string) {
	// 从内存中删除回调函数
	t.timerCallback.Delete(timerId)
	// 从 Redis ZSet 中删除定时任务
	t.redis.ZRem(t.ctx, t.timerKey, timerId)
}

// AddTimeWithId 添加定时任务并使用自定义 ID（而非自动生成）。
//
// 注意：如果 timerId 已存在，将覆盖原有的定时任务时间和回调函数。
//
// 参数：
//   - timerId: 自定义定时器 ID（建议使用业务唯一标识，如 "order:cancel:12345"）
//   - durnation: 延迟时长
//   - callback: 到期回调函数
//
// 使用示例：
//
//	// 使用业务标识作为定时器 ID，便于追踪和管理
//	orderId := "12345"
//	timer.AddTimeWithId(
//	    fmt.Sprintf("order:cancel:%s", orderId),
//	    30*time.Minute,
//	    func() {
//	        fmt.Printf("订单 %s 超时取消\n", orderId)
//	    },
//	)
//
//	// 可以在其他地方根据已知 ID 直接删除
//	timer.RemoveTimer(fmt.Sprintf("order:cancel:%s", orderId))
func (t *Timer) AddTimeWithId(timerId string, durnation time.Duration, callback func()) {
	// 计算到期时间戳（毫秒）
	timestamp := time.Now().Add(durnation).UnixMilli()
	// 写入 Redis ZSet（若 member 已存在，ZAdd 会更新其 score）
	t.redis.ZAdd(t.ctx, t.timerKey, redis.Z{Score: float64(timestamp), Member: timerId})
	if callback != nil {
		// 将回调函数存入内存映射（会覆盖同名 ID 的旧回调）
		t.timerCallback.Store(timerId, callback)
	}
}

// DefaultCallback 设置默认回调函数。
//
// 当定时器触发时，如果在 sync.Map 中未找到对应的回调函数，
// 则会调用此默认回调（参数为定时器 ID 字符串）。
//
// 使用场景：
//   - 回调持久化：将回调函数存入数据库，触发时根据 timerId 从数据库加载并执行
//   - 统一日志记录：所有未被显式注册回调的到期事件记录到日志
//   - 远程回调：根据 timerId 调用远程服务的 Webhook 接口
//
// 使用示例：
//
//	timer.DefaultCallback(func(timerId string) {
//	    // 从数据库加载回调配置
//	    config := loadCallbackFromDB(timerId)
//	    if config != nil {
//	        executeCallback(config)
//	    } else {
//	        log.Printf("未找到定时器 %s 的回调配置", timerId)
//	    }
//	})
func (t *Timer) DefaultCallback(cb func(timerId string)) {
	t.defaultCallback = cb
}
