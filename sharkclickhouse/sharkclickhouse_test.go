package sharkclickhouse

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// TestNewInvalidConfig 验证 nil、空配置和空 Host 地址都会返回错误。
func TestNewInvalidConfig(t *testing.T) {
	for _, cfg := range []*Config{nil, {}, {Host: []string{""}}} {
		if _, err := New(context.Background(), cfg); err == nil {
			t.Fatalf("New(%+v) succeeded, want error", cfg)
		}
	}
}

// TestNewConnectionFailure 验证连接不可用的地址（先监听再关闭得到）会返回错误。
func TestNewConnectionFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := New(ctx, &Config{Host: []string{addr}}); err == nil {
		t.Fatal("New succeeded for unavailable server")
	}
}

// TestNewCanceledContext 验证已取消的 ctx 会让 New 返回 context.Canceled。
func TestNewCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New(ctx, &Config{Host: []string{"127.0.0.1:9000"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("New error = %v, want context.Canceled", err)
	}
}

// TestIntegration 连接真实 ClickHouse 执行一条查询，依赖内网环境，环境不可达时会失败。
func TestIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := New(ctx, &Config{
		Host:     []string{"192.168.191.100:19000"},
		User:     "admin",
		Password: "CEki57pxTJyYaLD",
		Database: "kgame",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	}()
	var result uint8
	if err := client.QueryRow(ctx, "SELECT toUInt8(1)").Scan(&result); err != nil {
		t.Fatal(err)
	}
	if result != 1 {
		t.Fatalf("result = %d, want 1", result)
	}
}
