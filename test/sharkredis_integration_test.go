package test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/lornshark/shark/sharkredis"
)

func TestRedisNewCluster(t *testing.T) {
	cfg := loadRedisConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cluster, err := sharkredis.NewCluster(ctx, cfg)
	if err != nil {
		// 非集群模式回退
		if strings.Contains(err.Error(), "cluster support disabled") {
			t.Log("Redis 不支持集群模式，回退单机测试")
			return
		}
		t.Fatalf("Redis 集群连接失败: %v", err)
	}
	defer cluster.Close()
	t.Log("Redis 集群模式连接成功")

	// SET/GET/DEL
	key := "test:sharkredis:cluster:key"
	if err := cluster.Set(ctx, key, "hello", 10*time.Second).Err(); err != nil {
		t.Fatalf("Set 失败: %v", err)
	}
	got, err := cluster.Get(ctx, key).Result()
	if err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	if got != "hello" {
		t.Errorf("Get = %s, want hello", got)
	}
	cluster.Del(ctx, key)
	t.Log("Cluster SET/GET/DEL 通过")

}

func TestRedisNewClient(t *testing.T) {
	cfg := loadRedisConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := sharkredis.NewClient(ctx, cfg)
	if err != nil {
		t.Fatalf("Redis 单机连接失败: %v", err)
	}
	defer client.Close()
	t.Log("Redis Client 连接成功")

	// SET/GET/DEL
	key := "test:sharkredis:client:key"
	if err := client.Set(ctx, key, "world", 10*time.Second).Err(); err != nil {
		t.Fatalf("Set 失败: %v", err)
	}
	got, err := client.Get(ctx, key).Result()
	if err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	if got != "world" {
		t.Errorf("Get = %s, want world", got)
	}
	client.Del(ctx, key)
	t.Log("Client SET/GET/DEL 通过")

	// TTL 测试
	client.Set(ctx, key, "expire", 2*time.Second)
	ttl, _ := client.TTL(ctx, key).Result()
	t.Logf("TTL = %v", ttl)
	client.Del(ctx, key)
}

// Benchmark Redis SET
func BenchmarkRedisSet(b *testing.B) {
	cfg := &sharkredis.Config{
		Host:     []string{"127.0.0.1:6379"},
		Password: "",
	}
	ctx := context.Background()
	client, err := sharkredis.NewClient(ctx, cfg)
	if err != nil {
		b.Skipf("Redis 不可用: %v", err)
	}
	defer client.Close()

	for b.Loop() {
		client.Set(ctx, "bench:key", "value", time.Minute)
	}
}

var _ = redis.NewClient // 确保 redis 包被引用
