// Package sharkrabbitmq 提供了 RabbitMQ 消息队列的客户端封装。
//
// 支持:
//   - 自动重连（连接断开后自动恢复）
//   - 多 broker 负载均衡（基于 CRC16 哈希选择节点）
//   - 缓冲发布（100000 条消息缓冲，异步批量发送）
//   - 单条消费（Consume）和批量消费（BatchConsume）
//   - 优雅关闭（通过 context 和 WaitGroup 控制退出）
//
// 投递语义说明：
//   - 尽力而为（Best effort）投递
//   - 当发布缓冲区已满时，消息可能被丢弃
//   - 在优雅关闭过程中，消息可能被丢弃
//   - 不对"发送过程中（in-flight）消息"提供持久化保障
//   - 消费端为至少一次投递（at-least-once），业务必须保证幂等性
package sharkrabbitmq

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/howeyc/crc16"
	"github.com/lornshark/shark/sharkfunc"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
)

// Config 是 RabbitMQ 的连接配置。
//
// 支持多 broker 地址（Host 数组），客户端会根据服务名称的 CRC16 哈希值
// 选择一个固定的 broker 节点进行连接，实现多服务间的负载均衡分布。
type Config struct {
	// Host broker 地址列表，格式为 "host:port"
	Host []string `json:"host" yaml:"host" mapstructure:"host"`
	// User 认证用户名
	User string `json:"user" yaml:"user" mapstructure:"user"`
	// Password 认证密码
	Password string `json:"password" yaml:"password" mapstructure:"password"`
}

// Client 是 RabbitMQ 客户端，封装了连接管理、消息发布和消费功能。
//
// 内部通过以下机制保证可靠性：
//   - connLock: 保护连接和 channel 的并发安全读写
//   - publish chan: 容量 100000 的缓冲通道，异步发布消息
//   - closeLock: 保护优雅关闭期间 publish channel 的关闭
type Client struct {
	config        *Config
	wg            *sync.WaitGroup
	ctx           context.Context
	logger        *zap.Logger
	conn          *amqp.Connection // 当前连接
	connLock      sync.Mutex       // 连接读写锁
	channel       *amqp.Channel    // 当前 channel
	publish       chan publishMsg  // 发布消息缓冲通道（容量 100000）
	name          string           // 服务名称（用于消费者标识）
	id            string           // 实例 ID（用于消费者标识）
	closeLock     sync.Mutex       // 关闭锁
	connectionCtx context.Context  // 连接上下文（用于取消连接）
}

// publishMsg 是内部发布消息的数据结构。
type publishMsg struct {
	exchange string           // 交换机名称
	key      string           // 路由键
	value    *amqp.Publishing // 消息内容
}

// New 创建 RabbitMQ 客户端并建立连接。
//
// 使用服务名称的 CRC16 哈希值取模选择 broker 节点，
// 确保同一服务始终连接到同一个 broker（冷热均衡）。
//
// 使用示例:
//
//	client, err := sharkrabbitmq.New(ctx, logger, wg, &sharkrabbitmq.Config{
//	    Host:     []string{"127.0.0.1:5672"},
//	    User:     "guest",
//	    Password: "guest",
//	}, "my-service", "1")
//	if err != nil {
//	    log.Fatal(err)
//	}
//
// 参数:
//   - ctx: 上下文，用于控制客户端生命周期
//   - logger: zap 日志记录器
//   - wg: 外部 WaitGroup（客户端内部 goroutine 会 Add）
//   - config: 连接配置
//   - name: 服务名称（用于消费者标识）
//   - id: 实例 ID（用于消费者标识）
//
// 返回:
//   - *Client: RabbitMQ 客户端实例
//   - error: 连接失败时返回错误
func New(ctx context.Context, logger *zap.Logger, wg *sync.WaitGroup, config *Config, name string, id string) (*Client, error) {
	// 使用 CRC16 哈希选择 broker 节点，同一服务固定连接到同一节点
	index := crc16.Checksum([]byte(name), crc16.IBMTable)
	index = index % uint16(len(config.Host))
	client := &Client{
		config: config,
		ctx:    ctx,
		wg:     wg,
		logger: logger,
		name:   name,
		id:     id,
	}
	// 初始化发布缓冲区（容量 100000 条消息）
	client.publish = make(chan publishMsg, 100000)
	// 等待首次连接成功
	innerwg := &sync.WaitGroup{}
	innerwg.Add(1)
	go client.connect(int(index), innerwg)
	innerwg.Wait()
	// 启动消息发布协程
	wg.Add(1)
	go client.publish_msg()
	// 启动优雅退出监听
	go client.exit_waiting()
	return client, nil
}

