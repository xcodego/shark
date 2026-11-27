// Package sharkkafka 提供 Kafka 客户端封装：SASL/SCRAM-SHA-512 与 TLS 连接、带缓存的 Writer 以及批量消费。
package sharkkafka

import (
	"context"
	"crypto/tls"
	"fmt"
	"sync"
	"time"

	"github.com/panjf2000/ants/v2"
	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl/scram"
	"github.com/xcodego/shark/sharkutils"
	"go.uber.org/zap"
)

// Config 是 Kafka 的连接配置。
type Config struct {
	Host     []string `json:"host" yaml:"host" mapstructure:"host"`             // Broker 地址
	User     string   `json:"user" yaml:"user" mapstructure:"user"`             // SASL 用户名；与密码都非空才启用 SCRAM-SHA-512 认证
	Password string   `json:"password" yaml:"password" mapstructure:"password"` // SASL 密码
	TLS      bool     `json:"tls" yaml:"tls" mapstructure:"tls"`                // 是否启用 TLS
}

// Client 管理按 topic 缓存的 Kafka Writer 和共享的连接配置。请使用 New 创建。
type Client struct {
	ctx     context.Context
	config  *Config
	writers map[string]*kafka.Writer
	lock    sync.Mutex
	dialer  *kafka.Dialer
	logger  *zap.Logger
	antpool *ants.Pool
}

// New 创建 Kafka 客户端；config 不能为 nil。
// ctx 控制 BatchConsumer 的生命周期，取消后所有批量消费退出。
// TLS 最低 TLS 1.2；SASL 与 TLS 可独立组合，均未启用时 dialer 为 nil（使用 kafka-go 默认拨号器）。
func New(ctx context.Context, config *Config, logger *zap.Logger) (*Client, error) {
	if config == nil {
		return nil, fmt.Errorf("config required")
	}

	var tlsConfig *tls.Config
	if config.TLS {
		tlsConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
	}

	var dialer *kafka.Dialer
	hasSASL := config.User != "" && config.Password != ""
	hasTLS := tlsConfig != nil

	switch {
	case hasSASL && hasTLS:
		mechanism, err := scram.Mechanism(scram.SHA512, config.User, config.Password)
		if err != nil {
			return nil, err
		}
		dialer = &kafka.Dialer{
			Timeout:       10 * time.Second,
			SASLMechanism: mechanism,
			TLS:           tlsConfig,
		}
	case hasSASL && !hasTLS:
		mechanism, err := scram.Mechanism(scram.SHA512, config.User, config.Password)
		if err != nil {
			return nil, err
		}
		dialer = &kafka.Dialer{
			Timeout:       10 * time.Second,
			SASLMechanism: mechanism,
			TLS:           nil,
		}
	case !hasSASL && hasTLS:
		dialer = &kafka.Dialer{
			Timeout: 10 * time.Second,
			TLS:     tlsConfig,
		}
	default:
		dialer = nil
	}
	antpool, err := ants.NewPool(10)
	if err != nil {
		return nil, err
	}
	return &Client{
		ctx:     ctx,
		config:  config,
		writers: make(map[string]*kafka.Writer),
		dialer:  dialer,
		logger:  logger,
		antpool: antpool,
	}, nil
}

// WriterConfig 配置 Kafka Writer，字段为 nil 时使用默认值。
// 默认：BatchSize 10000、BatchBytes 2MB、BatchTimeout 1s、RequiredAcks 为 RequireOne、Async 为 true。
type WriterConfig struct {
	BatchSize    *int
	BatchBytes   *int
	BatchTimeout *time.Duration
	RequiredAcks *kafka.RequiredAcks
	Async        *bool
}

