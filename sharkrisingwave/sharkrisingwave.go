// Package sharkrisingwave 提供 RisingWave 数据库的 GORM 连接和日志适配器。
//
// RisingWave 是一款兼容 PostgreSQL 协议的流式数据库（Streaming Database），
// 支持物化视图（Materialized View）和实时数据处理。
// 本包通过 GORM + PostgreSQL 驱动的方式连接 RisingWave，并提供基于 zap 的结构化日志适配。
//
// 核心功能：
//  1. Config 结构体：声明式配置数据库连接参数（Host/User/Password/Database）
//  2. New 函数：创建 GORM 数据库连接实例，配置连接池和自定义日志
//  3. log 结构体：实现 GORM 的 logger.Interface，将 GORM 日志输出到 zap.Logger
//
// 连接池配置（默认值）：
//   - SetConnMaxIdleTime: 10 分钟（空闲连接最大存活时间）
//   - SetConnMaxLifetime: 1 小时（连接最大存活时间）
//   - SetMaxIdleConns: 10（最大空闲连接数）
//   - SetMaxOpenConns: 30（最大打开连接数）
//
// 注意：
//   - RisingWave 对某些 SQL 特性有局限，使用时需参考 RisingWave 官方文档
//   - GORM 自动迁移（AutoMigrate）可能不完全兼容 RisingWave，建议手动管理表结构
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

// Config 定义了 RisingWave 数据库的连接配置。
//
// 支持多种配置来源（json/yaml/mapstructure 标签），可通过配置文件、环境变量等灵活注入。
//
// 所有字段均为必填。
//
// 使用示例：
//
//	// YAML 配置文件读取
//	// config.yaml:
//	//   risingwave:
//	//     host: "localhost:4566"
//	//     user: "root"
//	//     password: "secret123"
//	//     database: "mydb"
//
//	var cfg sharkrisingwave.Config
//	// 通过 viper 或直接赋值填充
//	cfg = sharkrisingwave.Config{
//	    Host:     "risingwave-cluster:4566",
//	    User:     "root",
//	    Password: "mysecret",
//	    Database: "mydatabase",
//	}
//	logger, _ := zap.NewProduction()
//	db, err := sharkrisingwave.New(context.Background(), logger, &cfg)
//	if err != nil {
//	    panic(err)
//	}
type Config struct {
	Host     string `json:"host" yaml:"host" mapstructure:"host"`             // RisingWave 服务地址，格式："host:port"（例如 "localhost:4566"）
	User     string `json:"user" yaml:"user" mapstructure:"user"`             // 数据库登录用户名
	Password string `json:"password" yaml:"password" mapstructure:"password"` // 数据库登录密码
	Database string `json:"database" yaml:"database" mapstructure:"database"` // 目标数据库名称
}

// New 创建并返回一个配置好的 GORM 数据库连接实例。
//
// 参数：
//   - ctx: 上下文（用于初始 Ping 检测）
//   - logger: zap 日志记录器（必填，所有 GORM SQL 日志会通过 zap 输出）
//   - config: 数据库连接配置（必填，为 nil 时返回错误）
//
// 连接配置：
//   - 自动对 User/Password 进行 URL 转义，避免特殊字符导致 DSN 解析失败
//   - 使用自定义 zap 日志适配器（log 结构体），所有 SQL 执行日志统一输出到 zap
//   - 配置连接池参数：空闲超时 10 分钟、连接生命周期 1 小时、最大空闲 10、最大开放 30
//   - 创建连接后执行 Ping 验证连通性，失败则返回错误
//
// 返回值：
//   - *gorm.DB: GORM 数据库实例，可用于查询、事务、模型操作
//   - error: 创建失败时返回错误（配置为 nil / 连接失败 / Ping 失败）
//
// 使用示例：
//
//	logger, _ := zap.NewProduction()
//	defer logger.Sync()
//
//	cfg := &sharkrisingwave.Config{
//	    Host:     "localhost:4566",
//	    User:     "root",
//	    Password: "password",
//	    Database: "mydb",
//	}
//
//	db, err := sharkrisingwave.New(context.Background(), logger, cfg)
//	if err != nil {
//	    log.Fatalf("连接 RisingWave 失败: %v", err)
//	}
//
//	// 执行查询
//	type Order struct {
//	    ID     int64
//	    Amount float64
//	}
//	var orders []Order
//	db.Table("orders").Where("amount > ?", 100).Find(&orders)
//
//	// 执行原生 SQL
//	db.Exec("CREATE MATERIALIZED VIEW mv_orders AS SELECT ...")
func New(ctx context.Context, logger *zap.Logger, config *Config) (*gorm.DB, error) {
	if config == nil {
		return nil, fmt.Errorf("config required")
	}
	// 构建 PostgreSQL 协议 DSN：使用 url.QueryEscape 转义用户名和密码中的特殊字符
	dsn := fmt.Sprintf(
		"postgres://%s:%s@%s/%s?sslmode=disable",
		url.QueryEscape(config.User),
		url.QueryEscape(config.Password),
		config.Host,
		config.Database,
	)
	// 使用 GORM 的 postgres 驱动打开连接，并注入自定义日志适配器
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: &log{
			logger: logger,
		},
	})
	if err != nil {
		return nil, err
	}
	// 获取底层 *sql.DB 实例以配置连接池
	gdb, _ := db.DB()
	gdb.SetConnMaxIdleTime(10 * time.Minute) // 空闲连接最长存活 10 分钟
	gdb.SetConnMaxLifetime(1 * time.Hour)    // 连接最长存活 1 小时
	gdb.SetMaxIdleConns(10)                  // 最大空闲连接数 10
	gdb.SetMaxOpenConns(30)                  // 最大打开连接数 30
	// 验证连接可用性
	if err := gdb.Ping(); err != nil {
		return nil, err
	}
	return db, err
}

