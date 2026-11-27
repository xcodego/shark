// Package sharkcache 提供支持多级回源和并发请求合并的泛型缓存。
package sharkcache

import (
	"errors"

	"github.com/xcodego/shark/sharkutils"
	"golang.org/x/sync/singleflight"
)

// ErrNotFound 表示所有 seeker 均未命中。
var ErrNotFound = errors.New("sharkcache: not found")

// ErrTypeAssertion 表示类型断言失败，通常是 seeker 返回的类型与泛型 T 不匹配。
var ErrTypeAssertion = errors.New("sharkcache: type assertion failed")

// Cache 是泛型缓存防击穿器，基于 singleflight 合并同一 key 的并发查询，
// 并通过 seeker 链实现多级查询（如：本地缓存 → Redis → DB）。
//
// seeker 返回规则：
//   - 返回非 nil 值（error 为 nil 或 ErrNotFound）：命中，立即返回；
//   - 返回 nil 值（error 为 nil 或 ErrNotFound）：未命中，尝试下一层；
//   - 返回其他 error：存储故障，立即返回该错误，不再回退；
//   - 所有 seeker 均未命中：返回 ErrNotFound。
//
// 示例：
//
//	cache := sharkcache.New(
//	    func(args ...any) (*User, error) { return localCache.Get(args[0].(int64)), nil },
//	    func(args ...any) (*User, error) { return redis.GetUser(args[0].(int64)) },
//	    func(args ...any) (*User, error) { return db.FindUser(args[0].(int64)) },
//	)
//	user, err := cache.Get(userId)
type Cache[T any] struct {
	seekers []func(args ...any) (*T, error) // 查询链，索引越小越优先
	sg      singleflight.Group              // 合并同一 key 的并发请求
}

// New 创建缓存防击穿器。seekers 按顺序组成查询链（越靠前越优先），每个 seeker 接收 Get 传入的 args。
func New[T any](seekers ...func(args ...any) (*T, error)) *Cache[T] {
	return &Cache[T]{
		seekers: seekers,
	}
}

// Get 根据参数依次经 seeker 链查询，返回命中的值；全部未命中返回 ErrNotFound。
//
// 执行流程：
//  1. 将 args 序列化为 JSON 字符串作为 key（见 sharkutils.MakeKey），相同参数共享同一次查询；
//  2. 通过 singleflight 保证同一 key 的并发请求只执行一次；
//  3. 按顺序遍历 seekers，按 Cache 文档中的规则判断命中、未命中或故障。
//
// 示例：
//
//	user, err := cache.Get(userId)
//	user, err := cache.Get(userId, orgId, status) // 多参数
func (c *Cache[T]) Get(args ...any) (*T, error) {
	key := sharkutils.MakeKey(args...)

	v, err, _ := c.sg.Do(key, func() (any, error) {
		for _, seeker := range c.seekers {
			v, err := seeker(args...)
			if err != nil && !errors.Is(err, ErrNotFound) {
				return nil, err
			}
			if v != nil {
				return v, nil
			}
		}
		return nil, ErrNotFound
	})
	if err != nil {
		return nil, err
	}
	// singleflight 返回 any，需断言回 *T；失败说明 seeker 返回类型与 T 不符
	if v, ok := v.(*T); ok {
		return v, nil
	}
	return nil, ErrTypeAssertion
}