// Writer 返回 topic 对应的 Writer，首次调用时创建并缓存；已缓存时忽略 cfg。
// 使用 Hash 分区策略，相同 key 的消息进入同一分区。
func (s *Client) Writer(topic string, cfg *WriterConfig) (*kafka.Writer, error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	if writer, ok := s.writers[topic]; ok {
		return writer, nil
	}
	if cfg == nil {
		cfg = &WriterConfig{
			BatchSize:    sharkutils.Pointer(10000),
			BatchBytes:   sharkutils.Pointer(1024 * 1024 * 2),
			BatchTimeout: sharkutils.Pointer(time.Second),
			RequiredAcks: sharkutils.Pointer(kafka.RequireOne),
			Async:        sharkutils.Pointer(true),
		}
	}
	if cfg.BatchSize == nil {
		cfg.BatchSize = sharkutils.Pointer(10000)
	}
	if cfg.BatchBytes == nil {
		cfg.BatchBytes = sharkutils.Pointer(1024 * 1024 * 2)
	}
	if cfg.BatchTimeout == nil {
		cfg.BatchTimeout = sharkutils.Pointer(time.Second)
	}
	if cfg.RequiredAcks == nil {
		cfg.RequiredAcks = sharkutils.Pointer(kafka.RequireOne)
	}
	if cfg.Async == nil {
		cfg.Async = sharkutils.Pointer(true)
	}
	writerConfig := kafka.WriterConfig{
		Brokers:      s.config.Host,
		Topic:        topic,
		Dialer:       s.dialer,
		Balancer:     &kafka.Hash{},
		BatchSize:    *cfg.BatchSize,
		BatchBytes:   *cfg.BatchBytes,
		BatchTimeout: *cfg.BatchTimeout,
		RequiredAcks: int(*cfg.RequiredAcks),
		Async:        *cfg.Async,
	}
	writer := kafka.NewWriter(writerConfig)
	s.writers[topic] = writer
	return writer, nil
}

// CloseWriter 关闭并移除指定 topic 的 Writer；topic 不存在时返回 nil。
func (s *Client) CloseWriter(topic string) error {
	s.lock.Lock()
	writer, ok := s.writers[topic]
	if ok {
		delete(s.writers, topic)
	}
	s.lock.Unlock()
	if ok {
		return writer.Close()
	}
	return nil
}

// Close 关闭所有 Writer（异步 Writer 会先刷出缓冲）并清空缓存，同时释放内部协程池，返回遇到的第一个错误。
// 重复调用是安全的。BatchConsumer 由创建时传入的 ctx 控制退出，不在这里停止。
func (s *Client) Close() error {
	s.lock.Lock()
	writers := s.writers
	s.writers = make(map[string]*kafka.Writer)
	s.lock.Unlock()
	var firstErr error
	for _, writer := range writers {
		if err := writer.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.antpool != nil {
		s.antpool.Release()
	}
	return firstErr
}

// ReaderConfig 配置 Kafka Reader，字段为 nil 时使用默认值。
// 默认：MinBytes 1、MaxBytes 10MB、StartOffset 为 FirstOffset（仅对新消费组生效）。
type ReaderConfig struct {
	MinBytes    *int
	MaxBytes    *int
	StartOffset *int64
}

// Reader 为 topic 和消费组创建 Kafka Reader，调用方负责关闭。
func (s *Client) Reader(topic string, group string, cfg *ReaderConfig) *kafka.Reader {
	if cfg == nil {
		cfg = &ReaderConfig{
			MinBytes:    sharkutils.Pointer(1),
			MaxBytes:    sharkutils.Pointer(10 * 1024 * 1024),
			StartOffset: sharkutils.Pointer(kafka.FirstOffset),
		}
	}
	if cfg.MinBytes == nil {
		cfg.MinBytes = sharkutils.Pointer(1)
	}
	if cfg.MaxBytes == nil {
		cfg.MaxBytes = sharkutils.Pointer(10 * 1024 * 1024)
	}
	if cfg.StartOffset == nil {
		cfg.StartOffset = sharkutils.Pointer(kafka.FirstOffset)
	}
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:           s.config.Host,
		Topic:             topic,
		GroupID:           group,
		MinBytes:          *cfg.MinBytes,
		MaxBytes:          *cfg.MaxBytes,
		Dialer:            s.dialer,
		StartOffset:       *cfg.StartOffset,
		SessionTimeout:    time.Minute,
		RebalanceTimeout:  time.Minute,
		QueueCapacity:     10000,
		HeartbeatInterval: 10 * time.Second,
	})
	return reader
}

// BatchConfig 配置批量消费行为，字段为 nil 时使用默认值。
// BatchSize 为每批最多条数，默认 10000；Timeout 为读到首条后继续凑批的最长等待，默认 0，
// 即取走通道中已有的消息就回调，不额外等待。
type BatchConfig struct {
	ReaderConfig
	BatchSize *int
	Timeout   *time.Duration
}

