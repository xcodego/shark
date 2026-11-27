// Package sharketcd 提供 etcd 客户端创建和健康检查。
package sharketcd

import (
	"context"
	"fmt"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// Config 是 etcd 连接配置。
type Config struct {
	// Host 是 etcd 集群端点地址列表。
	Host []string `json:"host" yaml:"host" mapstructure:"host"`
	// User 是认证用户名，为空时不启用认证。
	User string `json:"user" yaml:"user" mapstructure:"user"`
	// Password 是认证密码。
	Password string `json:"password" yaml:"password" mapstructure:"password"`
}

// New 创建 etcd 客户端并通过读取一个 key 做健康检查，拨号超时 5 秒；检查失败时关闭客户端并返回错误。
func New(ctx context.Context, config *Config) (*clientv3.Client, error) {
	if config == nil {
		return nil, fmt.Errorf("config required")
	}

	cfg := clientv3.Config{
		Endpoints:   config.Host,
		DialTimeout: 5 * time.Second,
	}

	if config.User != "" {
		cfg.Username = config.User
		cfg.Password = config.Password
	}

	client, err := clientv3.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("创建 etcd 客户端失败: %w", err)
	}

	// 在 ctx 上叠加 3 秒超时，避免健康检查长时间阻塞
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, err = client.Get(ctx, "health_check")
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("etcd 健康检查失败: %w", err)
	}

	return client, nil
}
