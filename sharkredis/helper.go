package sharkredis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/go-redsync/redsync/v4"
	goredis "github.com/go-redsync/redsync/v4/redis/goredis/v9"
	"github.com/redis/go-redis/v9"
	"github.com/tidwall/gjson"
)

// Helper 统一封装单机和集群 Redis 客户端的常用操作，client 与 cluster 只会设置其一；
// 两者都未设置时各方法按各自说明返回空结果或错误。project 和 name 用于拼接分布式锁 key。
type Helper struct {
	project    string
	name       string
	client     *redis.Client
	cluster    *redis.ClusterClient
	redisMutex *redsync.Redsync
}

// NewHelperWithClient 使用单机客户端创建 Helper；project、name 用于生成锁 key。
func NewHelperWithClient(client *redis.Client, project, name string) *Helper {
	return &Helper{
		project:    project,
		name:       name,
		client:     client,
		redisMutex: redsync.New(goredis.NewPool(client)),
	}
}

// NewHelperWithCluster 使用集群客户端创建 Helper；project、name 用于生成锁 key。
func NewHelperWithCluster(cluster *redis.ClusterClient, project, name string) *Helper {
	return &Helper{
		project:    project,
		name:       name,
		cluster:    cluster,
		redisMutex: redsync.New(goredis.NewPool(cluster)),
	}
}

// Keys 是 Scan 的别名，以流式 channel 返回匹配的 key（不使用 KEYS 命令）。
func (h *Helper) Keys(ctx context.Context, pattern string) <-chan string {
	return h.Scan(ctx, pattern)
}

// Scan 使用 SCAN 迭代并通过 channel 返回匹配的 key，集群模式下遍历所有主节点（key 顺序不保证）。
// 调用方必须消费完 channel 或取消 ctx，否则扫描协程会阻塞；
// ctx 取消或扫描出错时 channel 直接关闭，错误不会返回给调用方。未配置客户端时返回已关闭的 channel。
func (h *Helper) Scan(ctx context.Context, pattern string) <-chan string {
	if h.client != nil {
		out := make(chan string, 1024)
		go func() {
			defer close(out)
			var cursor uint64
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				keys, cur, err := h.client.Scan(ctx, cursor, pattern, 100).Result()
				if err != nil {
					return
				}
				for _, k := range keys {
					select {
					case out <- k:
					case <-ctx.Done():
						return
					}
				}
				if cur == 0 {
					return
				}
				cursor = cur
			}
		}()
		return out
	}

	if h.cluster != nil {
		out := make(chan string, 1024)
		var wg sync.WaitGroup
		go func() {
			defer close(out)
			_ = h.cluster.ForEachMaster(ctx, func(ctx context.Context, node *redis.Client) error {
				wg.Add(1)
				go func(node *redis.Client) {
					defer wg.Done()
					var cursor uint64
					for {
						select {
						case <-ctx.Done():
							return
						default:
						}
						keys, cur, err := node.Scan(ctx, cursor, pattern, 100).Result()
						if err != nil {
							return
						}
						for _, k := range keys {
							select {
							case out <- k:
							case <-ctx.Done():
								return
							}
						}
						if cur == 0 {
							return
						}
						cursor = cur
					}
				}(node)
				return nil
			})
			wg.Wait()
		}()
		return out
	}

	out := make(chan string)
	close(out)
	return out
}

// Unlink 使用 SCAN 和分批 UNLINK 删除匹配的 key；出错或取消时可能只删除部分 key。
// 未配置客户端时不执行操作。
func (h *Helper) Unlink(ctx context.Context, pattern string) error {
	if h.client != nil {
		var cursor uint64
		const batchSize = 50
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			keys, cur, err := h.client.Scan(ctx, cursor, pattern, 100).Result()
			if err != nil {
				return err
			}
			for i := 0; i < len(keys); i += batchSize {
				end := i + batchSize
				if end > len(keys) {
					end = len(keys)
				}
				if err := h.client.Unlink(ctx, keys[i:end]...).Err(); err != nil {
					return err
				}
			}
			if cur == 0 {
				return nil
			}
			cursor = cur
		}
	}

	if h.cluster != nil {
		return h.cluster.ForEachMaster(ctx, func(ctx context.Context, node *redis.Client) error {
			var cursor uint64
			const batchSize = 50
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}
				keys, cur, err := node.Scan(ctx, cursor, pattern, 100).Result()
				if err != nil {
					return err
				}
				for i := 0; i < len(keys); i += batchSize {
					end := i + batchSize
					if end > len(keys) {
						end = len(keys)
					}
					if err := node.Unlink(ctx, keys[i:end]...).Err(); err != nil {
						return err
					}
				}
				if cur == 0 {
					return nil
				}
				cursor = cur
			}
		})
	}

	return nil
}

