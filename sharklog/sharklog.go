/*
	AI 代码审查
	请重点检查逻辑错误、潜在 Bug、边界条件、并发安全及资源泄漏等实质性问题。
	不要过度审查，不要吹毛求疵，不要纠结代码风格、命名或个人编码习惯，也不要提出无关的重构和优化建议。
	只报告有明确依据、可能导致实际故障的问题，并说明原因和触发条件。
	如果没有发现明显问题，直接说明：未发现明显问题。
*/
// Package sharklog 提供输出到控制台和 Kafka 的 zap 日志器。
package sharklog

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/xcodego/shark/sharksnowflake"

	"github.com/bytedance/sonic"
	"github.com/segmentio/kafka-go"
	"github.com/spf13/cast"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// New 创建日志器，日志输出到控制台；设置 Kafka Writer 后也会推送到 Kafka。
// name 和 id 用于组成 Kafka 消息中的服务实例名。
func New(ctx context.Context, name string, id string) *SharkLog {
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
		enc.AppendString(t.Format("2006-01-02 15:04:05.000"))
	}
	encoderConfig.CallerKey = "caller"
	encoderConfig.EncodeCaller = zapcore.ShortCallerEncoder
	encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder

	consoleEncoder := zapcore.NewConsoleEncoder(encoderConfig)
	consoleCore := zapcore.NewCore(consoleEncoder, zapcore.AddSync(os.Stdout), zapcore.DebugLevel)

	hostName, err := os.Hostname()
	if err != nil {
		hostName = "unknown"
	}

	snowFlake := sharksnowflake.NewSnowflake()

	lwriter := &logWriter{
		ctx:       ctx,
		name:      name,
		id:        id,
		hostName:  hostName,
		snowFlake: snowFlake,
	}

	logcore := zapcore.NewCore(zapcore.NewJSONEncoder(encoderConfig), zapcore.AddSync(lwriter), zapcore.DebugLevel)
	core := zapcore.NewTee(consoleCore, logcore)

	logger := zap.New(core, zap.AddCaller())

	return &SharkLog{
		Zap:    logger,
		writer: lwriter,
	}
}

// SharkLog 包装 zap.Logger，并支持将日志推送到 Kafka。
type SharkLog struct {
	Zap    *zap.Logger
	writer *logWriter
}

// SetKafkaWriter 设置 Kafka Writer，启用远程日志推送。
func (s *SharkLog) SetKafkaWriter(writer *kafka.Writer) {
	s.writer.locker.Lock()
	defer s.writer.locker.Unlock()
	if s.writer.writer != nil {
		return
	}
	s.writer.writer = writer
}

// Close 关闭,业务确保所有日志都写入了,再关闭
func (s *SharkLog) Close() {
	s.writer.locker.Lock()
	defer s.writer.locker.Unlock()
	if s.writer.writer == nil {
		return
	}
	_ = s.writer.writer.Close()
	s.writer.writer = nil
}

type logWriter struct {
	ctx       context.Context
	writer    *kafka.Writer
	snowFlake *sharksnowflake.Snowflake
	hostName  string
	name      string
	id        string
	locker    sync.Mutex
}

// Write 将日志写入 Kafka；未配置 Writer 时忽略，写入失败时输出错误但不返回。
func (w *logWriter) Write(p []byte) (n int, err error) {
	w.locker.Lock()
	defer w.locker.Unlock()

	if w.writer != nil {
		data := map[string]any{
			"log_id":      cast.ToString(w.snowFlake.Generate()),
			"server_name": fmt.Sprintf("%v-%v", w.name, w.id),
			"server_host": w.hostName,
			"msg":         string(p),
		}
		b, e := sonic.Marshal(data)
		if e != nil {
			fmt.Println("日志序列化失败", e, "日志内容", string(p))
			return len(p), e
		}
		err := w.writer.WriteMessages(w.ctx, kafka.Message{Value: b})
		if err != nil {
			fmt.Println("日志写入Kafka失败", err, "日志内容", string(p))
		}
	}
	return len(p), nil
}
