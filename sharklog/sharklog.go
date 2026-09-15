// Package sharklog 提供基于 zap 的双通道日志系统（控制台输出 + Kafka 远程推送）。
//
// 核心设计：
//  1. 双通道输出：日志同时输出到控制台（Console Encoder）和 Kafka（JSON Encoder）
//  2. 控制台格式：可读的时间格式 "2006-01-02 15:04:05.000" + 短调用者路径 + 级别缩写
//  3. Kafka 格式：结构化的 JSON，包含 log_id（Snowflake 唯一ID）、server_name、server_host、msg
//  4. 延迟初始化：Kafka Writer 通过 SetKafkaWriter 按需注入，未注入时仅控制台输出
//  5. 优雅退出：检测到 "server exit" 日志时自动关闭 Kafka Writer
//  6. 容错设计：Kafka 写入失败仅输出到控制台，不阻塞业务日志
//
// 日志格式（控制台）：
//
//	2025-06-18 01:30:45.123	INFO	sharkapp/app.go:42	服务启动成功
//
// 日志格式（Kafka JSON）：
//
//	{
//	  "log_id": "78451234567890",
//	  "server_name": "myproject-instance-1",
//	  "server_host": "pod-abc123",
//	  "msg": "{\"L\":\"INFO\",\"T\":\"2025-06-18T01:30:45.123+0800\",\"C\":\"sharkapp/app.go:42\",\"M\":\"服务启动成功\"}"
//	}
//
// 使用示例：
//
//	// 创建日志器
//	log := sharklog.New(ctx, "myproject", "instance-1")
//
//	// 可选：设置 Kafka Writer 启用远程日志推送
//	kw := &kafka.Writer{
//	    Addr:     kafka.TCP("kafka:9092"),
//	    Topic:    "app-logs",
//	    Balancer: &kafka.LeastBytes{},
//	}
//	log.SetKafkaWriter(kw)
//
//	// 使用 zap 原生方式记录日志
//	log.Zap.Info("服务启动成功")
//	log.Zap.Info("处理请求", zap.String("user_id", "12345"))
//	log.Zap.Error("数据库连接失败", zap.Error(err))
package sharklog

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/lornshark/shark/sharksnowflake"

	"github.com/bytedance/sonic"
	"github.com/segmentio/kafka-go"
	"github.com/spf13/cast"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// New 创建一个双通道 SharkLog 日志器实例。
//
// 参数：
//   - ctx:  上下文，用于控制 Kafka Writer 优雅关闭（ctx 取消 + "server exit" 日志触发 Close）
//   - name: 服务名/项目名，作为 Kafka 消息中 server_name 的前缀
//   - id:   实例标识，与 name 组合为 "name-id" 格式的完整 server_name
//
// 日志输出配置：
//   - 控制台：Console Encoder，本地时间格式 "2006-01-02 15:04:05.000"，DebugLevel 全量输出
//   - Kafka： JSON Encoder，DebugLevel 全量输出（需通过 SetKafkaWriter 注入 Writer 才真正推送）
//   - 两者通过 zapcore.NewTee 合并，日志同时写入两处
//
// 使用示例：
//
//	ctx := context.Background()
//	log := sharklog.New(ctx, "order-service", "pod-1")
//
//	// 基础日志
//	log.Zap.Info("订单服务启动")
//	log.Zap.Debug("调试信息", zap.Any("config", cfg))
//
//	// 结构化日志
//	log.Zap.Info("订单创建",
//	    zap.String("order_id", "ORD-20250618-001"),
//	    zap.Float64("amount", 99.99),
//	)
//
//	// 错误日志
//	log.Zap.Error("支付失败", zap.Error(err))
func New(ctx context.Context, name string, id string) *SharkLog {
	// 配置编码器：自定义时间格式、短路径调用者、级别缩写
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
		enc.AppendString(t.Format("2006-01-02 15:04:05.000")) // 可读的时间格式（含毫秒）
	}
	encoderConfig.CallerKey = "caller"                      // 日志调用者字段名
	encoderConfig.EncodeCaller = zapcore.ShortCallerEncoder // 短路径（包/文件:行号）
	encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder // 级别大写缩写（INFO/WARN/ERROR）

	// 控制台编码器：Console Encoder + 标准输出 + DebugLevel
	consoleEncoder := zapcore.NewConsoleEncoder(encoderConfig)
	consoleCore := zapcore.NewCore(consoleEncoder, zapcore.AddSync(os.Stdout), zapcore.DebugLevel)

	// 获取主机名用于 Kafka 消息的 server_host 字段
	hostName, _ := os.Hostname()

	// 创建 Snowflake 实例，为每条日志生成全局唯一 log_id
	snowFlake := sharksnowflake.NewSnowflake()

	// Kafka 写入器（logWriter 实现 zapcore.WriteSyncer 接口）
	lwriter := &logWriter{
		ctx:       ctx,
		name:      name,
		id:        id,
		hostName:  hostName,
		snowFlake: snowFlake,
	}

	// Kafka 编码器：JSON Encoder + logWriter + DebugLevel
	// 注意：logWriter.Write 内部检查 writer 是否为 nil，为 nil 时静默丢弃
	redisCore := zapcore.NewCore(zapcore.NewJSONEncoder(encoderConfig), zapcore.AddSync(lwriter), zapcore.DebugLevel)

	// 合并双通道：控制台 + Kafka，日志同时写入两个 Core
	core := zapcore.NewTee(consoleCore, redisCore)

	// 创建 zap.Logger，AddCaller 启用调用者信息（文件名+行号）
	logger := zap.New(core, zap.AddCaller())

	return &SharkLog{
		Zap:    logger,
		writer: lwriter,
	}
}

