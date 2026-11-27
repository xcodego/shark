// Package sharkrisingwave 提供 RisingWave 的 GORM 连接和 zap 日志适配。
package sharkrisingwave

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Config 是 RisingWave 连接配置，RisingWave 兼容 PostgreSQL 协议。
type Config struct {
	Host     string `json:"host" yaml:"host" mapstructure:"host"`             // 服务地址，格式为 host:port
	User     string `json:"user" yaml:"user" mapstructure:"user"`             // 用户名
	Password string `json:"password" yaml:"password" mapstructure:"password"` // 密码
	Database string `json:"database" yaml:"database" mapstructure:"database"` // 数据库名
}

// New 创建 GORM 连接（禁用 SSL）并通过 Ping 检查连通性；logger 为 nil 时不输出 GORM 日志，ctx 当前未使用。
// 连接池：最大空闲 10、最大打开 30，空闲 10 分钟、存活 1 小时后回收。
func New(ctx context.Context, logger *zap.Logger, config *Config) (*gorm.DB, error) {
	if config == nil {
		return nil, fmt.Errorf("config required")
	}
	// 用户名和密码可能含特殊字符，需转义后再拼入 DSN
	dsn := fmt.Sprintf(
		"postgres://%s:%s@%s/%s?sslmode=disable",
		url.QueryEscape(config.User),
		url.QueryEscape(config.Password),
		config.Host,
		config.Database,
	)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: &log{
			logger: logger,
		},
	})
	if err != nil {
		return nil, err
	}
	gdb, _ := db.DB()
	gdb.SetConnMaxIdleTime(10 * time.Minute)
	gdb.SetConnMaxLifetime(1 * time.Hour)
	gdb.SetMaxIdleConns(10)
	gdb.SetMaxOpenConns(30)
	if err := gdb.Ping(); err != nil {
		return nil, err
	}
	return db, err
}

// log 实现 gorm logger.Interface，将 GORM 日志写入 zap。
type log struct {
	logger *zap.Logger
	level  logger.LogLevel
}

// LogMode 返回指定日志级别的 logger 副本，level 决定 Trace 是否记录每条 SQL。
func (z *log) LogMode(level logger.LogLevel) logger.Interface {
	return &log{level: level, logger: z.logger}
}

// Info 将 GORM Info 日志写入 zap。
func (z *log) Info(ctx context.Context, msg string, data ...any) {
	if z.logger == nil {
		return
	}
	z.logger.Info("RW执行Info", zap.String("msg", msg), zap.Any("data", data))
}

// Warn 将 GORM Warn 日志写入 zap。
func (z *log) Warn(ctx context.Context, msg string, data ...any) {
	if z.logger == nil {
		return
	}
	z.logger.Warn("RW执行Warn", zap.String("msg", msg), zap.Any("data", data))
}

// Error 将 GORM Error 日志写入 zap。
func (z *log) Error(ctx context.Context, msg string, data ...any) {
	if z.logger == nil {
		return
	}
	z.logger.Error("RW执行Error", zap.String("msg", msg), zap.Any("data", data))
}

// Trace 记录 SQL 错误；日志级别达到 Info 时额外记录每条 SQL 及耗时。
// 记录未找到和唯一键冲突（23505）属于正常业务分支，直接忽略。
func (z *log) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	if z.logger == nil {
		return
	}
	var sql string
	var rows int64
	var sqlLoaded bool = false
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			if pgErr.Code == "23505" {
				return
			}
		}
		sql, rows = fc()
		sqlLoaded = true
		z.logger.Error("RW执行失败", zap.String("sql", sql), zap.Int64("rows", rows), zap.Error(err))
	}
	if z.level >= logger.Info {
		elapsed := time.Since(begin)
		if !sqlLoaded {
			sql, rows = fc()
		}
		z.logger.Info("RW执行日志", zap.String("sql", sql), zap.Int64("rows", rows), zap.Duration("elapsed", elapsed))
	}
}
