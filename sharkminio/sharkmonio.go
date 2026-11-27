// Package sharkminio 提供 MinIO 客户端创建。
package sharkminio

import (
	"context"
	"fmt"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Config 是 MinIO 连接配置。
type Config struct {
	// Host 是 MinIO 服务地址，格式为 host:port。
	Host string `json:"host" yaml:"host" mapstructure:"host"`
	// User 是 Access Key。
	User string `json:"user" yaml:"user" mapstructure:"user"`
	// Password 是 Secret Key。
	Password string `json:"password" yaml:"password" mapstructure:"password"`
}

// New 创建使用静态 V4 凭证的 MinIO 客户端，固定使用 HTTP（不启用 TLS），不做连通性检查；ctx 当前未使用。
func New(ctx context.Context, config *Config) (*minio.Client, error) {
	if config == nil {
		return nil, fmt.Errorf("config required")
	}

	endpoint := config.Host
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(config.User, config.Password, ""),
		Secure: false,
	})
	if err != nil {
		return nil, err
	}
	return client, nil
}
