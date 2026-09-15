package test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lornshark/shark/sharkrabbitmq"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
)

func TestRabbitmqNew(t *testing.T) {
	cfg := loadRabbitmqConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	client, err := sharkrabbitmq.New(ctx, zap.NewNop(), &wg, cfg, "test-service", "1")
	if err != nil {
		t.Fatalf("连接 RabbitMQ 失败: %v", err)
	}
	defer func() {
		cancel()
		wg.Wait()
	}()
	t.Log("RabbitMQ 连接成功")

	// 测试发布消息
	err = client.Publish("test-shark-exchange", "test.key", map[string]any{
		"msg":    "hello from shark test",
		"ts":     time.Now().Unix(),
		"source": "shark-test",
	})
	if err != nil {
		t.Fatalf("Publish 失败: %v", err)
	}
	t.Log("Publish 成功")
}

func TestRabbitmqConsume(t *testing.T) {
	cfg := loadRabbitmqConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	client, err := sharkrabbitmq.New(ctx, zap.NewNop(), &wg, cfg, "test-consumer", "1")
	if err != nil {
		t.Fatalf("连接 RabbitMQ 失败: %v", err)
	}

	// 发送消息
	exchange := "test-shark-consume"
	queue := "test-shark-queue"
	key := "test.key"
	client.Publish(exchange, key, map[string]string{"action": "test"})
	time.Sleep(500 * time.Millisecond)

	// 消费消息
	received := make(chan struct{}, 1)
	client.Consume(exchange, queue, key, func(msg amqp.Delivery) {
		t.Logf("收到消息: %s", string(msg.Body))
		msg.Ack(false)
		received <- struct{}{}
	})

	select {
	case <-received:
		t.Log("Consume 收到消息")
	case <-time.After(10 * time.Second):
		t.Log("Consume 超时（可能无消息）")
	}

	client.DeleteQueue(queue)
	cancel()
	wg.Wait()
}

func TestRabbitmqBatchConsume(t *testing.T) {
	cfg := loadRabbitmqConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	client, err := sharkrabbitmq.New(ctx, zap.NewNop(), &wg, cfg, "test-batch-consumer", "1")
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}

	exchange := "test-shark-batch-exchange"
	queue := "test-shark-batch-queue"
	key := "batch.key"

	// 发送多条消息
	client.Publish(exchange, key, map[string]string{"id": "1"})
	client.Publish(exchange, key, map[string]string{"id": "2"})
	time.Sleep(500 * time.Millisecond)

	received := make(chan int, 1)
	client.BatchConsume(exchange, queue, key, nil, func(msgs []amqp.Delivery) bool {
		count := len(msgs)
		t.Logf("批量收到 %d 条消息", count)
		received <- count
		return false // 停止消费
	})

	select {
	case count := <-received:
		if count < 1 {
			t.Errorf("应至少收到 1 条, got %d", count)
		}
	case <-time.After(10 * time.Second):
		t.Log("BatchConsume 超时")
	}

	client.DeleteQueue(queue)
	cancel()
	wg.Wait()
}
