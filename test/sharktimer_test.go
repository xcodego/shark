package test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lornshark/shark/sharktimer"
	"github.com/redis/go-redis/v9"
)

// mockTimerRedis 实现 sharktimer.TimerRedis 接口，用于单元测试。
type mockTimerRedis struct {
	mu   sync.Mutex
	data map[string]map[string]float64 // key → member → score
}

func newMockTimerRedis() *mockTimerRedis {
	return &mockTimerRedis{
		data: make(map[string]map[string]float64),
	}
}

func (m *mockTimerRedis) ZRangeByScoreWithScores(ctx context.Context, key string, opt *redis.ZRangeBy) *redis.ZSliceCmd {
	cmd := redis.NewZSliceCmd(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	members, ok := m.data[key]
	if !ok {
		cmd.SetVal([]redis.Z{})
		return cmd
	}
	var result []redis.Z
	for member, score := range members {
		// 简化处理：opt.Min/Max 不做解析，返回所有成员
		_ = opt
		result = append(result, redis.Z{Score: score, Member: member})
	}
	cmd.SetVal(result)
	return cmd
}

func (m *mockTimerRedis) ZRem(ctx context.Context, key string, members ...interface{}) *redis.IntCmd {
	cmd := redis.NewIntCmd(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	removed := int64(0)
	if set, ok := m.data[key]; ok {
		for _, member := range members {
			memberStr, ok := member.(string)
			if !ok {
				continue
			}
			if _, exists := set[memberStr]; exists {
				delete(set, memberStr)
				removed++
			}
		}
	}
	cmd.SetVal(removed)
	return cmd
}

func (m *mockTimerRedis) ZAdd(ctx context.Context, key string, members ...redis.Z) *redis.IntCmd {
	cmd := redis.NewIntCmd(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.data[key]; !ok {
		m.data[key] = make(map[string]float64)
	}
	added := int64(0)
	for _, z := range members {
		member, ok := z.Member.(string)
		if !ok {
			continue
		}
		m.data[key][member] = z.Score
		added++
	}
	cmd.SetVal(added)
	return cmd
}

func TestNewTimer(t *testing.T) {
	mockRedis := newMockTimerRedis()
	ctx, cancel := context.WithCancel(context.Background())
	// 立即取消，避免后台轮询线程持续运行
	cancel()
	timer := sharktimer.NewTimer(ctx, "test", "unit-timer", "inst-1", mockRedis)
	if timer == nil {
		t.Fatal("NewTimer 不应返回 nil")
	}
	// 等待轮询线程退出
	time.Sleep(50 * time.Millisecond)
}

func TestAddTimer(t *testing.T) {
	mockRedis := newMockTimerRedis()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	timer := sharktimer.NewTimer(ctx, "test", "unit-timer", "inst-1", mockRedis)

	var wg sync.WaitGroup
	wg.Add(1)
	triggered := false
	timerId := timer.AddTimer(50*time.Millisecond, func() {
		triggered = true
		wg.Done()
	})
	if timerId == "" {
		t.Fatal("AddTimer 应返回非空的定时器 ID")
	}
	t.Logf("定时器 ID: %s", timerId)

	// 等待回调触发
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		if !triggered {
			t.Error("回调未被触发")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("定时器回调超时未触发")
	}
}

func TestRemoveTimer(t *testing.T) {
	mockRedis := newMockTimerRedis()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	timer := sharktimer.NewTimer(ctx, "test", "unit-timer", "inst-1", mockRedis)

	var mu sync.Mutex
	triggered := false
	timerId := timer.AddTimer(500*time.Millisecond, func() {
		mu.Lock()
		triggered = true
		mu.Unlock()
	})
	if timerId == "" {
		t.Fatal("AddTimer 应返回非空的定时器 ID")
	}

	// 立即删除定时器
	timer.RemoveTimer(timerId)
	t.Logf("已删除定时器: %s", timerId)

	// 等待足够长的时间，确认回调不会被触发
	time.Sleep(1 * time.Second)
	mu.Lock()
	if triggered {
		t.Error("已删除的定时器不应触发回调")
	}
	mu.Unlock()
}

func TestAddTimeWithId(t *testing.T) {
	mockRedis := newMockTimerRedis()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	timer := sharktimer.NewTimer(ctx, "test", "unit-timer", "inst-1", mockRedis)

	var wg sync.WaitGroup
	wg.Add(1)
	triggered := false
	customId := "custom:timer:12345"
	timer.AddTimeWithId(customId, 50*time.Millisecond, func() {
		triggered = true
		wg.Done()
	})

	// 等待回调触发
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		if !triggered {
			t.Error("自定义 ID 的回调未被触发")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("自定义 ID 定时器回调超时未触发")
	}
}

func TestDefaultCallback(t *testing.T) {
	mockRedis := newMockTimerRedis()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	timer := sharktimer.NewTimer(ctx, "test", "unit-timer", "inst-1", mockRedis)

	var wg sync.WaitGroup
	wg.Add(1)
	var defaultTriggeredId string
	timer.DefaultCallback(func(timerId string) {
		defaultTriggeredId = timerId
		wg.Done()
	})

	// 添加一个没有回调的定时器（callback 为 nil）
	timerId := timer.AddTimer(50*time.Millisecond, nil)
	if timerId == "" {
		t.Fatal("AddTimer 应返回非空的定时器 ID")
	}
	t.Logf("无回调定时器 ID: %s", timerId)

	// 等待默认回调触发
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		if defaultTriggeredId != timerId {
			t.Errorf("默认回调参数错误: got %s, want %s", defaultTriggeredId, timerId)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("默认回调超时未触发")
	}
}

func TestRemoveTimerNonExistent(t *testing.T) {
	mockRedis := newMockTimerRedis()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	timer := sharktimer.NewTimer(ctx, "test", "unit-timer", "inst-1", mockRedis)

	// 删除不存在的定时器不应 panic
	timer.RemoveTimer("non-existent-timer-id")
	// 没有 panic 即为通过
}
