package test

import (
	"sync"
	"testing"

	"github.com/lornshark/shark/sharksnowflake"
)

func TestSnowflakeGenerate(t *testing.T) {
	sf := sharksnowflake.NewSnowflake()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Generate panic: %v", r)
		}
	}()
	id := sf.Generate()
	if id <= 0 {
		t.Errorf("生成的 ID 必须 > 0, got %d", id)
	}
	t.Logf("生成单个 ID: %d", id)
}

func TestSnowflakeUniqueness(t *testing.T) {
	sf := sharksnowflake.NewSnowflake()
	seen := make(map[int64]bool)
	n := 100000
	for i := 0; i < n; i++ {
		id := sf.Generate()
		if id <= 0 {
			t.Errorf("第 %d 个 ID 无效: %d", i, id)
			return
		}
		if seen[id] {
			t.Errorf("第 %d 个 ID 重复: %d", i, id)
			return
		}
		seen[id] = true
	}
	t.Logf("生成 %d 个唯一 ID 通过", n)
}

func TestSnowflakeConcurrent(t *testing.T) {
	sf := sharksnowflake.NewSnowflake()
	var wg sync.WaitGroup
	seen := sync.Map{}
	duplicate := false
	n := 10000
	workers := 10

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < n/workers; i++ {
				id := sf.Generate()
				if _, loaded := seen.LoadOrStore(id, true); loaded {
					duplicate = true
				}
			}
		}()
	}
	wg.Wait()

	if duplicate {
		t.Error("并发生成出现重复 ID")
	} else {
		t.Logf("并发 %d 协程 × %d 个 ID：无重复", workers, n)
	}
}

func TestSnowflakeMonotonic(t *testing.T) {
	sf := sharksnowflake.NewSnowflake()
	prev := int64(0)
	for i := 0; i < 1000; i++ {
		id := sf.Generate()
		if id <= prev {
			t.Errorf("ID 不是单调递增：prev=%d cur=%d i=%d", prev, id, i)
			return
		}
		prev = id
	}
	t.Log("1000 个 ID 单调递增通过")
}

func BenchmarkSnowflakeGenerate(b *testing.B) {
	sf := sharksnowflake.NewSnowflake()

	for b.Loop() {
		sf.Generate()
	}
}
