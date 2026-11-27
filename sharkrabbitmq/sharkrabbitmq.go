// Package sharkrabbitmq 提供支持重连、发布和消费的 RabbitMQ 客户端。
// 发布为尽力而为；消费端至少一次，业务需保证幂等。
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
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/xcodego/shark/sharkutils"
	"go.uber.org/zap"
)

// Config 是 RabbitMQ 的连接配置。
type Config struct {
	// Host 是 broker 地址列表，格式为 host:port，可带 amqp:// 或 amqps:// 前缀（缺省 amqp）；不能为空。
	Host []string `json:"host" yaml:"host" mapstructure:"host"`
	// User 是认证用户名。
	User string `json:"user" yaml:"user" mapstructure:"user"`
	// Password 是认证密码。
	Password string `json:"password" yaml:"password" mapstructure:"password"`
}

// Client 管理 RabbitMQ 连接、异步发布和消息消费。请使用 New 创建。
type Client struct {
	config        *Config
	wg            *sync.WaitGroup
	ctx           context.Context
	logger        *zap.Logger
	conn          *amqp.Connection
	connLock      sync.Mutex
	channel       *amqp.Channel
	publish       chan publishMsg // 异步发布缓冲队列，容量 100000
	name          string          // 服务名，同时用于选择 broker 和作为消费者标签前缀
	id            string          // 实例 id，与 name 组成消费者标签 name.id
	closeLock     sync.Mutex      // 保护 publish 通道的发送与关闭
	connectionCtx context.Context // 当前连接的生命周期，连接断开时取消，用于通知消费协程重建
	cancel        context.CancelFunc
	drained       chan struct{} // publish_msg 退出（发布队列处理完）后关闭，connect 据此才关闭连接
	connDone      chan struct{} // connect 协程退出（连接已关闭）后关闭，Close 据此等待
}

// publishMsg 是等待异步发送的消息。
type publishMsg struct {
	exchange string
	key      string
	value    *amqp.Publishing
}

// New 按服务名的 CRC16 结果从 config.Host 中选定一个 broker，并阻塞重试直到首次连接成功，
// 因此不会返回 error（启动阶段连不上 broker 继续运行也没有意义）。
// 之后连接断开会自动重连。ctx 取消（或调用 Close）后停止重连和消费，发布队列处理完再关闭连接；
// wg 用于等待发布协程退出。config.Host 不能为空。
func New(ctx context.Context, logger *zap.Logger, wg *sync.WaitGroup, config *Config, name string, id string) (*Client, error) {
	index := crc16.Checksum([]byte(name), crc16.IBMTable)
	index = index % uint16(len(config.Host))
	ctx, cancel := context.WithCancel(ctx)
	client := &Client{
		config:   config,
		ctx:      ctx,
		cancel:   cancel,
		wg:       wg,
		logger:   logger,
		name:     name,
		id:       id,
		drained:  make(chan struct{}),
		connDone: make(chan struct{}),
	}
	client.publish = make(chan publishMsg, 100000)
	innerwg := &sync.WaitGroup{}
	innerwg.Add(1)
	go client.connect(int(index), innerwg)
	innerwg.Wait()
	wg.Add(1)
	go client.publish_msg()
	go client.exit_waiting()
	return client, nil
}

// Close 停止重连和消费，等待发布队列处理完毕并关闭连接后返回。可重复调用。
//
// 队列里未发出的消息会在连接关闭前尽力发送；ctx 取消后发布失败的消息会被丢弃并记录日志。
// Close 之后 Publish 返回错误。ctx 被外部取消时效果相同，Close 只是额外等待连接关闭。
func (c *Client) Close() {
	c.cancel()
	<-c.connDone
}

// exit_waiting 等待 context 取消后关闭发布通道，使 publish_msg 处理完剩余消息后退出。
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

// set_conn 更新连接；传入 nil 时关闭当前连接。
func (c *Client) set_conn(conn *amqp.Connection) {
	c.connLock.Lock()
	defer c.connLock.Unlock()
	if c.conn != nil && conn == nil {
		c.conn.Close()
	}
	c.conn = conn
}

// set_channel 更新 channel；传入 nil 时关闭当前 channel。
func (c *Client) set_channel(channel *amqp.Channel) {
	c.connLock.Lock()
	defer c.connLock.Unlock()
	if c.channel != nil && channel == nil {
		c.channel.Close()
	}
	c.channel = channel
}

// connect 连接 Host[index] 并在连接关闭后持续重连，首次连接成功时通过 wg.Done 通知 New。
func (c *Client) connect(index int, wg *sync.WaitGroup) {
	defer close(c.connDone)
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
		channel, err := conn.Channel()
		if err != nil {
			c.logger.Error("创建Rabbitmq Channel失败", zap.String("host", c.config.Host[index]), zap.Error(err))
			conn.Close()
			time.Sleep(time.Second)
			continue
		}
		connectionCtx, cancel := context.WithCancel(context.Background())
		c.connectionCtx = connectionCtx
		c.set_conn(conn)
		c.set_channel(channel)
		if count > 0 {
			c.logger.Info("重连Rabbitmq成功", zap.String("host", c.config.Host[index]))
		} else {
			wg.Done()
		}
		connErr := make(chan *amqp.Error, 1)
		conn.NotifyClose(connErr)
		select {
		case <-c.ctx.Done():
			cancel()
			// 发布队列还在用这条连接，处理完才能关闭，否则剩余消息只能丢弃。
			<-c.drained
			conn.Close()
			return
		case e := <-connErr:
			c.logger.Warn("Rabbitmq连接已关闭", zap.String("host", c.config.Host[index]), zap.Error(e))
			cancel()
			c.set_channel(nil)
			c.set_conn(nil)
			count++
		}
	}
}

