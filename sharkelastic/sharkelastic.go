// Package sharkelastic 提供 Elasticsearch 客户端及 SQL 风格查询辅助函数。
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
	// Host 集群地址列表，格式为 "http://host:port" 或 "https://host:port"
	Host []string `json:"host" yaml:"host" mapstructure:"host"`
	// User 认证用户名（Basic Auth）
	User string `json:"user" yaml:"user" mapstructure:"user"`
	// Password 认证密码（Basic Auth）
	Password string `json:"password" yaml:"password" mapstructure:"password"`
}

// New 创建 Elasticsearch 客户端并通过 Ping 验证连接可用。
//
// 使用 ES v9 官方客户端，支持 Basic Auth；config 为 nil、客户端创建失败或 Ping 不通时返回错误。
// ctx 当前未使用，保留用于未来扩展。
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
//	es.CreateIndex(ctx, "users", 2,
//	    sharkelastic.FieldMapping{Name: "name", Type: sharkelastic.MappingTypeText},
//	    sharkelastic.FieldMapping{Name: "age", Type: sharkelastic.MappingTypeInteger},
//	)
func New(ctx context.Context, config *Config) (*Client, error) {
	if config == nil {
		return nil, fmt.Errorf("config required")
	}

	client, err := elasticsearch.New(
		elasticsearch.WithAddresses(config.Host...),
		elasticsearch.WithBasicAuth(config.User, config.Password),
	)
	if err != nil {
		return nil, err
	}

	_, err = client.Ping()
	if err != nil {
		return nil, err
	}

	return &Client{
		Elastic: client,
	}, nil
}
