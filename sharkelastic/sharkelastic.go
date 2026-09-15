package sharkelastic

import (
	"context"
	"fmt"

	"github.com/elastic/go-elasticsearch/v9"
)

// Config 是 Elasticsearch 的连接配置。
//
// 支持从 JSON、YAML、viper（mapstructure）等多种配置源加载。
type Config struct {
	// Host 集群地址列表，每个地址格式为 "http://host:port" 或 "https://host:port"
	// ES 客户端会自动进行节点发现和故障转移
	Host []string `json:"host" yaml:"host" mapstructure:"host"`
	// User 认证用户名（Basic Auth）
	User string `json:"user" yaml:"user" mapstructure:"user"`
	// Password 认证密码（Basic Auth）
	Password string `json:"password" yaml:"password" mapstructure:"password"`
}

// New 创建 Elasticsearch 客户端并验证连接。
//
// 使用 ES v9 官方客户端（github.com/elastic/go-elasticsearch），
// 支持 Basic Auth 认证和自动节点发现。
//
// 创建流程:
//  1. 验证 config 不为 nil
//  2. 使用 Host、User、Password 创建客户端
//  3. 执行 Ping 验证连接可用性
//
// 使用示例:
//
//	es, err := sharkelastic.New(ctx, &sharkelastic.Config{
//	    Host:     []string{"http://127.0.0.1:9200"},
//	    User:     "elastic",
//	    Password: "password",
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// 创建索引
//	es.CreateIndex(ctx, "users", 2,
//	    sharkelastic.FieldMapping{Name: "name", Type: sharkelastic.MappingTypeText},
//	    sharkelastic.FieldMapping{Name: "age", Type: sharkelastic.MappingTypeInteger},
//	)
//
// 参数:
//   - ctx: 上下文（当前未使用，保留用于未来扩展）
//   - config: ES 连接配置，不能为 nil
//
// 返回值:
//   - *SharkElastic: 包含已验证客户端的 SharkElastic 实例
//   - error: 配置为空、客户端创建失败或 Ping 不通时返回错误
func New(ctx context.Context, config *Config) (*SharkElastic, error) {
	if config == nil {
		return nil, fmt.Errorf("config required")
	}

	// 创建 ES 客户端，配置地址和认证信息
	client, err := elasticsearch.New(
		elasticsearch.WithAddresses(config.Host...),
		elasticsearch.WithBasicAuth(config.User, config.Password),
	)
	if err != nil {
		return nil, err
	}

	// Ping 验证连接是否可用
	_, err = client.Ping()
	if err != nil {
		return nil, err
	}

	return &SharkElastic{
		Client: client,
	}, nil
}
