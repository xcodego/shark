package test

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/lornshark/shark/sharkcache"
)

type testItem struct {
	ID   int64
	Name string
}

func TestCacheGetHit(t *testing.T) {
	var dbCalled atomic.Bool
	cache := sharkcache.New(
		func(args ...any) (*testItem, error) {
			dbCalled.Store(true)
			return &testItem{ID: args[0].(int64), Name: "from-db"}, nil
		},
	)
	result, err := cache.Get(int64(1))
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if result.Name != "from-db" {
		t.Errorf("Name = %s, want from-db", result.Name)
	}
	if !dbCalled.Load() {
		t.Error("seeker 应该被调用")
	}
}

func TestCacheGetNotFound(t *testing.T) {
	cache := sharkcache.New(
		func(args ...any) (*testItem, error) {
			return nil, nil // seeker 返回 nil，表示未命中
		},
	)
	_, err := cache.Get(int64(1))
	if !errors.Is(err, sharkcache.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestCacheGetMultiSeeker(t *testing.T) {
	var firstCalled, secondCalled atomic.Bool
	cache := sharkcache.New(
		// seeker 0: 返回 nil（未命中）
		func(args ...any) (*testItem, error) {
			firstCalled.Store(true)
			return nil, nil
		},
		// seeker 1: 命中
		func(args ...any) (*testItem, error) {
			secondCalled.Store(true)
			return &testItem{ID: args[0].(int64), Name: "from-seeker2"}, nil
		},
	)
	result, err := cache.Get(int64(42))
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if result.Name != "from-seeker2" {
		t.Errorf("Name = %s, want from-seeker2", result.Name)
	}
	if !firstCalled.Load() {
		t.Error("seeker 0 应被调用")
	}
	if !secondCalled.Load() {
		t.Error("seeker 1 应被调用")
	}
}

func TestCacheGetErrorSkip(t *testing.T) {
	var dbCalled atomic.Bool
	cache := sharkcache.New(
		// seeker 0: 返回错误（如 Redis 连不上）
		func(args ...any) (*testItem, error) {
			return nil, errors.New("redis connection failed")
		},
		// seeker 1: 回退到数据库
		func(args ...any) (*testItem, error) {
			dbCalled.Store(true)
			return &testItem{ID: args[0].(int64), Name: "from-db"}, nil
		},
	)
	result, err := cache.Get(int64(1))
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if !dbCalled.Load() {
		t.Error("seeker 0 出错后应回退到 seeker 1")
	}
	if result.Name != "from-db" {
		t.Errorf("Name = %s, want from-db", result.Name)
	}
}

func TestCacheGetAllSeekersFail(t *testing.T) {
	cache := sharkcache.New(
		func(args ...any) (*testItem, error) { return nil, errors.New("err1") },
		func(args ...any) (*testItem, error) { return nil, errors.New("err2") },
	)
	_, err := cache.Get(int64(1))
	if !errors.Is(err, sharkcache.ErrNotFound) {
		t.Errorf("所有 seeker 失败应返回 ErrNotFound, got: %v", err)
	}
}

func TestCacheMultipleArgs(t *testing.T) {
	cache := sharkcache.New(
		func(args ...any) (*testItem, error) {
			id := args[0].(int64)
			_ = args[1].(int64) // orgID
			_ = args[2].(int)   // status
			return &testItem{ID: id, Name: "OK"}, nil
		},
	)
	result, err := cache.Get(int64(1), int64(100), 1)
	if err != nil {
		t.Fatalf("Get with multiple args error: %v", err)
	}
	if result.Name != "OK" {
		t.Errorf("Name = %s", result.Name)
	}
}
