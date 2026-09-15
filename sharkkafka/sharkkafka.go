// Package sharkkafka 提供 Kafka 生产者和消费者的统一封装。
//
// 核心设计：
//  1. 连接管理：通过 Config 统一配置 Broker 地址、SASL 认证、TLS 加密，自动创建 Dialer
//  2. Writer 池化：按 topic 缓存 Writer 实例，避免重复创建，支持线程安全读写
//  3. 批量生产者：内置批量发送优化（BatchSize=10000, BatchBytes=2MB, BatchTimeout=1s）
//  4. 批量消费者：内置批量拉取 + 管道缓冲 + 自动提交 offset，支持优雅退出
//  5. 容错设计：生产者使用 RequireOne 确认（性能优先），消费者提交失败重试 5 次
//  6. 安全认证：支持 SASL/SCRAM-SHA512 认证 + TLS 加密
//
// 使用场景：
//   - 消息队列：服务间异步解耦通信
//   - 事件溯源：记录业务事件到 Kafka 供下游消费
//   - 日志收集：将应用日志推送到 Kafka（配合 sharklog 使用）
//   - 实时流处理：作为 RisingWave / Flink 等流处理系统的数据源
//
// 使用示例：
//
//	// 1. 创建 SharkKafka 实例
//	cfg := &sharkkafka.Config{
//	    Host:     []string{"kafka-broker:9092"},
//	    User:     "myuser",
//	    Password: "mypassword",
//	}
//	sk, err := sharkkafka.New(ctx, cfg, logger)
//	if err != nil {
//	    log.Fatalf("创建 Kafka 实例失败: %v", err)
//	}
//	defer sk.Close()
//
//	// 2. 发送消息
//	writer, _ := sk.Writer("order-events")
//	writer.WriteMessages(ctx, kafka.Message{
//	    Key:   []byte("order-12345"),
//	    Value: []byte(`{"status":"created"}`),
//	})
//
//	// 3. 消费消息
//	sk.BatchConsumer("order-events", "order-consumer-group", func(msgs []kafka.Message) bool {
//	    for _, msg := range msgs {
//	        fmt.Println("收到消息:", string(msg.Value))
//	    }
//	    return true // 返回 false 停止消费
//	})
package sharkkafka

import (
	"context"
	"crypto/tls"
	"fmt"
	"sync"
	"time"

	"github.com/lornshark/shark/sharkfunc"
	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl/scram"
	"go.uber.org/zap"
)

// Config 定义了 Kafka 集群的连接配置。
//
// 字段说明：
//   - Host:     Broker 地址列表（必填）
//   - User:     SASL 认证用户名（可选，为空时不启用认证）
//   - Password: SASL 认证密码（可选，为空时不启用认证）
//   - TLS:      是否启用 TLS 加密（默认 false，内网明文通信；true 时使用系统根证书验证服务端）
//
// 认证与加密组合：
//   - User+Password 均非空 → 启用 SASL/SCRAM-SHA512 认证
//   - TLS = true → 启用 TLS 加密（使用系统默认根证书池）
//   - 两者同时启用 → SASL 认证 + TLS 加密（公网安全通信）
//
// 使用示例：
//
//	// YAML 配置
//	// kafka:
//	//   host:
//	//     - "broker1:9092"
//	//   user: "myuser"
//	//   password: "mypassword"
//	//   tls: true
//
//	cfg := &sharkkafka.Config{
//	    Host:     []string{"kafka1:9092", "kafka2:9092"},
//	    User:     "admin",
//	    Password: "secret123",
//	}
//	sk, err := sharkkafka.New(ctx, cfg, logger)
type Config struct {
	Host     []string `json:"host" yaml:"host" mapstructure:"host"`             // Kafka Broker 地址列表，例如 ["broker1:9092", "broker2:9092"]
	User     string   `json:"user" yaml:"user" mapstructure:"user"`             // SASL 认证用户名，为空时不启用认证
	Password string   `json:"password" yaml:"password" mapstructure:"password"` // SASL 认证密码，为空时不启用认证
	TLS      bool     `json:"tls" yaml:"tls" mapstructure:"tls"`                // 是否启用 TLS 加密，默认 false（内网明文），true 时使用系统根证书验证服务端
}

