// Package sharkredis 提供 Redis 单机/集群客户端构造（NewClient、NewCluster）、
// 统一两种客户端的操作封装（Helper）和基于 redsync 的分布式锁（SharkMutex）。
package sharkredis

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Config 配置 Redis 客户端。NewCluster 使用全部 Host 地址，NewClient 只使用 Host[0]；
// PoolSize 和 MinIdleConns 为零时分别默认为 200 和 20。用户名固定为 "default"。
type Config struct {
	// Host 是 Redis 节点地址，格式为 host:port。
	Host []string `json:"host" yaml:"host" mapstructure:"host"`
	// Password 是 Redis 密码。
	Password string `json:"password" yaml:"password" mapstructure:"password"`
	// ReplaceFrom 是连接集群节点时要替换的地址子串（strings.ReplaceAll），用于把集群返回的内网地址映射为可达地址；
	// 仅 NewCluster 使用，且需 ReplaceTo 也非空才生效。
	ReplaceFrom string `json:"replace_from" yaml:"replace_from" mapstructure:"replace_from"`
	// ReplaceTo 是替换后的内容。
	ReplaceTo string `json:"replace_to" yaml:"replace_to" mapstructure:"replace_to"`
	// PoolSize 是每个节点的最大连接数，0 时默认为 200。
	PoolSize int `json:"pool_size" yaml:"pool_size" mapstructure:"pool_size"`
	// MinIdleConns 是最小空闲连接数，0 时默认为 20。
	MinIdleConns int `json:"min_idle_conns" yaml:"min_idle_conns" mapstructure:"min_idle_conns"`
}

// NewCluster 创建集群客户端并通过 PING 检查连接；config 为 nil 返回错误。
func NewCluster(ctx context.Context, config *Config) (*redis.ClusterClient, error) {
	if config == nil {
		return nil, fmt.Errorf("config required")
	}

	minIdle := 20
	if config.MinIdleConns > 0 {
		minIdle = config.MinIdleConns
	}
	poolSize := 200
	if config.PoolSize > 0 {
		poolSize = config.PoolSize
	}

	client := redis.NewClusterClient(&redis.ClusterOptions{
		Addrs:    config.Host,
		Username: "default",
		Password: config.Password,

		MaxRetries:      2,
		MinRetryBackoff: 100 * time.Millisecond,
		MaxRetryBackoff: time.Second,

		MinIdleConns:    minIdle,
		PoolSize:        poolSize,
		ConnMaxIdleTime: 10 * time.Minute,
		ConnMaxLifetime: 30 * time.Minute,

		DialTimeout:  2 * time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,

		NewClient: func(opt *redis.Options) *redis.Client {
			return redis.NewClient(opt)
		},

		Dialer: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if config.ReplaceFrom != "" && config.ReplaceTo != "" {
				addr = strings.ReplaceAll(addr, config.ReplaceFrom, config.ReplaceTo)
			}
			return net.Dial(network, addr)
		},
	})

	_, err := client.Ping(ctx).Result()
	if err != nil {
		return nil, err
	}
	return client, nil
}

// NewClient 创建单机客户端，使用 Host[0] 并通过 PING 检查连接；config 为 nil 或 Host 为空返回错误。
func NewClient(ctx context.Context, config *Config) (*redis.Client, error) {
	if config == nil {
		return nil, fmt.Errorf("config required")
	}
	if len(config.Host) == 0 {
		return nil, fmt.Errorf("at least one host required")
	}

	minIdle := 20
	if config.MinIdleConns > 0 {
		minIdle = config.MinIdleConns
	}
	poolSize := 200
	if config.PoolSize > 0 {
		poolSize = config.PoolSize
	}

	client := redis.NewClient(&redis.Options{
		Addr:     config.Host[0],
		Username: "default",
		Password: config.Password,

		MaxRetries:      2,
		MinRetryBackoff: 100 * time.Millisecond,
		MaxRetryBackoff: time.Second,

		MinIdleConns:    minIdle,
		PoolSize:        poolSize,
		ConnMaxIdleTime: 10 * time.Minute,
		ConnMaxLifetime: 30 * time.Minute,

		DialTimeout:  2 * time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
	})

	_, err := client.Ping(ctx).Result()
	if err != nil {
		return nil, err
	}
	return client, nil
}
