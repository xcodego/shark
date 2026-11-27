// Package sharkdb 封装 MySQL（GORM）连接、链式查询（SharkTable）、
// Keyset 游标分页与导出（TableScan）以及 Binlog 监听（BinlogListener）。
package sharkdb

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"

	"go.uber.org/zap"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Config 是 MySQL 数据库连接配置。
type Config struct {
	// Host 数据库连接地址，格式为 "host:port"，如 "127.0.0.1:3306"
	Host string `json:"host" yaml:"host" mapstructure:"host"`
	// User 数据库用户名
	User string `json:"user" yaml:"user" mapstructure:"user"`
	// Password 数据库密码
	Password string `json:"password" yaml:"password" mapstructure:"password"`
	// Database 要连接的数据库名称
	Database string `json:"database" yaml:"database" mapstructure:"database"`
	// Tls 非空时启用 TLS 加密连接（任意非空值等效，内部统一注册为名为 "tidb" 的配置）；空串表示不使用 TLS
	Tls string `json:"tls" yaml:"tls" mapstructure:"tls"`
	// MaxIdleConns 最大空闲连接数，0 使用默认值 20
	MaxIdleConns int `json:"max_idle_conns" yaml:"max_idle_conns" mapstructure:"max_idle_conns"`
	// MaxOpenConns 最大打开连接数，0 使用默认值 100
	MaxOpenConns int `json:"max_open_conns" yaml:"max_open_conns" mapstructure:"max_open_conns"`
	// ConnMaxIdleMinute 空闲连接最大存活分钟数，0 使用默认值 5
	ConnMaxIdleMinute int `json:"conn_max_idle_minute" yaml:"conn_max_idle_minute" mapstructure:"conn_max_idle_minute"`
	// ConnMaxLifetimeMinute 连接最大存活分钟数，0 使用默认值 60
	ConnMaxLifetimeMinute int `json:"conn_max_lifetime_minute" yaml:"conn_max_lifetime_minute" mapstructure:"conn_max_lifetime_minute"`
}

// NewDb 创建 GORM 数据库连接并 Ping 验证可用；config 为 nil 返回错误。
// 连接池参数取配置值，为 0 时使用默认值。
func NewDb(ctx context.Context, logger *zap.Logger, config *Config) (*gorm.DB, error) {
	if config == nil {
		return nil, fmt.Errorf("config required")
	}

	cfg := mysqldriver.NewConfig()
	cfg.User = config.User
	cfg.Passwd = config.Password
	cfg.Net = "tcp"
	cfg.Addr = config.Host
	cfg.DBName = config.Database
	cfg.Params = map[string]string{
		"charset":   "utf8mb4",
		"parseTime": "True",
		"loc":       "Local",
	}

	// 启用 TLS 时注册名为 "tidb" 的 TLS 配置
	if config.Tls != "" {
		cfg.Params["tls"] = "tidb"
		// 提取主机名（去掉端口号），TLS ServerName 应为纯域名/IP
		serverName := config.Host
		if host, _, err := net.SplitHostPort(config.Host); err == nil {
			serverName = host
		}
		// 最低要求 TLS 1.2
		mysqldriver.RegisterTLSConfig("tidb", &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: serverName,
		})
	}

	db, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{
		SkipDefaultTransaction:                   true,                 // 跳过默认事务
		PrepareStmt:                              true,                 // 启用预编译语句缓存
		CreateBatchSize:                          1000,                 // 批量创建每批 1000 条
		DisableForeignKeyConstraintWhenMigrating: true,                 // 迁移时不创建外键
		Logger:                                   &log{logger: logger}, // 自定义日志适配器
	})
	if err != nil {
		return nil, err
	}

	// 取底层 *sql.DB 以配置连接池
	gdb, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql.DB failed: %w", err)
	}
	// 连接池配置：优先使用自定义值，零值回退到默认值
	maxIdleTime := 5 * time.Minute
	if config.ConnMaxIdleMinute > 0 {
		maxIdleTime = time.Duration(config.ConnMaxIdleMinute) * time.Minute
	}
	maxLifetime := 1 * time.Hour
	if config.ConnMaxLifetimeMinute > 0 {
		maxLifetime = time.Duration(config.ConnMaxLifetimeMinute) * time.Minute
	}
	maxIdleConns := 20
	if config.MaxIdleConns > 0 {
		maxIdleConns = config.MaxIdleConns
	}
	maxOpenConns := 100
	if config.MaxOpenConns > 0 {
		maxOpenConns = config.MaxOpenConns
	}
	gdb.SetConnMaxIdleTime(maxIdleTime)
	gdb.SetConnMaxLifetime(maxLifetime)
	gdb.SetMaxIdleConns(maxIdleConns)
	gdb.SetMaxOpenConns(maxOpenConns)

	// Ping 验证连接可用
	if err := gdb.Ping(); err != nil {
		return nil, err
	}
	return db, err
}

// Close 关闭 db 底层的连接池；db 为 nil 时返回 nil。
// 适用于任意 *gorm.DB，包括 sharkrisingwave.New 返回的连接。关闭后不要再使用 db。
func Close(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get sql.DB failed: %w", err)
	}
	return sqlDB.Close()
}

// log 将 GORM 日志适配到 zap。
type log struct {
	logger *zap.Logger     // zap 日志记录器
	level  logger.LogLevel // GORM 日志级别
}

// LogMode 返回使用指定日志级别的新日志适配器。
func (z *log) LogMode(level logger.LogLevel) logger.Interface {
	return &log{level: level, logger: z.logger}
}

// Info 输出 Info 级别日志（不受 LogMode 级别过滤）。
func (z *log) Info(ctx context.Context, msg string, data ...any) {
	if z.logger == nil {
		return
	}
	z.logger.Info("SQL执行Info", zap.String("msg", msg), zap.Any("data", data))
}

// Warn 输出 Warn 级别日志（不受 LogMode 级别过滤）。
func (z *log) Warn(ctx context.Context, msg string, data ...any) {
	if z.logger == nil {
		return
	}
	z.logger.Warn("SQL执行Warn", zap.String("msg", msg), zap.Any("data", data))
}

// Error 输出 Error 级别日志（不受 LogMode 级别过滤）。
func (z *log) Error(ctx context.Context, msg string, data ...any) {
	if z.logger == nil {
		return
	}
	z.logger.Error("SQL执行Error", zap.String("msg", msg), zap.Any("data", data))
}

// Trace 记录 SQL 执行结果：出错时记录 Error 日志（忽略记录不存在和重复键冲突），
// 日志级别为 Info 及以上时额外记录每条 SQL 及耗时。
func (z *log) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	if z.logger == nil {
		return
	}

	var sql string
	var rows int64
	var sqlLoaded bool = false

	// 先过滤无需记录的错误
	if err != nil {
		// 查询无结果，非异常
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return
		}
		// MySQL 1062 重复键冲突由业务自行处理
		var mysqlErr *mysqldriver.MySQLError
		if errors.As(err, &mysqlErr) {
			if mysqlErr.Number == 1062 {
				return
			}
		}
		sql, rows = fc()
		sqlLoaded = true
		z.logger.Error("SQL执行失败", zap.String("sql", sql), zap.Int64("rows", rows), zap.Error(err))
	}

	if z.level >= logger.Info {
		elapsed := time.Since(begin)
		// 出错分支已取过 SQL 则不重复获取
		if !sqlLoaded {
			sql, rows = fc()
		}
		z.logger.Info("SQL执行日志", zap.String("sql", sql), zap.Duration("elapsed", elapsed))
	}
}