// publish_msg 异步发送缓冲消息：发送失败或暂无连接时每秒重试。
// 上下文取消后不再重试：无连接或发送失败的消息直接丢弃并记录日志，保证关闭过程有限时间内结束。
// 退出时关闭 drained，通知 connect 可以关闭连接。
func (c *Client) publish_msg() {
	defer c.wg.Done()
	defer close(c.drained)
	for msg := range c.publish {
		for {
			ch := c.get_channel()
			if c.ctx.Err() != nil && ch == nil {
				c.logLost(msg)
				break
			}
			if ch == nil {
				time.Sleep(time.Second)
				continue
			}
			err := ch.Publish(msg.exchange, msg.key, false, false, *msg.value)
			if err != nil {
				if c.ctx.Err() != nil {
					c.logLost(msg)
					break
				}
				time.Sleep(time.Second)
				continue
			}
			break
		}
	}
}

// logLost 记录一条因客户端关闭而未能发出的消息。
func (c *Client) logLost(msg publishMsg) {
	c.logger.Error("消息丢失: Rabbitmq连接已关闭且上下文已结束",
		zap.String("exchange", msg.exchange),
		zap.String("key", msg.key),
		zap.ByteString("value", msg.value.Body),
	)
}

// Consume 异步消费指定队列，立即返回。
// 会声明 topic 类型的持久化 exchange 与 queue 并按 key 绑定；handler 在单个协程中逐条同步调用，
// 需要自行 Ack（autoAck 关闭，prefetch 为 10000），panic 会被恢复并记录日志。
// 连接断开后自动重建消费者，ctx 取消后退出。
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
			channel.Qos(10000, 0, false)
			channel.ExchangeDeclare(exchange, "topic", true, false, false, false, nil)
			channel.QueueDeclare(queue, true, false, false, false, nil)
			channel.QueueBind(queue, key, exchange, false, nil)
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

// self_handle 调用 handler，recover 其 panic 并记录日志，避免消费协程退出。
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

// handle_channel 逐条调用 handler，直到 deliveries 通道关闭、连接断开或 ctx 取消。
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

// BatchConsumeConfig 设置批量消费的批次大小和凑批等待时间，字段为 nil 时使用默认值。
type BatchConsumeConfig struct {
	BatchSize *int           // 每批处理的最大消息数，默认 10000
	Timeout   *time.Duration // 读到首条后继续凑批的最长等待，默认 0：取走已有消息即处理，不额外等待
}

// BatchConsume 异步批量消费指定队列，立即返回。
// handler 返回 true 时逐条 Ack 整批消息；返回 false 或 panic 时不 Ack，关闭 channel 后消息会重新投递，
// 并立即重建消费者重试（因此业务应保证幂等）。连接断开后同样自动重建，ctx 取消后退出。
// exchange、queue 的声明方式与 Consume 相同，prefetch 为 BatchSize 的两倍。
func (c *Client) BatchConsume(exchange string, queue string, key string, cfg *BatchConsumeConfig, handler func([]amqp.Delivery) bool) {
	if cfg == nil {
		cfg = &BatchConsumeConfig{
			BatchSize: sharkutils.Pointer(10000),
			Timeout:   sharkutils.Pointer(time.Duration(0)),
		}
	}
	if cfg.BatchSize == nil {
		cfg.BatchSize = sharkutils.Pointer(10000)
	}
	if cfg.Timeout == nil {
		cfg.Timeout = sharkutils.Pointer(time.Duration(0))
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
			// 中转通道：连接断开或 ctx 取消时关闭它，使下面的 DrainChannelN 能及时返回
			drainChannel := make(chan amqp.Delivery, batchSize*2)
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
				messages := sharkutils.DrainChannelN(ctx, drainChannel, batchSize, *cfg.Timeout)
				result := safeHandler(messages)
				if !result {
					cancel()
					break
				}
				for _, msg := range messages {
					msg.Ack(false)
				}
			}
			cancel()
			channel.Close()
		}
	}()
}

// Publish 将消息放入异步发布队列后立即返回，不保证已送达 broker。
// string 和 []byte 原样发送，其他类型按 JSON 序列化，ContentType 固定为 application/json；
// 消息体上限 100 KB，队列满或客户端已关闭时返回错误。
func (c *Client) Publish(exchange string, key string, value any) error {
	var body []byte
	switch v := value.(type) {
	case string:
		body = []byte(v)
	case []byte:
		body = v
	default:
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
	c.closeLock.Lock()
	defer c.closeLock.Unlock()
	// 必须持锁后检查：发布通道只会在 ctx 取消之后、持同一把锁时关闭，
	// 这里检查通过就说明通道仍然打开。
	if c.ctx.Err() != nil {
		return fmt.Errorf("client is closed")
	}
	select {
	case c.publish <- msg:
		return nil
	default:
		return fmt.Errorf("publish channel is full")
	}
}

// DeleteQueue 删除指定队列及其中未消费的消息；无可用连接时阻塞重试，ctx 取消后返回。
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
		channel.QueueDelete(queue, false, false, false)
		channel.Close()
		break
	}
}