// exit_waiting 等待 context 取消后关闭发布通道。
func (c *Client) exit_waiting() {
	<-c.ctx.Done()
	c.closeLock.Lock()
	defer c.closeLock.Unlock()
	close(c.publish)
}

// get_conn 线程安全地获取当前连接。
func (c *Client) get_conn() *amqp.Connection {
	c.connLock.Lock()
	defer c.connLock.Unlock()
	conn := c.conn
	return conn
}

// get_channel 线程安全地获取当前 channel。
func (c *Client) get_channel() *amqp.Channel {
	c.connLock.Lock()
	defer c.connLock.Unlock()
	channel := c.channel
	return channel
}

// set_conn 线程安全地设置连接（旧连接为 nil 时自动关闭旧连接）。
func (c *Client) set_conn(conn *amqp.Connection) {
	c.connLock.Lock()
	defer c.connLock.Unlock()
	if c.conn != nil && conn == nil {
		c.conn.Close()
	}
	c.conn = conn
}

// set_channel 线程安全地设置 channel（旧 channel 为 nil 时自动关闭旧 channel）。
func (c *Client) set_channel(channel *amqp.Channel) {
	c.connLock.Lock()
	defer c.connLock.Unlock()
	if c.channel != nil && channel == nil {
		c.channel.Close()
	}
	c.channel = channel
}

// connect 建立 RabbitMQ 连接，连接断开时自动重连。
//
// 通过 NotifyClose 监听连接关闭事件，一旦连接断开立即清理旧连接/channel
// 并重新进入连接循环。首次连接成功时通过 wg.Done() 通知调用方。
//
// 参数:
//   - index: Host 数组索引（由 CRC16 哈希确定）
//   - wg: 首次连接成功时 Done
func (c *Client) connect(index int, wg *sync.WaitGroup) {
	count := 0
	for {
		if c.ctx.Err() != nil {
			break
		}
		host := c.config.Host[index]
		scheme := "amqp"
		switch {
		case strings.HasPrefix(host, "amqps://"):
			scheme = "amqps"
			host = strings.TrimPrefix(host, "amqps://")
		case strings.HasPrefix(host, "amqp://"):
			scheme = "amqp"
			host = strings.TrimPrefix(host, "amqp://")
		}
		amqpurl := fmt.Sprintf("%s://%s:%s@%s", scheme, c.config.User, c.config.Password, host)
		conn, err := amqp.Dial(amqpurl)
		if err != nil {
			c.logger.Error("连接Rabbitmq失败", zap.String("host", c.config.Host[index]), zap.Error(err))
			time.Sleep(time.Second)
			continue
		}
		// 创建 channel
		channel, err := conn.Channel()
		if err != nil {
			c.logger.Error("创建Rabbitmq Channel失败", zap.String("host", c.config.Host[index]), zap.Error(err))
			conn.Close()
			time.Sleep(time.Second)
			continue
		}
		connectionCtx, cancel := context.WithCancel(context.Background())
		c.connectionCtx = connectionCtx
		// 更新连接和 channel
		c.set_conn(conn)
		c.set_channel(channel)
		if count > 0 {
			c.logger.Info("重连Rabbitmq成功", zap.String("host", c.config.Host[index]))
		} else {
			wg.Done()
		}
		// 监听连接关闭事件
		connErr := make(chan *amqp.Error, 1)
		conn.NotifyClose(connErr)
		select {
		case <-c.ctx.Done():
			cancel()
			conn.Close()
			return
		case e := <-connErr:
			c.logger.Warn("Rabbitmq连接已关闭", zap.String("host", c.config.Host[index]), zap.Error(e))
			// 清空连接和 channel，触发重连
			cancel()
			c.set_channel(nil)
			c.set_conn(nil)
			count++
		}
	}
}

// publish_msg 从发布通道读取消息并发送到 RabbitMQ。
//
// 如果发送失败会重试（每秒一次），直到成功或 context 取消。
// context 已取消且无可用连接时，消息会被丢弃并记录日志。
func (c *Client) publish_msg() {
	defer c.wg.Done()
	for msg := range c.publish {
		for {
			ch := c.get_channel()
			if c.ctx.Err() != nil && ch == nil {
				// context 已取消且没有连接可用，丢弃消息
				c.logger.Error("消息丢失: Rabbitmq连接已关闭且上下文已结束",
					zap.String("exchange", msg.exchange),
					zap.String("key", msg.key),
					zap.ByteString("value", msg.value.Body),
				)
				break
			}
			if ch == nil {
				// 连接尚未就绪，等待 1 秒后重试
				time.Sleep(time.Second)
				continue
			}
			err := ch.Publish(msg.exchange, msg.key, false, false, *msg.value)
			if err != nil {
				time.Sleep(time.Second)
				continue
			}
			break
		}
	}
}

