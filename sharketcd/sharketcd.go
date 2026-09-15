// Package sharketcd 提供了 etcd 客户端的创建和连接管理。
//
// 封装了 etcd v3 客户端（go.etcd.io/etcd/client/v3）的连接创建、
// 认证配置和健康检查，为分布式配置中心和服务发现提供基础支持。
package sharketcd

import (
	"context"
	"fmt"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// Config 是 etcd 的连接配置。
//
// 支持从 JSON、YAML、viper（mapstructure）等多种配置源加载。
type Config struct {
	// Host etcd 集群端点地址列表，如 ["127.0.0.1:2379", "127.0.0.1:2380"]
	Host []string `json:"host" yaml:"host" mapstructure:"host"`
	// User 认证用户名，为空时不启用认证
	User string `json:"user" yaml:"user" mapstructure:"user"`
	// Password 认证密码，与 User 配合使用
	Password string `json:"password" yaml:"password" mapstructure:"password"`
}

// New 创建 etcd v3 客户端并执行健康检查。
//
// 创建流程:
//  1. 验证 config 不为 nil
//  2. 构造 clientv3.Config（连接超时 5 秒，可选认证）
//  3. 创建客户端
//  4. 执行 Get("health_check") 进行健康检查（3 秒超时）
//  5. 健康检查失败时自动关闭客户端
//
// 使用示例:
//
//	// 创建 etcd 客户端
//	client, err := sharketcd.New(ctx, &sharketcd.Config{
//	    Host:     []string{"127.0.0.1:2379"},
//	    User:     "root",
//	    Password: "password",
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer client.Close()
//
//	// 使用客户端读写数据
//	client.Put(ctx, "/config/app", `{"debug": true}`)
//	resp, _ := client.Get(ctx, "/config/app")
//
// 参数:
//   - ctx: 上下文，用于健康检查的超时控制
//   - config: etcd 连接配置，不能为 nil
//
// 返回值:
//   - *clientv3.Client: 已通过健康检查的 etcd 客户端
//   - error: 配置为空、连接失败或健康检查失败时返回错误
func New(ctx context.Context, config *Config) (*clientv3.Client, error) {
	if config == nil {
		return nil, fmt.Errorf("config required")
	}

	// 构造 etcd 客户端配置
	cfg := clientv3.Config{
		Endpoints:   config.Host,
		DialTimeout: 5 * time.Second, // 连接超时
	}

	// 可选认证配置
	if config.User != "" {
		cfg.Username = config.User
		cfg.Password = config.Password
	}

	// 创建 etcd 客户端
	client, err := clientv3.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("创建 etcd 客户端失败: %w", err)
	}

	// 健康检查：通过 Get 一个不存在的 key 来验证连接可用性
	// 使用独立超时（3 秒），避免长时间阻塞
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, err = client.Get(ctx, "health_check")
	if err != nil {
		// 健康检查失败时关闭客户端，避免资源泄漏
		client.Close()
		return nil, fmt.Errorf("etcd 健康检查失败: %w", err)
	}

	return client, nil
}
