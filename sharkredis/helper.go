// Package sharkredis 提供了 Redis 客户端（集群/单机模式）的创建和连接管理。
//
// 本文件（helper.go）提供 Helper 结构体，封装了基于 go-redis 的高层辅助操作：
//   - 对象序列化存取（SetObject / GetObject / HSetObject / HGetObject）
//   - 模式匹配 Key 扫描（Scan / Keys）
//   - 模式匹配批量异步删除（Unlink）
//   - Hash 部分字段批量获取（HMGetToMap）
package sharkredis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"sync"
	"time"

	"github.com/bytedance/sonic"   // 高性能 JSON 编解码器（字节跳动开源）
	"github.com/redis/go-redis/v9" // go-redis v9，支持集群/单机
	"github.com/tidwall/gjson"     // 快速 JSON 路径查询，用于 HSetObject 中遍历 JSON 对象
)

// Helper 是对 go-redis 单节点客户端（*redis.Client）和集群客户端（*redis.ClusterClient）的统一封装。
//
// # 设计目标
//
// Helper 屏蔽了集群和单机两种不同底层客户端的差异，调用方只需持有同一个 Helper 实例，
// 所有方法内部自动判断并路由到正确的客户端执行。这避免了业务代码中到处写 if cluster != nil
// 的分支判断。
//
// # 使用方式
//
//   - 通过 NewHelperWithClient 或 NewHelperWithCluster 构造。
//   - client 和 cluster 二者互斥，只会有一个非 nil。
//   - 若两者都为 nil（如零值初始化），所有方法将返回 redis.Nil 或零值，不会 panic。
//
// # 典型用法
//
//	helper := sharkredis.NewHelperWithClient(client)
//	err := helper.SetObject(ctx, "key", someStruct, time.Hour).Err()
//	err := helper.GetObject(ctx, "key", &someStruct)
type Helper struct {
	client  *redis.Client        // 单机/主从模式客户端，非 nil 时优先使用
	cluster *redis.ClusterClient // 集群模式客户端，当 client 为 nil 时使用
}

// NewHelperWithClient 使用单节点 Redis 客户端构造 Helper。
// 适用于调用方已经持有 *redis.Client 的场景（如由 NewClient 创建）。
func NewHelperWithClient(client *redis.Client) *Helper {
	return &Helper{
		client: client,
	}
}

// NewHelperWithCluster 使用 Redis 集群客户端构造 Helper。
// 适用于调用方已经持有 *redis.ClusterClient 的场景（如由 NewCluster 创建）。
func NewHelperWithCluster(cluster *redis.ClusterClient) *Helper {
	return &Helper{
		cluster: cluster,
	}
}

// Keys 是 Scan 的别名，返回匹配 pattern 的所有 key 的流式 channel。
//
// 与 Scan 完全等价，提供更语义化的命名以匹配 Redis KEYS 命令的习惯用法。
// 注意：内部实现使用 SCAN 游标迭代，不会像原生 KEYS 命令那样阻塞 Redis 服务端。
func (h *Helper) Keys(ctx context.Context, pattern string) <-chan string {
	return h.Scan(ctx, pattern)
}