// Consume 消费指定队列的消息，逐条交给 handler 处理。
//
// handler 需要自行决定是否 ack 消息：
//   - 调用 msg.Ack(false) 确认消费成功
//   - 不调用 ack 则消息会重新投递
//
// 内部自动处理连接断开重连，handler 中的 panic 会被捕获并记录日志。
//
// 使用示例:
//
//	client.Consume("my-exchange", "my-queue", "my-key", func(msg amqp.Delivery) {
//	    // 处理消息
//	    processMessage(msg.Body)
//	    msg.Ack(false) // 确认消费
//	})
//
// 参数:
//   - exchange: 交换机名称（自动声明为 topic 类型，持久化）
//   - queue: 队列名称（自动声明，持久化）
//   - key: 路由键
//   - handler: 单条消息处理函数
func (c *Client) Consume(exchange string, queue string, key string, handler func(amqp.Delivery)) {
	go func() {
		for {

			if c.ctx.Err() != nil {
				return
			}
			if c.connectionCtx != nil && c.connectionCtx.Err() != nil {
				time.Sleep(1 * time.Second)
				continue
			}
			conn := c.get_conn()
			if conn == nil {
				time.Sleep(1 * time.Second)
				continue
			}
			channel, err := conn.Channel()
			if err != nil {
				time.Sleep(time.Second)
				continue
			}
			// 设置 QoS（预取 10000 条消息）
			channel.Qos(10000, 0, false)
			// 声明交换机（持久化 topic 类型）
			channel.ExchangeDeclare(exchange, "topic", true, false, false, false, nil)
			// 声明队列（持久化）
			channel.QueueDeclare(queue, true, false, false, false, nil)
			// 绑定队列到交换机
			channel.QueueBind(queue, key, exchange, false, nil)
			// 开始消费
			data, err := channel.Consume(queue, fmt.Sprintf("%v.%v", c.name, c.id),
				false, // autoAck=false（手动确认）
				false, false, false, nil,
			)
			if err != nil {
				time.Sleep(time.Second)
			} else {
				c.handle_channel(data, handler)
			}
			channel.Close()
		}
	}()
}

// self_handle 安全调用 handler，捕获 panic 并记录日志。
func (c *Client) self_handle(msg amqp.Delivery, handler func(amqp.Delivery)) {
	defer func() {
		if r := recover(); r != nil {
			c.logger.Error("Rabbitmq消费者处理消息panic",
				zap.Any("r", r),
				zap.String("stack", string(debug.Stack())),
			)
		}
	}()
	handler(msg)
}

// handle_channel 从 channel 接收消息并逐个处理。
func (c *Client) handle_channel(channel <-chan amqp.Delivery, handler func(amqp.Delivery)) {
	for {
		select {
		case <-c.connectionCtx.Done():
			return
		case <-c.ctx.Done():
			return
		case msg, ok := <-channel:
			if !ok {
				return
			}
			c.self_handle(msg, handler)
		}
	}
}

// BatchConsume 批量消费消息，handler 返回 false 或 panic 停止处理且不 ack 消息。
//
// 每批最多聚合（使用 DrainChannelN），
// handler 返回 true 时统一 ack 整批消息，返回 false 时不 ack。
// handler 中不应手动 ack 消息。
//
// 使用示例:
//
//	client.BatchConsume("my-exchange", "my-queue", "my-key",
//	    func(msgs []amqp.Delivery) bool {
//	        for _, msg := range msgs {
//	            if err := process(msg.Body); err != nil {
//	                return false // 停止处理，不 ack
//	            }
//	        }
//	        return true // 整批 ack
//	    },
//	)
//
// 参数:
//   - exchange: 交换机名称
//   - queue: 队列名称
//   - key: 路由键
//   - batchSize: 每批处理的最大消息数
//   - handler: 批量消息处理函数，返回 true 表示整批 ack
type BatchConsumeConfig struct {
	BatchSize *int           // 每批处理的最大消息数，默认 10000
	Timeout   *time.Duration // 批次处理超时时间，默认 0 立即返回
}

