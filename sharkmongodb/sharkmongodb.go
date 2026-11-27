// Package sharkmongodb 提供 MongoDB 客户端创建和连接检查，以及基于 Change Stream 的变更监听（BinlogListener）。
package sharkmongodb

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Config 是 MongoDB 连接配置。
type Config struct {
	// Host 是 MongoDB 地址，格式为 host:port。
	Host string `json:"host" yaml:"host" mapstructure:"host"`

	// User 是认证用户名；New 总是把用户名和密码拼入连接 URI，因此需要提供认证信息。
	User string `json:"user" yaml:"user" mapstructure:"user"`

	// Password 是认证密码。
	Password string `json:"password" yaml:"password" mapstructure:"password"`
}

// New 创建 MongoDB 客户端并 Ping 验证连接，认证源固定为 admin；config 为 nil 时返回错误。
func New(ctx context.Context, config *Config) (*mongo.Client, error) {
	if config == nil {
		return nil, fmt.Errorf("config required")
	}

	uri := fmt.Sprintf("mongodb://%v:%v@%v/?authSource=admin", config.User, config.Password, config.Host)

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return nil, err
	}

	err = client.Ping(ctx, nil)
	if err != nil {
		return nil, err
	}
	return client, nil
}