// SetObject 使用 encoding/json 序列化 value 并写入 Redis；序列化失败时返回携带错误的命令结果，
// 未配置客户端时返回 nil（调用方需判空）。
func (h *Helper) SetObject(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd {
	bytes, err := json.Marshal(value)
	if err != nil {
		return redis.NewStatusResult("", err)
	}
	if h.client != nil {
		return h.client.Set(ctx, key, string(bytes), expiration)
	}
	if h.cluster != nil {
		return h.cluster.Set(ctx, key, string(bytes), expiration)
	}
	return nil
}

// GetObject 读取并用 encoding/json 反序列化到 value；
// key 不存在或未配置客户端时，返回的命令结果中错误为 redis.Nil。
func (h *Helper) GetObject(ctx context.Context, key string, value any) *redis.StatusCmd {
	var result string
	var err error

	if h.client != nil {
		result, err = h.client.Get(ctx, key).Result()
	} else if h.cluster != nil {
		result, err = h.cluster.Get(ctx, key).Result()
	} else {
		return redis.NewStatusResult("", redis.Nil)
	}
	if err != nil {
		return redis.NewStatusResult("", err)
	}

	err = json.Unmarshal([]byte(result), value)
	if err != nil {
		return redis.NewStatusResult("", err)
	}
	return redis.NewStatusResult("OK", nil)
}

// HSetObject 将 value 序列化后，把顶层字段（按 json 名）以字符串写入 Redis Hash；
// 未配置客户端时返回 nil（调用方需判空）。
func (h *Helper) HSetObject(ctx context.Context, key string, value any) *redis.IntCmd {
	var mvalue map[string]string = make(map[string]string)

	bytes, err := sonic.Marshal(value)
	if err != nil {
		return redis.NewIntResult(0, err)
	}

	jobject := gjson.ParseBytes(bytes)
	for k, v := range jobject.Map() {
		mvalue[k] = v.String()
	}

	if h.client != nil {
		return h.client.HSet(ctx, key, mvalue)
	}
	if h.cluster != nil {
		return h.cluster.HSet(ctx, key, mvalue)
	}
	return nil
}

// HGetObject 将 Hash 字段读入结构体指针 value；指定 fields 时使用 HMGET，否则读取全部字段。
// 未配置客户端时返回 redis.Nil。字段值转换失败的错误会被忽略：转换在第一个失败字段处中止，其后的字段不会被赋值。
func (h *Helper) HGetObject(ctx context.Context, key string, value any, fields ...string) error {
	var cmdable redis.Cmdable
	if h.client != nil {
		cmdable = h.client
	} else if h.cluster != nil {
		cmdable = h.cluster
	} else {
		return redis.Nil
	}

	v := reflect.ValueOf(value)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return errors.New("dst must be a non-nil pointer")
	}
	v = v.Elem()
	if v.Kind() != reflect.Struct {
		return errors.New("dst must point to a struct")
	}

	m := make(map[string]string)

	if len(fields) > 0 {
		vals, err := cmdable.HMGet(ctx, key, fields...).Result()
		if err != nil {
			return err
		}
		for i, field := range fields {
			if vals[i] == nil {
				continue
			}
			switch v := vals[i].(type) {
			case string:
				m[field] = v
			case []byte:
				m[field] = string(v)
			default:
				m[field] = fmt.Sprint(v)
			}
		}
	} else {
		vals, err := cmdable.HGetAll(ctx, key).Result()
		if err != nil {
			return err
		}
		m = vals
	}

	h.mapToStruct(m, v)
	return nil
}

// mapToStruct 按 json tag（无则用字段名）将 Hash 字符串值转换并写入结构体，
// 基础类型直接转换，其余类型按 JSON 反序列化；遇到第一个转换错误即返回。
func (h *Helper) mapToStruct(m map[string]string, v reflect.Value) error {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		fv := v.Field(i)
		if !fv.CanSet() {
			continue
		}

		key := sf.Tag.Get("json")
		if key == "" {
			key = sf.Name
		}

		s, ok := m[key]
		if !ok {
			continue
		}

		switch fv.Kind() {
		case reflect.String:
			fv.SetString(s)

		case reflect.Bool:
			b, err := strconv.ParseBool(s)
			if err != nil {
				return err
			}
			fv.SetBool(b)

		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			n, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return err
			}
			fv.SetInt(n)

		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			n, err := strconv.ParseUint(s, 10, 64)
			if err != nil {
				return err
			}
			fv.SetUint(n)

		case reflect.Float32, reflect.Float64:
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return err
			}
			fv.SetFloat(f)

		default:
			if err := json.Unmarshal([]byte(s), fv.Addr().Interface()); err != nil {
				return fmt.Errorf("field %s: %w", sf.Name, err)
			}
		}
	}
	return nil
}

// HMGetToMap 获取指定 Hash 字段并返回字符串 map；不存在的字段不会出现在结果中。
func (h *Helper) HMGetToMap(ctx context.Context, key string, fields ...string) (map[string]string, error) {
	var cmdable redis.Cmdable
	if h.client != nil {
		cmdable = h.client
	} else if h.cluster != nil {
		cmdable = h.cluster
	} else {
		return nil, errors.New("redis client is nil")
	}

	vals, err := cmdable.HMGet(ctx, key, fields...).Result()
	if err != nil {
		return nil, err
	}

	result := make(map[string]string, len(fields))
	for i, field := range fields {
		if i >= len(vals) {
			break
		}
		if vals[i] == nil {
			continue
		}
		switch v := vals[i].(type) {
		case string:
			result[field] = v
		case []byte:
			result[field] = string(v)
		default:
			result[field] = fmt.Sprint(v)
		}
	}
	return result, nil
}

// mutexKey 生成锁 key：{project}:mutex:{name}:{value 以 : 连接}。
func (h *Helper) mutexKey(value ...any) string {
	sv := make([]string, len(value))
	for i, v := range value {
		sv[i] = fmt.Sprint(v)
	}
	s := strings.Join(sv, ":")
	return fmt.Sprintf("%v:mutex:%v:%v", h.project, h.name, s)
}

// NewMutex 创建分布式锁，key 为 {project}:mutex:{name}:{value 以 : 连接}。
func (h *Helper) NewMutex(value ...any) *SharkMutex {
	key := h.mutexKey(value...)
	return &SharkMutex{
		mutex: h.redisMutex,
		key:   key,
	}
}