// SharkKafka 是 Kafka 生产者和消费者的统一管理器。
//
// 内部维护了 Writer 池（按 topic 缓存）和共享的 Dialer（连接配置）。
// 所有公共方法都是并发安全的。
//
// 字段说明：
//   - ctx:     上下文，用于控制消费者生命周期
//   - config:  连接配置
//   - writers: Writer 池，key 为 topic 名称
//   - lock:    保护 writers map 的互斥锁
//   - dialer:  连接拨号器（nil = 全明文；非 nil 时包含 SASL 和/或 TLS 配置）
//   - logger:  zap 日志记录器
//
// 零值 SharkKafka 不可直接使用，必须通过 New() 创建。
type SharkKafka struct {
	ctx     context.Context          // 上下文，用于控制消费者生命周期
	config  *Config                  // 连接配置
	writers map[string]*kafka.Writer // Writer 池，按 topic 缓存
	lock    sync.Mutex               // 保护 writers map 的互斥锁
	dialer  *kafka.Dialer            // 连接拨号器（nil = 全明文；非 nil 时包含 SASL 和/或 TLS 配置）
	logger  *zap.Logger              // zap 日志记录器
}

// New 创建一个 SharkKafka 实例。
//
// 参数：
//   - ctx:    上下文，用于控制消费者生命周期
//   - config: 连接配置（必填，为 nil 时返回错误）
//   - logger: zap 日志记录器
//
// 认证与加密逻辑：
//   - SASL:   config.User 和 config.Password 均非空 → 启用 SASL/SCRAM-SHA512
//   - TLS:    config.TLS = true → 启用 TLS 加密（使用系统默认根证书池）
//   - 组合:   SASL + TLS 同时启用 → 公网安全通信（认证 + 加密）
//   - 无 SASL + 无 TLS → 内网明文通信（当前默认行为）
//
// Dialer 构建矩阵：
//
//	+----------+---------+------------------------+
//	|  SASL    |  TLS    |  Dialer                |
//	+----------+---------+------------------------+
//	|  禁用    |  禁用   |  nil (全明文)           |
//	|  禁用    |  启用   |  TLS only              |
//	|  启用    |  禁用   |  SASL only (当前行为)   |
//	|  启用    |  启用   |  SASL + TLS             |
//	+----------+---------+------------------------+
//
// 使用示例：
//
//	// 内网明文
//	cfg := &sharkkafka.Config{Host: []string{"localhost:9092"}}
//
//	// SASL 认证 + TLS 加密（公网）
//	cfg := &sharkkafka.Config{
//	    Host:     []string{"kafka.example.com:9093"},
//	    User:     "admin",
//	    Password: "secret",
//	    TLS:      true,
//	}
//
//	// 仅 TLS 加密（公网，无认证）
//	cfg := &sharkkafka.Config{
//	    Host: []string{"kafka.example.com:9093"},
//	    TLS:  true,
//	}
//
//	sk, err := sharkkafka.New(context.Background(), cfg, logger)
//	if err != nil {
//	    log.Fatalf("初始化 Kafka 失败: %v", err)
//	}
//	defer sk.Close()
func New(ctx context.Context, config *Config, logger *zap.Logger) (*SharkKafka, error) {
	if config == nil {
		return nil, fmt.Errorf("config required")
	}

	// 构建 TLS 配置（config.TLS = true 时启用，使用系统默认根证书池）
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
		// SASL 认证 + TLS 加密
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
		// SASL 认证，明文传输（内网）
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
		// 仅 TLS 加密，无认证
		dialer = &kafka.Dialer{
			Timeout: 10 * time.Second,
			TLS:     tlsConfig,
		}
	default:
		// 无 SASL 无 TLS，全明文
		dialer = nil
	}

	return &SharkKafka{
		ctx:     ctx,
		config:  config,
		writers: make(map[string]*kafka.Writer),
		dialer:  dialer,
		logger:  logger,
	}, nil
}