// log 实现了 GORM 的 logger.Interface 接口，将 GORM SQL 日志桥接到 zap.Logger。
//
// 日志过滤规则：
//   - Info/Warn/Error 级别：所有日志均输出到 zap（带 "msg" 和 "data" 字段）
//   - Trace 级别：
//     1. 忽略 gorm.ErrRecordNotFound（记录未找到属于正常业务逻辑）
//     2. 忽略 PostgreSQL 错误码 23505（唯一约束冲突 / duplicate key，属于正常业务逻辑）
//     3. 执行失败的 SQL 输出 Error 级别日志
//     4. 当 GORM 日志级别 >= Info 时，输出执行成功的 SQL 及其耗时
//
// 注意：该结构体未导出，仅供包内 New 函数内部使用。
type log struct {
	logger *zap.Logger     // zap 日志记录器实例
	level  logger.LogLevel // GORM 日志级别（从 GORM 的 LogMode 配置传入）
}

// LogMode 设置 GORM 日志级别，返回新的 logger.Interface 实例。
// 该方法遵循 GORM logger.Interface 接口规范。
func (z *log) LogMode(level logger.LogLevel) logger.Interface {
	return &log{level: level, logger: z.logger}
}

// Info 输出 GORM 的 Info 级别日志到 zap.Logger。
// 所有 Info 日志带有 "msg" 和 "data" 字段。
func (z *log) Info(ctx context.Context, msg string, data ...any) {
	if z.logger == nil {
		return
	}
	z.logger.Info("RW执行Info", zap.String("msg", msg), zap.Any("data", data))
}

// Warn 输出 GORM 的 Warn 级别日志到 zap.Logger。
// 所有 Warn 日志带有 "msg" 和 "data" 字段。
func (z *log) Warn(ctx context.Context, msg string, data ...any) {
	if z.logger == nil {
		return
	}
	z.logger.Warn("RW执行Warn", zap.String("msg", msg), zap.Any("data", data))
}

// Error 输出 GORM 的 Error 级别日志到 zap.Logger。
// 所有 Error 日志带有 "msg" 和 "data" 字段。
func (z *log) Error(ctx context.Context, msg string, data ...any) {
	if z.logger == nil {
		return
	}
	z.logger.Error("RW执行Error", zap.String("msg", msg), zap.Any("data", data))
}

// Trace 是 GORM SQL 执行的追踪回调，在每次 SQL 执行后被调用。
//
// 参数：
//   - ctx: 请求上下文
//   - begin: SQL 开始执行的时间
//   - fc: 延迟调用函数，返回 (SQL 语句, 影响行数)
//   - err: SQL 执行的错误（nil 表示成功）
//
// 日志过滤逻辑：
//  1. 忽略 gorm.ErrRecordNotFound — 查询无结果属于正常业务，不记录错误
//  2. 忽略 PostgreSQL 错误码 23505 — 唯一约束冲突/重复键，属于正常业务（如幂等插入）
//  3. 其他错误以 Error 级别输出完整的 SQL、影响行数和错误信息
//  4. 当 GORM 日志级别 >= Info 时，输出成功 SQL 的执行耗时（用于慢查询分析）
//
// 注意：fc() 采用延迟调用，仅在需要时才获取 SQL 字符串，避免不必要的字符串拼接开销。
func (z *log) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	if z.logger == nil {
		return
	}
	var sql string
	var rows int64
	var sqlLoaded bool = false
	if err != nil {
		// 忽略「记录未找到」：正常业务逻辑（如 First() 未找到记录）
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return
		}
		// 忽略「唯一约束冲突」：PostgreSQL 错误码 23505 = unique_violation
		// 常见于幂等插入或并发创建场景，属于正常业务逻辑
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			if pgErr.Code == "23505" {
				return
			}
		}
		// 真实错误：获取 SQL 并记录 Error 级别日志
		sql, rows = fc()
		sqlLoaded = true
		z.logger.Error("RW执行失败", zap.String("sql", sql), zap.Int64("rows", rows), zap.Error(err))
	}
	// 当 GORM 日志级别 >= Info 时，记录成功 SQL 的执行耗时
	if z.level >= logger.Info {
		elapsed := time.Since(begin)
		if !sqlLoaded {
			sql, rows = fc()
		}
		z.logger.Info("RW执行日志", zap.String("sql", sql), zap.Int64("rows", rows), zap.Duration("elapsed", elapsed))
	}
}