// BatchConsumer 阻塞式批量消费消息，直到 ctx 取消、handler 返回 false 或 offset 提交最终失败。
//
// 每批消息交给 handler 处理，handler 返回 true 后才提交 offset（至少一次语义），
// handler panic 会被恢复并按返回 false 处理；ctx 取消时 handler 可能收到空切片。
func (s *Client) BatchConsumer(topic string, group string, cfg *BatchConfig, handler func([]kafka.Message) bool) {
	if cfg == nil {
		cfg = &BatchConfig{
			ReaderConfig: ReaderConfig{
				MinBytes:    sharkutils.Pointer(1),
				MaxBytes:    sharkutils.Pointer(10 * 1024 * 1024),
				StartOffset: sharkutils.Pointer(kafka.FirstOffset),
			},
			BatchSize: sharkutils.Pointer(10000),
			Timeout:   sharkutils.Pointer(time.Duration(0)),
		}
	}
	if cfg.MinBytes == nil {
		cfg.MinBytes = sharkutils.Pointer(1)
	}
	if cfg.MaxBytes == nil {
		cfg.MaxBytes = sharkutils.Pointer(10 * 1024 * 1024)
	}
	if cfg.StartOffset == nil {
		cfg.StartOffset = sharkutils.Pointer(kafka.FirstOffset)
	}
	if cfg.BatchSize == nil {
		cfg.BatchSize = sharkutils.Pointer(10000)
	}
	if cfg.Timeout == nil {
		cfg.Timeout = sharkutils.Pointer(time.Duration(0))
	}
	reader := s.Reader(topic, group, &cfg.ReaderConfig)
	batchSize := *cfg.BatchSize
	// 拉取协程（本函数）与处理协程之间的缓冲，容量为两批，让拉取与处理并行
	channel := make(chan kafka.Message, batchSize*2)
	defer func() {
		close(channel)
		reader.Close()
	}()
	running, runningCalcel := context.WithCancel(s.ctx)
	defer runningCalcel()

	safeHandler := func(msgs []kafka.Message) (result bool) {
		defer func() {
			if r := recover(); r != nil {
				s.logger.Error("Consumer handler panic", zap.Any("err", r))
				result = false
			}
		}()
		return handler(msgs)
	}

	// 处理协程：攒批 → handler → 提交 offset
	go func() {
		for {
			messages := sharkutils.DrainChannelN(running, channel, batchSize, *cfg.Timeout)
			result := safeHandler(messages)
			if !result {
				s.logger.Warn("Consumer handler returned false, stop consuming", zap.String("topic", topic), zap.String("group", group))
				runningCalcel()
				return
			}
			// 提交失败每秒重试，次数上限极大，实际等同于一直重试直到 ctx 取消
			var commitError error
			for i := 0; i < 50000000; i++ {
				commitError = reader.CommitMessages(running, messages...)
				if commitError == nil {
					break
				}
				s.logger.Warn("提交 Kafka 消息 offset 失败", zap.String("topic", reader.Config().Topic), zap.Error(commitError), zap.Int("retry", i+1))
				time.Sleep(time.Second)
				if running.Err() != nil {
					return
				}
			}
			if commitError != nil {
				runningCalcel()
				s.logger.Error("提交 Kafka 消息 offset 失败 panic", zap.String("topic", reader.Config().Topic), zap.Error(commitError))
				return
			}
			if running.Err() != nil {
				return
			}
		}
	}()

	// fetchMessages 拉取一条消息投入通道；返回 false 表示应退出。读取出错时等待 1 秒后重试。
	var fetchMessages = func(ctx context.Context) bool {
		msg, err := reader.FetchMessage(ctx)
		if err == nil {
			select {
			case channel <- msg:
			case <-ctx.Done():
				return false
			}
			return true
		}
		if ctx.Err() != nil {
			return false
		}
		s.logger.Error("读取 Kafka 消息失败", zap.String("topic", reader.Config().Topic), zap.Error(err))
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
			return false
		}
		return true
	}

	for {
		select {
		case <-running.Done():
			return
		default:
			if !fetchMessages(running) {
				return
			}
		}
	}
}