// Writer 获取或创建指定 topic 的 Kafka Writer（生产者）。
//
// Writer 采用池化设计：首次请求时创建并缓存，后续请求直接返回缓存的实例。
// 该方法线程安全，通过互斥锁保护 writers map。
//
// Writer 配置（默认值）：
//   - Balancer:     &kafka.Hash{}（按 Key 哈希分区）
//   - BatchSize:    10000（单批最多 10000 条消息）
//   - BatchBytes:   2MB（单批最多 2MB）
//   - BatchTimeout: 1s（批次超时）
//   - RequiredAcks: RequireOne（仅等待 Leader 确认，性能优先）
//   - Async:        false（同步写入，保证消息不丢失）
//
// 参数：
//   - topic: Kafka topic 名称
//
// 返回值：
//   - *kafka.Writer: 可复用的 Writer 实例
//   - error: 当前实现始终返回 nil（错误仅在 NewWriter 内部处理）
//
// 使用示例：
//
//	sk, _ := sharkkafka.New(ctx, cfg, logger)
//
//	// 获取 Writer（首次创建，后续复用）
//	writer, err := sk.Writer("order-events")
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// 发送单条消息
//	err = writer.WriteMessages(ctx, kafka.Message{
//	    Key:   []byte("order-12345"),
//	    Value: []byte(`{"order_id":"12345","status":"created"}`),
//	})
//
//	// 批量发送消息
//	messages := []kafka.Message{
//	    {Key: []byte("1"), Value: []byte(`{"data":"msg1"}`)},
//	    {Key: []byte("2"), Value: []byte(`{"data":"msg2"}`)},
//	}
//	writer.WriteMessages(ctx, messages...)
type WriterConfig struct {
	BatchSize    *int                // 单批最多消息数 默认 10000
	BatchBytes   *int                // 单批最大字节数 2MB
	BatchTimeout *time.Duration      // 批次超时时间   1s
	RequiredAcks *kafka.RequiredAcks // 消息确认级别   RequiredAcks
	Async        *bool               // 是否异步写入   true
}

func (s *SharkKafka) Writer(topic string, cfg *WriterConfig) (*kafka.Writer, error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	// 已缓存：直接返回
	if writer, ok := s.writers[topic]; ok {
		return writer, nil
	}
	if cfg == nil {
		cfg = &WriterConfig{
			BatchSize:    sharkfunc.Pointer(10000),            // 单批最多 10000 条消息
			BatchBytes:   sharkfunc.Pointer(1024 * 1024 * 2),  // 单批最多 2MB
			BatchTimeout: sharkfunc.Pointer(time.Second),      // 批次超时 1s
			RequiredAcks: sharkfunc.Pointer(kafka.RequireOne), // 仅等待 Leader 确认
			Async:        sharkfunc.Pointer(true),             // 同步写入，保证消息不丢失
		}
	}
	if cfg.BatchSize == nil {
		cfg.BatchSize = sharkfunc.Pointer(10000)
	}
	if cfg.BatchBytes == nil {
		cfg.BatchBytes = sharkfunc.Pointer(1024 * 1024 * 2)
	}
	if cfg.BatchTimeout == nil {
		cfg.BatchTimeout = sharkfunc.Pointer(time.Second)
	}
	if cfg.RequiredAcks == nil {
		cfg.RequiredAcks = sharkfunc.Pointer(kafka.RequireOne)
	}
	if cfg.Async == nil {
		cfg.Async = sharkfunc.Pointer(true)
	}
	// 未缓存：创建新的 Writer
	writerConfig := kafka.WriterConfig{
		Brokers:      s.config.Host,
		Topic:        topic,
		Dialer:       s.dialer,
		Balancer:     &kafka.Hash{},          // 按 Key 哈希分区，保证相同 Key 进入同一分区
		BatchSize:    *cfg.BatchSize,         // 单批最多 10000 条消息
		BatchBytes:   *cfg.BatchBytes,        // 单批最多 2MB
		BatchTimeout: *cfg.BatchTimeout,      // 批次超时 1s
		RequiredAcks: int(*cfg.RequiredAcks), // 仅等待 Leader 确认
		Async:        *cfg.Async,             // 同步写入，保证消息不丢失
	}
	writer := kafka.NewWriter(writerConfig)
	s.writers[topic] = writer
	return writer, nil
}

