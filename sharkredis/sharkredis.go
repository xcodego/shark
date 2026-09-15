// Package sharkredis 提供了 Redis 客户端（集群/单机模式）的创建和连接管理。
//
// # 功能概述
//
// 本包封装了 go-redis v9 的集群客户端（ClusterClient）和单机客户端（Client）的创建，
// 提供统一配置入口，简化 Redis 连接的初始化和维护。
//
// # 核心能力
//
//   - 双模式支持：通过 NewCluster 创建集群模式，NewClient 创建单机/主从模式。
//   - 自定义 Dialer：支持在容器化环境中替换地址（如 Kubernetes Service IP → Pod IP），
//     通过 Config.ReplaceFrom / ReplaceTo 实现。
//   - 统一连接池配置：可配置空闲连接数、最大连接数、超时时间、重试策略等。
//   - 连接健康检查：创建客户端时自动执行 Ping 验证，确保连接可用。
//   - 辅助工具：helper.go 提供 Helper 结构体，封装常用的对象存取、批量扫描、批量删除等操作。
//
// # 配置加载
//
// Config 结构体支持 JSON、YAML 及 viper mapstructure 标签，可配合 viper、配置中心使用：
//
//	viper.UnmarshalKey("redis", &config)
//
// # 快速开始
//
// 集群模式：
//
//	cluster, err := sharkredis.NewCluster(ctx, &sharkredis.Config{
//	    Host:     []string{"127.0.0.1:6379", "127.0.0.1:6380"},
//	    Password: "pwd",
//	})
//
// 单机模式：
//
//	client, err := sharkredis.NewClient(ctx, &sharkredis.Config{
//	    Host:     []string{"127.0.0.1:6379"},
//	    Password: "pwd",
//	})
//
// 高级用法（Helper 对象存取）：
//
//	helper := sharkredis.NewHelperWithClient(client)
//	err = helper.SetObject(ctx, "user:1001", user, time.Hour).Err()
//	err = helper.GetObject(ctx, "user:1001", &user)
package sharkredis

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Config 是 Redis 的连接配置，统一管理集群和单机两种模式的连接参数。
//
// # 模式选择
//
// Host 字段的长度决定了客户端模式：
//   - 集群模式（NewCluster）：Host 应包含所有集群节点的地址（如 "node1:6379", "node2:6379"），
//     go-redis 会自动发现集群拓扑。
//   - 单机模式（NewClient）：Host[0] 作为直连地址，其余元素会被忽略。
//
// # 连接池参数
//
// PoolSize 和 MinIdleConns 为零值时分别采用默认值 200 和 20，以保持向后兼容。
// 在高并发场景下建议根据 QPS 调整 PoolSize；低负载场景可适当调低 MinIdleConns 节省资源。
//
// # 地址替换（Kubernetes 场景）
//
// ReplaceFrom / ReplaceTo 用于解决容器化环境中 Redis Cluster 返回的内部 Service IP
// 无法从外部访问的问题。例如：
//
//	ReplaceFrom: "redis-cluster-headless.namespace.svc.cluster.local"
//	ReplaceTo:   "10.0.1.x"  // 实际的 Pod IP
//
// 当自定义 Dialer 收到连接请求时，会自动将地址中的 ReplaceFrom 替换为 ReplaceTo。
// 注意：仅 ReplaceFrom 和 ReplaceTo 均非空时才启用替换逻辑。
//
// # 序列化支持
//
// 结构体同时标注了 json、yaml、mapstructure 三种 tag，可从以下配置源直接反序列化：
//   - 标准 JSON 文件或 API 响应
//   - YAML 配置文件
//   - viper / mapstructure 配置管理
type Config struct {
	// Host Redis 节点地址列表，格式为 "host:port"。
	// 集群模式传所有节点，单机模式传单个地址。
	Host []string `json:"host" yaml:"host" mapstructure:"host"`
	// Password 连接密码，为空字符串时不使用 AUTH 认证。
	Password string `json:"password" yaml:"password" mapstructure:"password"`
	// ReplaceFrom 需要被替换的地址前缀（如 Kubernetes Service 域名）。
	// 仅当 ReplaceFrom 和 ReplaceTo 均非空时，自定义 Dialer 才会执行替换。
	ReplaceFrom string `json:"replace_from" yaml:"replace_from" mapstructure:"replace_from"`
	// ReplaceTo 替换目标地址（如具体的 Pod IP）。
	ReplaceTo string `json:"replace_to" yaml:"replace_to" mapstructure:"replace_to"`
	// PoolSize 连接池最大连接数，0 时使用默认值 200。
	// 该值对每个集群节点分别生效（集群模式下是 per-node 的）。
	PoolSize int `json:"pool_size" yaml:"pool_size" mapstructure:"pool_size"`
	// MinIdleConns 连接池最小空闲连接数，0 时使用默认值 20。
	// 适当设置可减少冷连接建立延迟，但过多空闲连接会占用 Redis 服务端文件描述符。
	MinIdleConns int `json:"min_idle_conns" yaml:"min_idle_conns" mapstructure:"min_idle_conns"`
}

