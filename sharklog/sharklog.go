// Package sharklog 提供同时输出到控制台和 Kafka 的 zap 日志器。
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

	snowFlake := sharksnowflake.New()

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
	// Zap 日志器，同时写入控制台和（设置 Writer 后的）Kafka。
	Zap    *zap.Logger
	writer *logWriter
}

// SetKafkaWriter 设置 Kafka Writer，启用远程日志推送；已设置过则忽略。
func (s *SharkLog) SetKafkaWriter(writer *kafka.Writer) {
	s.writer.locker.Lock()
	defer s.writer.locker.Unlock()
	if s.writer.writer != nil {
		return
	}
	s.writer.writer = writer
}

// Close 关闭 Kafka Writer，调用前应确保业务日志已全部写出；之后日志只输出到控制台。
func (s *SharkLog) Close() {
	// 先刷出 zap 缓冲；终端和管道对 stdout 的 Sync 常返回错误，可忽略。
	_ = s.Zap.Sync()
	s.writer.locker.Lock()
	defer s.writer.locker.Unlock()
	if s.writer.writer == nil {
		return
	}
	_ = s.writer.writer.Close()
	s.writer.writer = nil
}

// logWriter 是 zap 的 Kafka 输出端，把每条 JSON 日志包装后写入 Kafka；Writer 为空时丢弃。
type logWriter struct {
	ctx       context.Context
	writer    *kafka.Writer
	snowFlake *sharksnowflake.Snowflake
	hostName  string
	name      string
	id        string
	locker    sync.Mutex
}

// Write 将一条日志包装（附 log_id、服务实例名、主机名）后写入 Kafka。
// 未配置 Writer 时直接忽略；写入失败时返回错误给 zap。
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
			return 0, e
		}
		err := w.writer.WriteMessages(w.ctx, kafka.Message{Value: b})
		if err != nil {
			return 0, err
		}
	}
	return len(p), nil
}