// CloseWriter 关闭指定 topic 的 Writer 并从池中移除。
//
// 如果 topic 对应的 Writer 不存在，则不做任何操作返回 nil。
//
// 参数：
//   - topic: 要关闭的 topic 名称
//
// 使用示例：
//
//	// 中途关闭某个 topic 的 Writer（例如动态创建的临时 topic）
//	err := sk.CloseWriter("temp-topic")
//	if err != nil {
//	    log.Printf("关闭 Writer 失败: %v", err)
//	}
func (s *SharkKafka) CloseWriter(topic string) error {
	s.lock.Lock()
	writer, ok := s.writers[topic]
	if ok {
		delete(s.writers, topic) // 从池中移除
	}
	s.lock.Unlock() // 提前释放锁，避免 Close 阻塞其他 Writer 操作
	if ok {
		return writer.Close()
	}
	return nil
}

// Close 关闭所有 Writer 并清空池。
//
// 遍历所有已缓存的 Writer 并逐一关闭。
// 收集第一个遇到的错误返回（不中断后续关闭操作）。
//
// 调用时机：在应用退出时调用（通常配合 defer）。
//
// 使用示例：
//
//	sk, _ := sharkkafka.New(ctx, cfg, logger)
//	defer sk.Close() // 应用退出时自动关闭所有 Writer
func (s *SharkKafka) Close() error {
	s.lock.Lock()
	writers := s.writers
	s.writers = make(map[string]*kafka.Writer) // 清空池
	s.lock.Unlock()
	var firstErr error
	for _, writer := range writers {
		if err := writer.Close(); err != nil && firstErr == nil {
			firstErr = err // 只记录第一个错误
		}
	}
	return firstErr
}

// Reader 创建一个 Kafka Reader（消费者）。
//
// 每次调用都创建新的 Reader 实例（不缓存），调用方负责调用 Close()。
//
// 参数：
//   - topic: 要消费的 topic 名称
//   - group: 消费者组 ID（同一组的消费者分摊消费分区）
//
// Reader 配置（默认值）：
//   - MinBytes:    1（只要有数据就立即返回）
//   - MaxBytes:    10MB（单次拉取最多 10MB）
//   - StartOffset: FirstOffset（从最早的消息开始）
//
// 使用示例：
//
//	// 创建 Reader
//	reader := sk.Reader("order-events", "order-consumer-group")
//	defer reader.Close()
//
//	// 逐条消费
//	for {
//	    msg, err := reader.ReadMessage(ctx)
//	    if err != nil {
//	        log.Printf("读取消息失败: %v", err)
//	        break
//	    }
//	    fmt.Printf("收到消息: key=%s value=%s\n", string(msg.Key), string(msg.Value))
//	}
type ReaderConfig struct {
	MinBytes    *int   // 单次拉取的最小字节数（低延迟）默认 1
	MaxBytes    *int   // 单次拉取的最大字节数（高吞吐）默认 10MB
	StartOffset *int64 // 消费起始偏移量（FirstOffset / LastOffset / 指定偏移量）
}