func (c *Client) BatchConsume(exchange string, queue string, key string, cfg *BatchConsumeConfig, handler func([]amqp.Delivery) bool) {
	if cfg == nil {
		cfg = &BatchConsumeConfig{
			BatchSize: sharkfunc.Pointer(10000),
			Timeout:   sharkfunc.Pointer(time.Duration(0)),
		}
	}
	if cfg.BatchSize == nil {
		cfg.BatchSize = sharkfunc.Pointer(10000)
	}
	if cfg.Timeout == nil {
		cfg.Timeout = sharkfunc.Pointer(time.Duration(0))
	}
	batchSize := *cfg.BatchSize
	go func() {
		for {
			if c.ctx.Err() != nil {
				return
			}
			if c.connectionCtx != nil && c.connectionCtx.Err() != nil {
				time.Sleep(1 * time.Second)
				continue
			}
			conn := c.get_conn()
			if conn == nil {
				time.Sleep(time.Second)
				continue
			}
			channel, err := conn.Channel()
			if err != nil {
				time.Sleep(time.Second)
				continue
			}
			channel.Qos(batchSize*2, 0, false)
			channel.ExchangeDeclare(exchange, "topic", true, false, false, false, nil)
			channel.QueueDeclare(queue, true, false, false, false, nil)
			channel.QueueBind(queue, key, exchange, false, nil)
			dataChannel, err := channel.Consume(queue, fmt.Sprintf("%v.%v", c.name, c.id),
				false, false, false, false, nil,
			)
			drainChannel := make(chan amqp.Delivery, batchSize*2)
			// 将 dataChannel 的消息转发到 drainChannel，避免阻塞
			go func() {
				for msg := range dataChannel {
					select {
					case drainChannel <- msg:
					case <-c.ctx.Done():
						close(drainChannel)
						return
					case <-c.connectionCtx.Done():
						close(drainChannel)
						return
					}
				}
				close(drainChannel)
			}()
			if err != nil {
				channel.Close()
				time.Sleep(time.Second)
				continue
			}
			ctx, cancel := context.WithCancel(c.ctx)
			// 安全包装 handler，捕获 panic
			safeHandler := func(msgs []amqp.Delivery) (result bool) {
				defer func() {
					if r := recover(); r != nil {
						c.logger.Error("Rabbitmq消费者处理消息panic",
							zap.Any("r", r),
							zap.String("stack", string(debug.Stack())),
						)
						result = false
					}
				}()
				return handler(msgs)
			}
			for {
				// 批量收集消息
				messages, err := sharkfunc.DrainChannelN(ctx, drainChannel, batchSize, *cfg.Timeout)
				if err != nil {
					cancel()
					break
				}
				result := safeHandler(messages)
				if !result {
					cancel()
					break
				}
				// 整批 ack
				for _, msg := range messages {
					msg.Ack(false)
				}
			}
			cancel()
			channel.Close()
		}
	}()
}

// Publish 发布一条消息到指定 exchange 的指定 routing key。
//
// value 支持 string（JSON 字符串）、[]byte、或任意可序列化结构体。
// 消息体最大 10KB，超出会返回错误。
// 发布通道满时返回 "publish channel is full" 错误（非阻塞）。
//
// 使用示例:
//
//	// 发布 JSON 结构体
//	type OrderEvent struct {
//	    OrderId int64  `json:"order_id"`
//	    Status  string `json:"status"`
//	}
//	err := client.Publish("orders", "created", OrderEvent{OrderId: 123, Status: "pending"})
//
//	// 发布 JSON 字符串
//	err := client.Publish("orders", "updated", `{"order_id": 123, "status": "paid"}`)
//
// 参数:
//   - exchange: 交换机名称
//   - key: 路由键
//   - value: 消息内容（string/[]byte/struct）
//
// 返回:
//   - error: 消息体过大、客户端已关闭或发布通道满时返回错误
func (c *Client) Publish(exchange string, key string, value any) error {
	var body []byte
	switch v := value.(type) {
	case string:
		body = []byte(v)
	case []byte:
		body = v
	default:
		// 结构体序列化为 JSON
		body, _ = sonic.Marshal(value)
	}
	if len(body) > 1024*100 {
		return fmt.Errorf("消息体过大，最大支持100KB")
	}
	msg := publishMsg{
		exchange: exchange,
		key:      key,
		value: &amqp.Publishing{
			ContentType: "application/json",
			Body:        []byte(body),
		},
	}
	if c.ctx.Err() != nil {
		return fmt.Errorf("client is closed")
	}
	c.closeLock.Lock()
	defer c.closeLock.Unlock()
	select {
	case c.publish <- msg:
		return nil
	default:
		return fmt.Errorf("publish channel is full")
	}
}

// DeleteQueue 删除指定队列（未消费的消息会被删除）。
//
// 使用示例:
//
//	client.DeleteQueue("temp-queue")
//
// 参数:
//   - queue: 要删除的队列名称
func (c *Client) DeleteQueue(queue string) {
	for {
		if c.ctx.Err() != nil {
			return
		}
		conn := c.get_conn()
		if conn == nil {
			time.Sleep(time.Second)
			continue
		}
		channel, err := conn.Channel()
		if err != nil {
			time.Sleep(time.Second)
			continue
		}
		// ifUnused=false, ifEmpty=false, noWait=false
		channel.QueueDelete(queue, false, false, false)
		channel.Close()
		break
	}
}
