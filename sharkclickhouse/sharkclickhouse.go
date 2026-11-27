// Package sharkclickhouse 提供 ClickHouse 原生客户端连接。
package sharkclickhouse

import (
	"context"
	"crypto/tls"
	"fmt"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// Config 是 ClickHouse 原生协议的连接配置。
type Config struct {
	Host     []string `json:"host" yaml:"host" mapstructure:"host"`
	User     string   `json:"user" yaml:"user" mapstructure:"user"`
	Password string   `json:"password" yaml:"password" mapstructure:"password"`
	Database string   `json:"database" yaml:"database" mapstructure:"database"`
	TLS      bool     `json:"tls" yaml:"tls" mapstructure:"tls"`
}

// New 创建客户端并使用 ctx 验证连接。Host 使用原生协议地址，例如 localhost:9000。
// 调用方在不再使用客户端时应调用 Client.Close。
func New(ctx context.Context, config *Config) (driver.Conn, error) {
	if config == nil {
		return nil, fmt.Errorf("config required")
	}
	if len(config.Host) == 0 {
		return nil, fmt.Errorf("clickhouse host required")
	}
	for _, host := range config.Host {
		if strings.TrimSpace(host) == "" {
			return nil, fmt.Errorf("clickhouse host must not be empty")
		}
	}

	options := &clickhouse.Options{
		Addr: config.Host,
		Auth: clickhouse.Auth{
			Username: config.User,
			Password: config.Password,
			Database: config.Database,
		},
	}
	if config.TLS {
		options.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	client, err := clickhouse.Open(options)
	if err != nil {
		return nil, fmt.Errorf("open clickhouse: %w", err)
	}
	if err := client.Ping(ctx); err != nil {
		if closeErr := client.Close(); closeErr != nil {
			return nil, fmt.Errorf("ping clickhouse: %w; close clickhouse: %v", err, closeErr)
		}
		return nil, fmt.Errorf("ping clickhouse: %w", err)
	}
	return client, nil
}
