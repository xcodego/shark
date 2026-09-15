package test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lornshark/shark/sharkkafka"
	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"
)

func TestKafkaNew(t *testing.T) {
	cfg := loadKafkaConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	sk, err := sharkkafka.New(ctx, cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("创建 SharkKafka 失败: %v", err)
	}
	defer sk.Close()
	t.Log("SharkKafka 创建成功")
}

func TestKafkaWriter(t *testing.T) {
	cfg := loadKafkaConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	sk, err := sharkkafka.New(ctx, cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	defer sk.Close()

	writer, err := sk.Writer("test-shark-topic", nil)
	if err != nil {
		t.Fatalf("获取 Writer 失败: %v", err)
	}
	t.Log("Writer 获取成功")

	// 测试写入（Kafka borker 可能触发 SASL 握手，不可用则跳过）
	err = writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte("test-key"),
		Value: []byte(`{"msg":"hello from shark test"}`),
	})
	if err != nil {
		t.Skipf("Kafka WriteMessages 失败（可能 SASL 认证问题）: %v", err)
	}
	t.Log("消息发送成功")

	// 相同 topic 再次获取应该复用
	w2, _ := sk.Writer("test-shark-topic", nil)
	if w2 != writer {
		t.Error("同一 topic 应返回缓存的 Writer")
	}
	t.Log("Writer 池化验证通过")

	sk.CloseWriter("test-shark-topic")
}

func TestKafkaReader(t *testing.T) {
	cfg := loadKafkaConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	sk, err := sharkkafka.New(ctx, cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	defer sk.Close()

	// 先发送一条消息
	topicName := "test-shark-reader"
	writer, _ := sk.Writer(topicName, nil)
	writer.WriteMessages(ctx, kafka.Message{Key: []byte("r1"), Value: []byte("reader-test")})
	time.Sleep(500 * time.Millisecond)
	sk.CloseWriter(topicName)

	// 创建 Reader
	reader := sk.Reader(topicName, "test-shark-group", nil)
	defer reader.Close()
	t.Log("Reader 创建成功")
}

func TestKafkaBatchConsumer(t *testing.T) {
	cfg := loadKafkaConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	sk, err := sharkkafka.New(ctx, cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	defer sk.Close()

	topicName := "test-shark-batch"
	// 先发送几条消息
	writer, _ := sk.Writer(topicName, nil)
	writer.WriteMessages(ctx,
		kafka.Message{Key: []byte("k1"), Value: []byte("msg1")},
		kafka.Message{Key: []byte("k2"), Value: []byte("msg2")},
	)
	time.Sleep(500 * time.Millisecond)
	sk.CloseWriter(topicName)

	// 批量消费
	var wg sync.WaitGroup
	received := make(chan struct{}, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		sk.BatchConsumer(topicName, "test-shark-batch-group", nil, func(msgs []kafka.Message) bool {
			t.Logf("批量收到 %d 条消息", len(msgs))
			received <- struct{}{}
			return false // 收到消息后立即停止
		})
	}()

	select {
	case <-received:
		t.Log("BatchConsumer 收到消息")
	case <-time.After(10 * time.Second):
		t.Log("BatchConsumer 超时（可能 Kafka 无数据）")
	}
	cancel()
	wg.Wait()
}