// Scan 以游标迭代方式扫描匹配 pattern 的所有 key，结果通过 channel 流式返回。
//
// # 工作原理
//
// 使用 Redis 原生 SCAN 命令（而非 KEYS），以游标方式分批遍历 key 空间，避免阻塞 Redis 服务端。
// 每次 SCAN 调用返回约 100 条 key（COUNT 参数）。
//
// # 单节点模式
//
//   - 在 h.client 上启动一个 goroutine 循环执行 SCAN。
//   - 游标从 0 开始，每次迭代获取一批 key 并发送到 channel。
//   - 当游标归零时表示遍历完成，goroutine 退出并 close channel。
//
// # 集群模式
//
//   - 通过 ClusterClient.ForEachMaster 遍历集群中所有主节点。
//   - 为每个主节点启动一个独立的 goroutine，并发扫描该节点分片上的所有 key。
//   - 所有 goroutine 将 key 汇入同一个输出 channel。
//   - 使用 sync.WaitGroup 等待所有节点扫描完成后 close channel。
//   - 注意：不同节点的 slot 空间天然不重叠，正常情况下不会产生重复 key。
//
// # 资源管理
//
//   - channel 缓冲区大小为 1024，平衡内存占用与 goroutine 阻塞风险。
//     当消费端处理速度低于扫描速度时，缓冲区可暂存结果，避免扫描 goroutine 被阻塞。
//   - 支持 ctx 取消：任何 goroutine 在发送/接收时若检测到 ctx.Done() 则立即退出。
//     当外部 cancel 时，所有 goroutine 会逐步退出，channel 最终被关闭。
//
// # 错误处理策略
//
//   - 遇到错误时静默退出 goroutine（不向外暴露具体错误）。
//   - 这意味着扫描可能不完整——调用方无法知道是否有节点扫描失败。
//   - 若需要感知扫描错误，请使用包级函数或在业务层另做容错处理。
//
// # 边界情况
//
//   - client 和 cluster 均为 nil 时，返回一个立即关闭的空 channel，确保 range 循环不会阻塞。
//   - pattern 为空字符串时，行为等同于 "*"（匹配所有 key）。
func (h *Helper) Scan(ctx context.Context, pattern string) <-chan string {
	// ---- 单节点分支 ----
	if h.client != nil {
		// 带缓冲 channel，平衡内存与吞吐
		out := make(chan string, 1024)
		go func() {
			defer close(out) // 扫描完成或出错时关闭 channel，通知消费者结束
			var cursor uint64
			for {
				// 每次循环开始前检查 ctx 是否已取消
				select {
				case <-ctx.Done():
					return
				default:
				}
				// 调用 SCAN 命令，每批最多返回 100 个 key
				keys, cur, err := h.client.Scan(ctx, cursor, pattern, 100).Result()
				if err != nil {
					return // 出错静默退出
				}
				// 将本批 key 逐个发送到 channel
				for _, k := range keys {
					select {
					case out <- k: // 写入 channel
					case <-ctx.Done(): // 发送过程中检测到取消信号
						return
					}
				}
				// 游标归零表示遍历完成
				if cur == 0 {
					return
				}
				cursor = cur // 更新游标，继续下一批
			}
		}()
		return out
	}

	// ---- 集群分支 ----
	if h.cluster != nil {
		out := make(chan string, 1024)
		var wg sync.WaitGroup // 等待所有节点的扫描 goroutine 完成
		go func() {
			defer close(out) // 所有节点扫描完成后关闭 channel
			// ForEachMaster 遍历集群中的每个主节点（不包含从节点，避免重复扫描）
			_ = h.cluster.ForEachMaster(ctx, func(ctx context.Context, node *redis.Client) error {
				wg.Add(1)
				// 为每个主节点启动独立 goroutine 并发扫描
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
							return // 该节点出错，静默退出，不影响其他节点
						}
						for _, k := range keys {
							select {
							case out <- k: // 写入共享 channel
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
			wg.Wait() // 阻塞直到所有主节点的扫描 goroutine 退出
		}()
		return out
	}

	// ---- 客户端未初始化分支 ----
	// client 和 cluster 均为 nil，返回已关闭的空 channel
	out := make(chan string)
	close(out)
	return out
}

// Unlink 扫描匹配 pattern 的所有 key，并使用 UNLINK 命令异步删除。
//
// # UNLINK vs DEL
//
// DEL 是同步删除命令，删除大 key（如包含数百万元素的 Set）时会导致 Redis 主线程阻塞。
// UNLINK 是异步删除命令，Redis 会在后台线程中逐步回收内存，主线程不会阻塞。
// 因此本方法适用于需要删除大量数据且对延迟敏感的在线场景。
//
// # 批量策略
//
// 每次 SCAN 返回约 100 个 key，然后按每批 50 个 key 执行 UNLINK 命令。
// 这样避免了单次 UNLINK 参数过多导致 Redis 命令缓冲区溢出或网络包过大。
//
// # 单节点模式
//
// 在 h.client 上循环 SCAN，分批 UNLINK，遇到任何错误立即返回。
//
// # 集群模式
//
// 通过 ForEachMaster 遍历所有主节点。每个节点内部串行扫描并批量 UNLINK。
// ForEachMaster 的闭包若返回非 nil 错误，则会中断对其他主节点的遍历并立即返回该错误。
// 这意味着如果第 2 个节点出错，后续节点将不会被处理。
//
// # 上下文取消
//
// 每次循环迭代和每次 UNLINK 调用都会检查 ctx 是否取消。取消时返回 ctx.Err()，
// 此时已经删除的 key 不会回滚，调用方如需精确控制建议在独立的 goroutine 中调用。
//
// # 注意事项
//
//   - pattern 为空字符串时不做额外校验，实际效果等同匹配所有 key（"*"），
//     请调用方自行保证 pattern 合法性。
//   - client 和 cluster 均为 nil 时直接返回 nil（视为空操作而非错误）。
func (h *Helper) Unlink(ctx context.Context, pattern string) error {
	// ---- 单节点分支 ----
	if h.client != nil {
		var cursor uint64
		const batchSize = 50 // 每批最多 UNLINK 的 key 数量，平衡效率与命令大小
		for {
			// 每次循环前检查 ctx 是否已取消
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			// 扫描一批 key（最多 100 个）
			keys, cur, err := h.client.Scan(ctx, cursor, pattern, 100).Result()
			if err != nil {
				return err
			}
			// 分批 UNLINK，避免单次命令参数过多
			for i := 0; i < len(keys); i += batchSize {
				end := i + batchSize
				if end > len(keys) {
					end = len(keys)
				}
				if err := h.client.Unlink(ctx, keys[i:end]...).Err(); err != nil {
					return err
				}
			}
			// 游标归零表示遍历完成
			if cur == 0 {
				return nil
			}
			cursor = cur
		}
	}

	// ---- 集群分支 ----
	if h.cluster != nil {
		// ForEachMaster 遍历所有主节点，闭包返回错误会中断遍历
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

	// ---- 客户端未初始化分支 ----
	// 无可用客户端时视为空操作，直接返回 nil
	return nil
}

// SetObject 将任意 Go 值序列化为 JSON 字符串后以 SET 命令写入 Redis。
//
// # 序列化方式
//
// 使用标准库 encoding/json 将 value 序列化为 JSON 字节，然后转为 string 存入 Redis。
// 这意味着 value 必须满足 encoding/json 的序列化规则：
//   - 结构体字段需要大写导出且带有 json tag（或可导出）。
//   - 循环引用结构体会导致序列化失败。
//
// # 参数说明
//
//   - key：Redis key，支持任何 Redis 合法的 key 字符串。
//   - value：任意可 JSON 序列化的 Go 值（结构体、map、slice、基本类型等）。
//   - expiration：key 的过期时间。
//     传 0 表示永不过期；
//     传正数表示到期后自动删除（Redis 的 TTL 机制，精度为毫秒）。
//
// # 返回值
//
// 返回 *redis.StatusCmd：
//   - 成功时 .Val() 为 "OK"，.Err() 为 nil。
//   - 序列化失败时 .Err() 为 json.Marshal 的错误。
//   - Redis 写入失败时 .Err() 为 go-redis 的网络/协议错误。
//   - client 和 cluster 均为 nil 时返回 nil（调用方需自行判空）。
//
// # 使用示例
//
//	type User struct {
//	    ID   int    `json:"id"`
//	    Name string `json:"name"`
//	}
//	user := User{ID: 1, Name: "Alice"}
//
//	// 存入 Redis，1 小时后过期
//	if err := helper.SetObject(ctx, "user:1", user, time.Hour).Err(); err != nil {
//	    log.Fatal(err)
//	}
func (h *Helper) SetObject(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd {
	// 序列化 Go 值为 JSON 字节
	bytes, err := json.Marshal(value)
	if err != nil {
		return redis.NewStatusResult("", err)
	}
	// 使用 SET 命令写入 Redis，key 为指定的 key，value 为 JSON 字符串
	if h.client != nil {
		return h.client.Set(ctx, key, string(bytes), expiration)
	}
	if h.cluster != nil {
		return h.cluster.Set(ctx, key, string(bytes), expiration)
	}
	// client 和 cluster 均为 nil，返回 nil 让调用方自行处理
	return nil
}

// GetObject 通过 GET 命令读取 Redis 中的 JSON 字符串，并反序列化到 value 指向的对象。
//
// # 执行流程
//
//  1. 执行 GET key 命令获取 JSON 字符串。
//  2. 若 key 不存在（返回 redis.Nil），直接将该错误返回。
//  3. 若 key 存在，使用 encoding/json 将 JSON 字符串反序列化到 value 指向的对象。
//
// # 参数说明
//
//   - key：Redis key。
//   - value：必须是已分配内存的非 nil 指针（如 &someStruct{}），反序列化结果将写入该指针指向的对象。
//     传入 nil 指针或非指针类型时 json.Unmarshal 会 panic。
//
// # 返回值
//
// 返回 *redis.StatusCmd：
//   - 成功时 .Val() 为 "OK"，.Err() 为 nil。
//   - key 不存在时 .Err() 为 redis.Nil（可用 errors.Is(err, redis.Nil) 判断）。
//   - JSON 解析失败时 .Err() 为 json.Unmarshal 的错误（类型不匹配、格式错误等）。
//   - client 和 cluster 均为 nil 时 .Err() 为 redis.Nil。
//
// # 使用示例
//
//	var user User
//	err := helper.GetObject(ctx, "user:1", &user)
//	if errors.Is(err, redis.Nil) {
//	    // key 不存在
//	} else if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Println(user.Name) // 使用反序列化后的对象
func (h *Helper) GetObject(ctx context.Context, key string, value any) *redis.StatusCmd {
	var result string
	var err error

	// 执行 GET 命令
	if h.client != nil {
		result, err = h.client.Get(ctx, key).Result()
	} else if h.cluster != nil {
		result, err = h.cluster.Get(ctx, key).Result()
	} else {
		// client 和 cluster 均为 nil
		return redis.NewStatusResult("", redis.Nil)
	}
	// 检查 GET 结果：key 不存在或其他网络错误
	if err != nil {
		return redis.NewStatusResult("", err)
	}

	// 将 JSON 字符串反序列化到 value 指向的对象
	err = json.Unmarshal([]byte(result), value)
	if err != nil {
		return redis.NewStatusResult("", err)
	}
	return redis.NewStatusResult("OK", nil)
}

// HSetObject 将任意 Go 值序列化为 JSON 后，拆解为 field-value 对以 HSET 命令写入 Redis Hash。
//
// # 实现原理
//
//  1. 使用 sonic.Marshal 将 value 序列化为 JSON 字节（sonic 性能优于标准库）。
//  2. 使用 gjson.ParseBytes 将 JSON 解析为 gjson.Result。
//  3. 遍历 JSON 对象的顶层字段，构建 map[string]string。
//  4. 调用 HSET 命令将 map 中的所有 field-value 对写入 Hash。
//
// # 数据结构映射
//
// JSON tag 会被自动识别为 Hash field 名。例如：
//
//	type User struct {
//	    ID   int    `json:"id"`
//	    Name string `json:"name"`
//	}
//
// 写入后 Hash 中会有 field "id" → "1"、field "name" → "Alice"。
// 注意：所有值都会被转为字符串存储，int/bool 等类型在存入后丢失原始类型信息。
//
// # 参数说明
//
//   - key：Redis Hash 的 key。
//   - value：任意可 JSON 序列化的 Go 值，顶层必须可展开为 JSON Object（结构体或 map）。
//     传入基础类型（如 string、int）或数组会因无法展开而只写入部分或零个字段。
//
// # 返回值
//
// 返回 *redis.IntCmd：
//   - 成功时 .Val() 为实际写入的字段数量（新增的 field 计数，已存在的 field 被覆盖时不计入）。
//   - 序列化失败时 .Err() 为 sonic.Marshal 的错误。
//   - client 和 cluster 均为 nil 时返回 nil。
//
// # 使用示例
//
//	user := User{ID: 1, Name: "Alice"}
//	n, err := helper.HSetObject(ctx, "user:hash:1", user).Result()
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Printf("写入 %d 个字段\n", n)
func (h *Helper) HSetObject(ctx context.Context, key string, value any) *redis.IntCmd {
	// 预分配 field-value map
	var mvalue map[string]string = make(map[string]string)

	// 步骤 1：使用 sonic 高性能序列化为 JSON
	bytes, err := sonic.Marshal(value)
	if err != nil {
		return redis.NewIntResult(0, err)
	}

	// 步骤 2：使用 gjson 解析 JSON 并提取顶层字段
	// gjson.Map() 遍历 JSON 对象的所有键，返回 map[string]gjson.Result
	jobject := gjson.ParseBytes(bytes)
	for k, v := range jobject.Map() {
		// v.String() 会去除 JSON 字符串引号，数值型直接转字符串
		mvalue[k] = v.String()
	}

	// 步骤 3：执行 HSET 命令，将所有 field-value 对写入 Hash
	if h.client != nil {
		return h.client.HSet(ctx, key, mvalue)
	}
	if h.cluster != nil {
		return h.cluster.HSet(ctx, key, mvalue)
	}
	return nil
}

// HGetObject 从 Redis Hash 中获取字段值并反序列化到 value 指向的结构体。
//
// # 获取策略
//
// 根据是否传入 fields 参数选择不同的 Redis 命令：
//   - 有 fields：使用 HMGET 只拉取指定字段，避免当 Hash 很大时拉取全量数据。
//   - 无 fields：使用 HGETALL 拉取 Hash 的全部字段，再从中匹配结构体字段。
//
// # 类型映射
//
// 通过反射将 map[string]string 的字段值写入结构体字段。字段匹配规则：
//   - 优先使用结构体字段的 json tag 名称。
//   - 若无 json tag，使用字段的 Go 名称。
//   - Hash 中不存在的 field 会被忽略（对应结构体字段保持零值）。
//   - 支持的字段类型：string、bool、int 系列、uint 系列、float 系列，以及可 JSON 反序列化的复杂类型。
//
// # 参数说明
//
//   - key：Redis Hash 的 key。
//   - value：必须是非 nil 的指针，且指向一个结构体（不是 map）。
//     反序列化结果通过反射直接写入该结构体字段。
//   - fields：可选，指定需要获取的 Hash field 名列表。
//     注意：fields 中的名称应与结构体 json tag 一致，而非 Go 字段名。
//
// # 返回值
//
//   - 成功时返回 nil。
//   - value 不是指针或为 nil 时返回 "dst must be a non-nil pointer" 错误。
//   - value 不指向结构体时返回 "dst must point to a struct" 错误。
//   - Redis 命令执行失败时返回 go-redis 的错误。
//   - client 和 cluster 均为 nil 时返回 redis.Nil。
//
// # 使用示例
//
//	type User struct {
//	    ID    int    `json:"id"`
//	    Name  string `json:"name"`
//	    Email string `json:"email"`
//	}
//
//	// 获取整个 Hash
//	var user User
//	err := helper.HGetObject(ctx, "user:hash:1", &user)
//
//	// 仅获取 id 和 name 字段（推荐：Hash 字段很多时避免全量拉取）
//	err = helper.HGetObject(ctx, "user:hash:1", &user, "id", "name")
func (h *Helper) HGetObject(ctx context.Context, key string, value any, fields ...string) error {
	// 选择底层客户端，使用 redis.Cmdable 接口统一调用
	var cmdable redis.Cmdable
	if h.client != nil {
		cmdable = h.client
	} else if h.cluster != nil {
		cmdable = h.cluster
	} else {
		return redis.Nil // 无可用客户端
	}

	// 反射校验：value 必须是非 nil 的结构体指针
	v := reflect.ValueOf(value)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return errors.New("dst must be a non-nil pointer")
	}
	v = v.Elem() // 解引用指针，获取实际结构体值
	if v.Kind() != reflect.Struct {
		return errors.New("dst must point to a struct")
	}

	// 存储 field → string 映射，后续由 mapToStruct 反射写入结构体
	m := make(map[string]string)

	if len(fields) > 0 {
		// ---- 指定字段模式：使用 HMGET ----
		// 只拉取需要的字段，适合 Hash 字段数大的场景
		vals, err := cmdable.HMGet(ctx, key, fields...).Result()
		if err != nil {
			return err
		}
		// 遍历返回的值，将非 nil 的值转为 string 放入 map
		for i, field := range fields {
			if vals[i] == nil {
				continue // 该 field 不存在，跳过
			}
			// HMGET 返回值类型是 []interface{}，可能为 string 或 []byte
			switch v := vals[i].(type) {
			case string:
				m[field] = v
			case []byte:
				m[field] = string(v) // 字节切片转 string
			default:
				m[field] = fmt.Sprint(v) // 兜底转为字符串
			}
		}
	} else {
		// ---- 全量字段模式：使用 HGETALL ----
		// HGETALL 返回 map[string]string，即已经是 field→string 形式
		vals, err := cmdable.HGetAll(ctx, key).Result()
		if err != nil {
			return err
		}
		m = vals // 直接使用
	}

	// 将 map 数据通过反射写入结构体字段
	h.mapToStruct(m, v)
	return nil
}

// mapToStruct 将 map[string]string 的值通过反射写入结构体各个字段。
//
// # 字段匹配规则
//
//  1. 遍历结构体的所有字段（reflect.Type.NumField）。
//  2. 跳过不可设置的字段（未导出字段）。
//  3. 获取字段的 json tag 作为 map key；若 json tag 为空，则使用字段的 Go 名称。
//  4. 在 map 中查找对应 key，若不存在则跳过（该字段保持零值）。
//  5. 根据字段的 Kind（类型）将字符串值转换为对应类型并调用 Set 方法写入。
//
// # 支持的类型
//
//   - string：直接赋值。
//   - bool：使用 strconv.ParseBool 解析。
//   - int / int8 / int16 / int32 / int64：使用 strconv.ParseInt 解析。
//   - uint / uint8 / uint16 / uint32 / uint64：使用 strconv.ParseUint 解析。
//   - float32 / float64：使用 strconv.ParseFloat 解析。
//   - 其他（struct、切片、map 等）：使用 json.Unmarshal 反序列化。
//
// # 错误处理
//
// 类型转换失败（如将 "abc" 解析为 int）时会立即返回错误，不会继续处理后续字段。
// json.Unmarshal 失败时错误信息会包含字段名以便定位问题。
func (h *Helper) mapToStruct(m map[string]string, v reflect.Value) error {
	t := v.Type() // 获取结构体的 reflect.Type，用于遍历字段
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i) // 结构体字段的元信息（名称、类型、tag 等）
		fv := v.Field(i) // 字段的 reflect.Value，用于设置值
		if !fv.CanSet() {
			continue // 跳过未导出字段（不可设置）
		}

		// 确定字段在 map 中对应的 key：
		// 优先使用 json tag，若无则使用 Go 字段名
		key := sf.Tag.Get("json")
		if key == "" {
			key = sf.Name
		}

		// 在 map 中查找该 key
		s, ok := m[key]
		if !ok {
			continue // Hash 中不存在该字段，跳过（保持零值）
		}

		// 根据字段类型进行字符串→目标类型的转换
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
			// 对于 struct、slice、map 等复杂类型，使用 JSON 反序列化
			if err := json.Unmarshal([]byte(s), fv.Addr().Interface()); err != nil {
				return fmt.Errorf("field %s: %w", sf.Name, err)
			}
		}
	}
	return nil
}

// HMGetToMap 使用 HMGET 命令获取 Hash 中指定字段的值，返回 field → string 的 map。
//
// # 与 HGetObject 的区别
//
// HMGetToMap 返回原始的 map[string]string，不进行结构体绑定，适合以下场景：
//   - 数据结构不固定（如动态配置项）。
//   - 只需要少数几个字段，不需要映射完整结构体。
//   - 需要对返回值做自定义处理（如类型转换、默认值填充）。
//
// # 参数说明
//
//   - key：Redis Hash 的 key。
//   - fields：需要获取的 Hash field 名列表。若为空切片，HMGET 将不会查询任何字段，
//     返回一个空的 map（HMGET 不带参数时通常返回错误，但此处由 go-redis 处理）。
//
// # 返回值
//
//   - 成功时返回 map[string]string，key 为 field 名，value 为字段值（string 类型）。
//     Hash 中不存在的 field 不会出现在返回的 map 中（nil 值被跳过）。
//   - Redis 命令失败时返回 nil 和错误。
//   - client 和 cluster 均为 nil 时返回 "redis client is nil" 错误。
//
// # 使用示例
//
//	m, err := helper.HMGetToMap(ctx, "config:app", "host", "port", "debug")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	host := m["host"] // "127.0.0.1"
//	// "port" 不存在时为 ""（不在 map 中，需用 ok 模式检查）
func (h *Helper) HMGetToMap(ctx context.Context, key string, fields ...string) (map[string]string, error) {
	// 选择底层客户端
	var cmdable redis.Cmdable
	if h.client != nil {
		cmdable = h.client
	} else if h.cluster != nil {
		cmdable = h.cluster
	} else {
		return nil, errors.New("redis client is nil")
	}

	// 执行 HMGET 命令
	vals, err := cmdable.HMGet(ctx, key, fields...).Result()
	if err != nil {
		return nil, err
	}

	// 构建返回的 map，预分配容量以减少扩容
	result := make(map[string]string, len(fields))
	for i, field := range fields {
		// 防御性检查：防止索引越界（正常情况下不会发生）
		if i >= len(vals) {
			break
		}
		// nil 值表示该 field 在 Hash 中不存在，跳过
		if vals[i] == nil {
			continue
		}
		// 将值转为 string（HMGET 返回 []interface{}，每个元素可能是 string 或 []byte）
		switch v := vals[i].(type) {
		case string:
			result[field] = v
		case []byte:
			result[field] = string(v) // 字节切片转 string
		default:
			result[field] = fmt.Sprint(v) // 兜底转换
		}
	}
	return result, nil
}