func (s *SharkKafka) Reader(topic string, group string, cfg *ReaderConfig) *kafka.Reader {
	if cfg == nil {
		cfg = &ReaderConfig{
			MinBytes:    sharkfunc.Pointer(1),                 // 有数据就返回（低延迟）
			MaxBytes:    sharkfunc.Pointer(10 * 1024 * 1024),  // 单次最多返回 10MB
			StartOffset: sharkfunc.Pointer(kafka.FirstOffset), // 从最早的消息开始消费
		}
	}
	if cfg.MinBytes == nil {
		cfg.MinBytes = sharkfunc.Pointer(1)
	}
	if cfg.MaxBytes == nil {
		cfg.MaxBytes = sharkfunc.Pointer(10 * 1024 * 1024)
	}
	if cfg.StartOffset == nil {
		cfg.StartOffset = sharkfunc.Pointer(kafka.FirstOffset)
	}
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:           s.config.Host,
		Topic:             topic,
		GroupID:           group,
		MinBytes:          *cfg.MinBytes, // 有数据就返回（低延迟）
		MaxBytes:          *cfg.MaxBytes, // 单次最多返回 10MB
		Dialer:            s.dialer,
		StartOffset:       *cfg.StartOffset, // 从最早的消息开始消费
		SessionTimeout:    time.Minute,      // 超过这个时间没有收到 Consumer 的 heartbeat，Coordinator 认为 Consumer 死了，然后触发 Rebalance。
		RebalanceTimeout:  time.Minute,      // Consumer 加入 group 后，参与 rebalance 的最大等待时间。
		QueueCapacity:     10000,            // 内部缓冲队列容量，避免短时间内拉取过多消息导致内存占用过高
		HeartbeatInterval: 10 * time.Second,
	})
	return reader
}

// BatchConsumer 启动批量消费者，自动拉取、缓冲、提交 offset。
//
// 工作流程：
//  1. 创建 Reader 并启动拉取协程，将消息推入带缓冲的 channel
//  2. 消费协程每批最多拉取 10000 条消息，调用 handler 处理
//  3. handler 返回 true → 提交 offset 并继续；返回 false / panic → 停止消费
//  4. offset 提交失败自动重试 5 次（每次超时 1 秒）
//
// 停止条件：
//   - handler 返回 false
//   - handler 内部 panic（捕获后停止）
//   - ctx 被取消（优雅退出）
//   - offset 提交连续失败 5 次
//
// 参数：
//   - topic:   要消费的 topic 名称
//   - group:   消费者组 ID
//   - handler: 批量消息处理函数。参数为消息切片，返回 true 继续消费，false 停止消费
//
// 使用示例：
//
//	sk, _ := sharkkafka.New(ctx, cfg, logger)
//
//	// 启动批量消费者（阻塞直到 handler 返回 false 或 ctx 取消）
//	sk.BatchConsumer("order-events", "order-processor", func(msgs []kafka.Message) bool {
//	    for _, msg := range msgs {
//	        var order Order
//	        if err := json.Unmarshal(msg.Value, &order); err != nil {
//	            logger.Error("消息解析失败", zap.Error(err))
//	            continue
//	        }
//	        processOrder(order)
//	    }
//	    return true // 继续消费
//	})
//
//	// 带停止条件的消费
//	var count int
//	sk.BatchConsumer("events", "counter-group", func(msgs []kafka.Message) bool {
//	    count += len(msgs)
//	    if count >= 10000 {
//	        logger.Info("已达到处理上限，停止消费")
//	        return false // 停止消费
//	    }
//	    return true
//	})
type BatchConfig struct {
	ReaderConfig
	BatchSize *int           // 每批处理的最大消息数，默认 10000
	Timeout   *time.Duration // 批次处理超时时间，默认 0 立即返回
}

