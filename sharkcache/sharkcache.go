package sharkcache

import (
	"errors"

	"github.com/lornshark/shark/sharkfunc"
	"golang.org/x/sync/singleflight"
)

var ErrNotFound = errors.New("sharkcache: not found")

// ErrTypeAssertion 类型断言失败，通常是因为 seeker 返回的类型与泛型 T 不匹配。
var ErrTypeAssertion = errors.New("sharkcache: type assertion failed")

// Cache 是一个泛型缓存穿透防护器。
//
// 它基于 singleflight 实现，用于防止缓存击穿：
//   - 多个 goroutine 同时请求同一个 key 时，只有一个会真正执行查询，
//     其他 goroutine 会等待并共享同一个结果。
//
// 它通过 seeker 链实现多级缓存回退（如：本地缓存 → Redis → DB）：
//   - seeker[0] 先查询一级缓存，命中则返回；
//   - 未命中则 seeker[1] 查询二级缓存，命中则返回；
//   - 依此类推，所有 seeker 都未命中则返回 ErrNotFound。
//
// 使用示例：
//
//	// 定义 seeker 链：本地缓存 → Redis → 数据库
//	cache := sharkcache.New(
//	    // seeker 0: 本地缓存
//	    func(args ...any) (*User, error) {
//	        return localCache.Get(args[0].(int64)), nil
//	    },
//	    // seeker 1: Redis
//	    func(args ...any) (*User, error) {
//	        return redis.GetUser(args[0].(int64))
//	    },
//	    // seeker 2: 数据库
//	    func(args ...any) (*User, error) {
//	        return db.FindUser(args[0].(int64))
//	    },
//	)
//
//	user, err := cache.Get(userId)
//	if err != nil {
//	    // 处理错误
//	}
type Cache[T any] struct {
	seekers []func(args ...any) (*T, error)
	sg      singleflight.Group
}

// New 创建一个缓存穿透防护器。
//
// seekers 按顺序组成查询链：索引越小越优先。
// 每个 seeker 接收 Get 传入的 args，返回 (*T, error)。
//
// 示例：
//
//	cache := New[User](
//	    localCacheSeeker,
//	    redisSeeker,
//	    dbSeeker,
//	)
func New[T any](seekers ...func(args ...any) (*T, error)) *Cache[T] {
	return &Cache[T]{
		seekers: seekers,
	}
}

// Get 根据参数获取缓存值。
//
// 执行流程：
//  1. 将 args 序列化后 MD5 生成 key，用于 singleflight 去重。
//  2. 通过 singleflight.Do 确保同一 key 只执行一次查询。
//  3. 按顺序遍历 seekers：任一返回非空值即成功返回。
//  4. 所有 seeker 都失败或返回 nil，则返回 ErrNotFound。
//
// args 会被 sonic.Marshal 序列化后计算 MD5 作为去重 key，
// 因此相同参数会自动合并为一次查询。
//
// 示例：
//
//	// 简单用法：按 ID 查询
//	user, err := cache.Get(userId)
//
//	// 多参数：按多个字段查询
//	user, err := cache.Get(userId, orgId, status)
func (c *Cache[T]) Get(args ...any) (*T, error) {
	// 将参数序列化为字节后计算 MD5，作为 singleflight 的去重 key

	key := sharkfunc.MakeKey(args...)

	// singleflight: 同一 key 的并发请求只会执行一次
	v, err, _ := c.sg.Do(key, func() (any, error) {
		// 按顺序尝试每个 seeker
		for _, seeker := range c.seekers {
			v, err := seeker(args...)
			if err != nil {
				// 当前 seeker 出错（如 Redis 连不上），
				// 继续尝试下一个 seeker（回退到数据库）
				continue
			}
			if v != nil {
				// 命中缓存/数据库，直接返回
				return v, nil
			}
		}
		// 所有 seeker 都未命中
		return nil, ErrNotFound
	})
	if err != nil {
		return nil, err
	}
	// singleflight 返回 any，需要类型断言回 *T
	if v, ok := v.(*T); ok {
		return v, nil
	}
	return nil, ErrTypeAssertion
}