// SharkLog 是对 zap.Logger 的包装，提供控制台 + Kafka 双通道日志能力。
//
// 字段说明：
//   - Zap:   可直接使用的 zap.Logger 实例，业务方通过此字段调用 Info/Warn/Error 等方法
//   - writer: 内部 Kafka 日志写入器（未导出），通过 SetKafkaWriter 注入
//
// 使用方式：业务方统一通过 SharkLog.Zap 记录日志，无需区分输出目标。
//
// 注意：如果未调用 SetKafkaWriter，日志仅输出到控制台，不会产生副作用。
type SharkLog struct {
	Zap    *zap.Logger // zap 日志器实例，业务方直接通过此字段使用
	writer *logWriter  // 内部 Kafka 日志写入器（未导出）
}

// SetKafkaWriter 设置 Kafka Writer 以启用远程日志推送。
//
// 参数：
//   - writer: Kafka Writer 实例（如 &kafka.Writer{Addr: ..., Topic: ...}）
//
// 调用时机：在 New() 之后、业务日志写入之前调用。
// 可多次调用以更换 Writer（如重连后更新）。
//
// 使用示例：
//
//	log := sharklog.New(ctx, "my-service", "inst-1")
//
//	// 配置 Kafka Writer
//	kw := &kafka.Writer{
//	    Addr:     kafka.TCP("kafka-broker:9092"),
//	    Topic:    "app-logs",
//	    Balancer: &kafka.LeastBytes{},
//	    BatchSize: 100,
//	    BatchTimeout: 500 * time.Millisecond,
//	}
//	log.SetKafkaWriter(kw)
//
//	// 此后所有日志将同时输出到控制台和 Kafka
//	log.Zap.Info("日志推送已启用")
func (s *SharkLog) SetKafkaWriter(writer *kafka.Writer) {
	s.writer.writer = writer
}

// Close 优雅关闭日志组件，关闭 Kafka Writer 连接。
//
// 应在应用退出前调用，确保最后一条日志成功推送到 Kafka。
func (s *SharkLog) Close() {
	if s.writer != nil && s.writer.writer != nil {
		s.writer.writer.Close()
		s.writer.writer = nil // 避免重复关闭
	}
}

// logWriter 实现 zapcore.WriteSyncer 接口，将日志写入 Kafka。
//
// 每条日志被包装为结构化的 JSON 消息，包含：
//   - log_id:      Snowflake 生成的全局唯一 ID（便于日志追踪和去重）
//   - server_name: "name-id" 格式的服务实例标识
//   - server_host: 主机名（从 os.Hostname() 获取）
//   - msg:         原始日志内容（zap JSON 编码后的字符串）
//
// 注意：该结构体未导出，仅供包内 New 函数内部使用。
type logWriter struct {
	ctx       context.Context           // 上下文，用于检测优雅退出
	writer    *kafka.Writer             // Kafka Writer 实例（nil 时静默丢弃日志）
	snowFlake *sharksnowflake.Snowflake // Snowflake 实例，生成 log_id
	hostName  string                    // 主机名，用于 server_host 字段
	name      string                    // 服务名，用于 server_name 前缀
	id        string                    // 实例 ID，与 name 组合为 server_name
}

// Write 实现 io.Writer 接口，由 zap Core 在每次日志写入时调用。
//
// 日志处理流程：
//  1. 检查 Kafka Writer 是否为 nil，为 nil 时直接返回（仅控制台输出）
//  2. 构造结构化消息：log_id（Snowflake）+ server_name + server_host + msg
//  3. 使用 sonic 快速序列化为 JSON
//  4. 写入 Kafka（非阻塞，失败时输出到控制台 stderr）
//  5. 检测优雅退出：当 ctx.Done() 关闭 且 日志内容包含 "server exit" 时，关闭 Kafka Writer
//
// 返回值始终为 len(p), nil（即使 Kafka 写入失败也不返回错误，不影响 zap 的正常流程）。
//
// 参数：
//   - p: zap 编码后的日志字节（JSON 格式，包含 L/T/C/M 字段）
//
// 注意：
//   - Kafka 写入失败时仅通过 fmt.Println 输出到控制台，不返回错误 —— 这是刻意设计，防止 Kafka 故障阻塞业务日志
//   - ctx.Done() + "server exit" 检测用于实现优雅关闭：服务退出时发送最后一条日志后自动关闭 Writer
func (w *logWriter) Write(p []byte) (n int, err error) {
	if w.writer != nil {
		// 构造结构化日志消息
		data := map[string]any{
			"log_id":      cast.ToString(w.snowFlake.Generate()), // 全局唯一日志 ID
			"server_name": fmt.Sprintf("%v-%v", w.name, w.id),    // 服务实例标识
			"server_host": w.hostName,                            // 主机名
			"msg":         string(p),                             // 原始日志内容
		}
		// 使用 sonic 进行高性能 JSON 序列化
		b, _ := sonic.Marshal(data)
		// 写入 Kafka（5 秒超时，避免 Kafka 不可用时阻塞日志）
		err := w.writer.WriteMessages(context.Background(), kafka.Message{Value: b})
		if err != nil {
			// Kafka 写入失败：降级输出到控制台，不阻塞业务日志
			fmt.Println("日志写入Kafka失败", err, "日志内容", string(p))
		}
	}
	// 始终返回成功（len(p), nil），保证 zap 日志流程不被中断
	return len(p), nil
}
