package sharkclickhouse

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"
)

func TestNewInvalidConfig(t *testing.T) {
	for _, cfg := range []*Config{nil, {}, {Host: []string{""}}} {
		if _, err := New(context.Background(), cfg); err == nil {
			t.Fatalf("New(%+v) succeeded, want error", cfg)
		}
	}
}

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

func TestNewCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New(ctx, &Config{Host: []string{"127.0.0.1:9000"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("New error = %v, want context.Canceled", err)
	}
}

func TestIntegration(t *testing.T) {
	host := os.Getenv("CLICKHOUSE_TEST_HOST")
	if host == "" {
		t.Skip("set CLICKHOUSE_TEST_HOST to run against ClickHouse")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := New(ctx, &Config{
		Host:     []string{host},
		User:     os.Getenv("CLICKHOUSE_TEST_USER"),
		Password: os.Getenv("CLICKHOUSE_TEST_PASSWORD"),
		Database: os.Getenv("CLICKHOUSE_TEST_DATABASE"),
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
