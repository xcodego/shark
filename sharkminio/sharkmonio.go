// Package sharkminio 提供了 MinIO 对象存储客户端的创建和连接管理。
//
// MinIO 是一个兼容 Amazon S3 API 的高性能对象存储服务，
// 本包封装了 MinIO Go SDK 的连接创建，支持静态凭证认证。
package sharkminio

import (
	"context"
	"fmt"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Config 是 MinIO 的连接配置。
//
// 支持从 JSON、YAML、viper（mapstructure）等多种配置源加载。
type Config struct {
	// Host MinIO 服务地址，格式为 "host:port"（如 "127.0.0.1:9000"）
	Host string `json:"host" yaml:"host" mapstructure:"host"`
	// User 访问密钥 AccessKey，为空时使用匿名访问
	User string `json:"user" yaml:"user" mapstructure:"user"`
	// Password 秘密密钥 SecretKey，为空时使用匿名访问
	Password string `json:"password" yaml:"password" mapstructure:"password"`
}

// New 创建 MinIO 客户端。
//
// 使用静态凭证（AccessKey + SecretKey）的 V4 签名认证，
// 默认不使用 TLS（Secure: false），适用于内网环境。
//
// 使用示例:
//
//	// 创建 MinIO 客户端
//	client, err := sharkminio.New(ctx, &sharkminio.Config{
//	    Host:     "127.0.0.1:9000",
//	    User:     "minioadmin",
//	    Password: "minioadmin",
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// 使用客户端上传文件
//	_, err = client.FPutObject(ctx, "my-bucket", "file.txt", "/path/to/file.txt", minio.PutObjectOptions{})
//
// 参数:
//   - ctx: 上下文（当前未使用，保留用于未来扩展）
//   - config: MinIO 连接配置，不能为 nil
//
// 返回值:
//   - *minio.Client: MinIO 客户端实例
//   - error: 配置为空或连接失败时返回错误
func New(ctx context.Context, config *Config) (*minio.Client, error) {
	if config == nil {
		return nil, fmt.Errorf("config required")
	}

	endpoint := config.Host
	// 创建 MinIO 客户端，使用 V4 静态凭证认证，不启用 TLS
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(config.User, config.Password, ""),
		Secure: false,
	})
	if err != nil {
		return nil, err
	}
	return client, nil
}