func (s *SharkKafka) BatchConsumer(topic string, group string, cfg *BatchConfig, handler func([]kafka.Message) bool) {
	if cfg == nil {
		cfg = &BatchConfig{
			ReaderConfig: ReaderConfig{
				MinBytes:    sharkfunc.Pointer(1),                 // 有数据就返回（低延迟）
				MaxBytes:    sharkfunc.Pointer(10 * 1024 * 1024),  // 单次最多返回 10MB
				StartOffset: sharkfunc.Pointer(kafka.FirstOffset), // 从最早的消息开始消费
			},
			BatchSize: sharkfunc.Pointer(10000),            // 每批处理的最大消息数，默认  10000
			Timeout:   sharkfunc.Pointer(time.Duration(0)), // 批次处理超时时间，默认 0 立即返回
		}
	}
	if cfg.MinBytes == nil {
		cfg.MinBytes = sharkfunc.Pointer(1)
	}
	if cfg.MaxBytes == nil {
		cfg.MaxBytes = sharkfunc.Pointer(10 * 1024 * 1024)
	}
	if cfg.StartOffset == nil {
		cfg.StartOffset = sharkfunc.Pointer(kafka.FirstOffset)
	}
	if cfg.BatchSize == nil {
		cfg.BatchSize = sharkfunc.Pointer(10000)
	}
	if cfg.Timeout == nil {
		cfg.Timeout = sharkfunc.Pointer(time.Duration(0))
	}
	reader := s.Reader(topic, group, &cfg.ReaderConfig)
	batchSize := *cfg.BatchSize
	channel := make(chan kafka.Message, batchSize*2) // 带缓冲 channel，容量为 batchSize 的 2 倍
	defer func() {
		close(channel) // 关闭 channel，通知消费协程退出
		reader.Close() // 关闭 Reader
	}()
	// 创建可取消的子上下文，用于 handler 返回 false 时停止拉取
	running, runningCalcel := context.WithCancel(s.ctx)
	defer runningCalcel()

	// safeHandler 包装 handler，捕获 panic 防止消费者崩溃
	safeHandler := func(msgs []kafka.Message) (result bool) {
		defer func() {
			if r := recover(); r != nil {
				s.logger.Error("Consumer handler panic", zap.Any("err", r))
				result = false // panic 后停止消费
			}
		}()
		return handler(msgs)
	}

	// 消费协程：从 channel 批量拉取消息并处理
	go func() {
		for {
			// 批量排空 channel，最多取 batchSize 条
			messages, err := sharkfunc.DrainChannelN(running, channel, batchSize, *cfg.Timeout)
			if err != nil {
				// channel 为空且上下文已取消 → 正常退出
				return
			}
			// 调用 handler 处理消息
			result := safeHandler(messages)
			if !result {
				// handler 返回 false 或 panic → 停止拉取
				s.logger.Warn("Consumer handler returned false, stop consuming", zap.String("topic", topic), zap.String("group", group))
				runningCalcel()
				return
			}
			// 提交 offset 一直重试,直到成功
			var commitError error
			for i := 0; i < 50000000; i++ {
				commitError = sharkfunc.WithTimeout(running, time.Second, func(ctx context.Context) error {
					return reader.CommitMessages(ctx, messages...)
				})
				if commitError == nil {
					break // 提交成功
				}
				s.logger.Warn("提交 Kafka 消息 offset 失败", zap.String("topic", reader.Config().Topic), zap.Error(commitError), zap.Int("retry", i+1))
				time.Sleep(time.Second) // 等待 1 秒后重试
				// 再次检查上下文是否已取消
				if running.Err() != nil {
					return
				}
			}
			if commitError != nil {
				runningCalcel()
				// 虽然不是panic,日志带上panic字样以便监控报警,当panic处理
				s.logger.Error("提交 Kafka 消息 offset 失败 panic", zap.String("topic", reader.Config().Topic), zap.Error(commitError))
				return
			}
			// 再次检查上下文是否已取消
			if running.Err() != nil {
				return
			}
		}
	}()

	// 拉取协程：从 Kafka 逐条拉取消息并推入 channel
	var fetchMessages = func(ctx context.Context) bool {
		msg, err := reader.FetchMessage(ctx)
		if err == nil {
			// 拉取成功：推入 channel（若 ctx 取消则退出）
			select {
			case channel <- msg:
			case <-ctx.Done():
				return false
			}
			return true
		}
		// 上下文取消 → 正常退出
		if ctx.Err() != nil {
			return false
		}
		// 拉取失败（非上下文取消）：记录错误并等待 1 秒后重试
		s.logger.Error("读取 Kafka 消息失败", zap.String("topic", reader.Config().Topic), zap.Error(err))
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
			return false
		}
		return true
	}

	// 主循环：持续拉取消息
	for {
		select {
		case <-running.Done():
			// 上下文取消 → 退出
			return
		default:
			if !fetchMessages(running) {
				// 拉取失败且不可恢复 → 退出
				return
			}
		}
	}
}