// NewCluster 创建 Redis 集群模式客户端并验证连接。
//
// 连接池配置:
//   - PoolSize: 200（最大连接数）
//   - MinIdleConns: 20（最小空闲连接数）
//   - ConnMaxIdleTime: 10 分钟
//   - ConnMaxLifetime: 30 分钟
//   - 读写/连接超时: 2 秒
//   - 重试: 最多 2 次，退避间隔 100ms~1s
//
// 使用示例:
//
//	cluster, err := sharkredis.NewCluster(ctx, &sharkredis.Config{
//	    Host:     []string{"127.0.0.1:6379", "127.0.0.1:6380"},
//	    Password: "myPassword",
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer cluster.Close()
//
//	// 使用集群客户端
//	cluster.Set(ctx, "mykey", "myvalue", 0)
//	val, _ := cluster.Get(ctx, "mykey").Result()
func NewCluster(ctx context.Context, config *Config) (*redis.ClusterClient, error) {
	// 防御性检查：Config 不能为 nil
	if config == nil {
		return nil, fmt.Errorf("config required")
	}

	// ---------- 连接池参数：零值兜底为默认值 ----------
	// 最小空闲连接数默认 20，维持一定量的热连接以降低请求延迟。
	minIdle := 20
	if config.MinIdleConns > 0 {
		minIdle = config.MinIdleConns
	}
	// 每个节点的连接池上限默认 200。
	poolSize := 200
	if config.PoolSize > 0 {
		poolSize = config.PoolSize
	}

	// ---------- 构建集群客户端 ----------
	client := redis.NewClusterClient(&redis.ClusterOptions{
		Addrs:    config.Host, // 集群所有节点地址，go-redis 会自动完成 CLUSTER SLOTS 发现
		Username: "default",   // Redis 6+ ACL 用户名，低版本 Redis 使用 "default"
		Password: config.Password,

		// --- 重试策略 ---
		MaxRetries:      2,                      // 命令失败后最多重试 2 次，防止雪崩
		MinRetryBackoff: 100 * time.Millisecond, // 重试最小退避间隔（首次重试）
		MaxRetryBackoff: time.Second,            // 重试最大退避间隔（指数退避上限）

		// --- 连接池 ---
		MinIdleConns:    minIdle,          // 维持的最小空闲连接数
		PoolSize:        poolSize,         // 每个节点的最大连接数
		ConnMaxIdleTime: 10 * time.Minute, // 空闲连接超过此时间将被关闭
		ConnMaxLifetime: 30 * time.Minute, // 连接最长存活时间（到达后主动关闭重建）

		// --- 超时 ---
		DialTimeout:  2 * time.Second, // TCP 连接建立超时
		ReadTimeout:  2 * time.Second, // Socket 读取超时（包含网络往返）
		WriteTimeout: 2 * time.Second, // Socket 写入超时

		// NewClient 回调：go-redis 在集群模式下用此函数创建到单节点的底层连接。
		NewClient: func(opt *redis.Options) *redis.Client {
			return redis.NewClient(opt)
		},

		// 自定义 Dialer：在建立 TCP 连接前，可替换目标地址。
		// 典型场景：Kubernetes 中将 Headless Service 域名替换为 Pod IP。
		Dialer: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if config.ReplaceFrom != "" && config.ReplaceTo != "" {
				addr = strings.ReplaceAll(addr, config.ReplaceFrom, config.ReplaceTo)
			}
			return net.Dial(network, addr)
		},
	})

	// 连接健康检查：执行 PING 验证所有节点可达
	_, err := client.Ping(ctx).Result()
	if err != nil {
		return nil, err
	}
	return client, nil
}

// NewClient 创建 Redis 单机/主从模式客户端并验证连接。
//
// 使用 config.Host[0] 作为连接地址，连接池参数与 NewCluster 一致。
//
// 使用示例:
//
//	client, err := sharkredis.NewClient(ctx, &sharkredis.Config{
//	    Host:     []string{"127.0.0.1:6379"},
//	    Password: "myPassword",
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer client.Close()
//
//	// 使用单机客户端
//	client.Set(ctx, "mykey", "myvalue", 10*time.Second)
func NewClient(ctx context.Context, config *Config) (*redis.Client, error) {
	// 防御性检查
	if config == nil {
		return nil, fmt.Errorf("config required")
	}
	if len(config.Host) == 0 {
		return nil, fmt.Errorf("at least one host required")
	}

	// ---------- 连接池参数：零值兜底为默认值 ----------
	// 最小空闲连接数默认 20。
	minIdle := 20
	if config.MinIdleConns > 0 {
		minIdle = config.MinIdleConns
	}
	// 连接池上限默认 200。
	poolSize := 200
	if config.PoolSize > 0 {
		poolSize = config.PoolSize
	}

	// ---------- 构建单机/主从客户端 ----------
	// 注意：go-redis 的 Options 直接传入 Addr，支持主从读写分离需通过 NewFailoverClient。
	client := redis.NewClient(&redis.Options{
		Addr:     config.Host[0], // 仅使用第一个地址作为直连目标
		Username: "default",      // Redis 6+ ACL 用户名
		Password: config.Password,

		// --- 重试策略 ---
		MaxRetries:      2,                      // 命令失败后最多重试 2 次
		MinRetryBackoff: 100 * time.Millisecond, // 重试最小退避间隔
		MaxRetryBackoff: time.Second,            // 重试最大退避间隔（指数退避上限）

		// --- 连接池 ---
		MinIdleConns:    minIdle,          // 维持的最小空闲连接数
		PoolSize:        poolSize,         // 连接池最大连接数
		ConnMaxIdleTime: 10 * time.Minute, // 空闲连接最大存活时间
		ConnMaxLifetime: 30 * time.Minute, // 连接最长存活时间（到达后主动关闭并重建）

		// --- 超时 ---
		DialTimeout:  2 * time.Second, // TCP 连接建立超时
		ReadTimeout:  2 * time.Second, // Socket 读取超时
		WriteTimeout: 2 * time.Second, // Socket 写入超时
	})

	// 连接健康检查
	_, err := client.Ping(ctx).Result()
	if err != nil {
		return nil, err
	}
	return client, nil
}
