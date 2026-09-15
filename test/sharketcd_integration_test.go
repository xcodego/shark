package test

import (
	"context"
	"testing"
	"time"

	"github.com/lornshark/shark/sharketcd"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func TestEtcdNew(t *testing.T) {
	cfg := loadEtcdConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := sharketcd.New(ctx, cfg)
	if err != nil {
		t.Fatalf("连接 etcd 失败: %v", err)
	}
	defer client.Close()
	t.Log("etcd 连接成功")

	// Put/Get
	kv := client.KV
	_, err = kv.Put(ctx, "/test/shark/etcd/key", "hello-etcd")
	if err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	resp, err := kv.Get(ctx, "/test/shark/etcd/key")
	if err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	if len(resp.Kvs) != 1 || string(resp.Kvs[0].Value) != "hello-etcd" {
		t.Errorf("Get 结果不匹配: %v", resp.Kvs)
	}

	// Delete
	_, err = kv.Delete(ctx, "/test/shark/etcd/key")
	if err != nil {
		t.Fatalf("Delete 失败: %v", err)
	}
	t.Log("etcd PUT/GET/DELETE 通过")
}

func TestEtcdWatch(t *testing.T) {
	cfg := loadEtcdConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := sharketcd.New(ctx, cfg)
	if err != nil {
		t.Fatalf("连接 etcd 失败: %v", err)
	}
	defer client.Close()

	// Watch
	watchCh := client.Watch(ctx, "/test/shark/etcd/watch", clientv3.WithPrefix())
	go func() {
		time.Sleep(100 * time.Millisecond)
		client.Put(ctx, "/test/shark/etcd/watch/key1", "value1")
	}()

	select {
	case resp := <-watchCh:
		if len(resp.Events) > 0 {
			t.Logf("Watch 收到事件: %v -> %v", resp.Events[0].Kv.Key, resp.Events[0].Kv.Value)
		}
	case <-time.After(5 * time.Second):
		t.Error("Watch 超时未收到事件")
	}

	client.Delete(ctx, "/test/shark/etcd/watch/key1")
}
