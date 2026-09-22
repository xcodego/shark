package test

import (
	"math/rand"
	"testing"

	"github.com/xcodego/shark/sharkskiplist"
)

const benchKeySpace = 1 << 20 // Set 类随机 key 空间

// newBenchSkipList 预置一个含 n 个连续 key(0..n-1) 的跳表，用于查找/遍历类基准。
func newBenchSkipList(n int) *sharkskiplist.SkipList[int, int] {
	sl := sharkskiplist.NewAsc[int, int]()
	for i := 0; i < n; i++ {
		sl.SetOrUpdate(i, i)
	}
	return sl
}

// 写入类 ----------------------------------------------------------------

func BenchmarkSkipListSetOrUpdate(b *testing.B) {
	sl := sharkskiplist.NewAsc[int, int]()
	b.ResetTimer()
	for b.Loop() {
		sl.SetOrUpdate(rand.Intn(benchKeySpace), 1)
	}
}

func BenchmarkSkipListSetOrUpdateSeq(b *testing.B) {
	sl := sharkskiplist.NewAsc[int, int]()
	i := 0
	b.ResetTimer()
	for b.Loop() {
		sl.SetOrUpdate(i, i)
		i++
	}
}

func BenchmarkSkipListSetIfNotExists(b *testing.B) {
	sl := sharkskiplist.NewAsc[int, int]()
	b.ResetTimer()
	for b.Loop() {
		sl.SetIfNotExists(rand.Intn(benchKeySpace), 1)
	}
}

// 查找类 ----------------------------------------------------------------

func BenchmarkSkipListGet(b *testing.B) {
	sl := newBenchSkipList(100000)
	b.ResetTimer()
	for b.Loop() {
		_, _ = sl.Get(rand.Intn(100000))
	}
}

func BenchmarkSkipListGetMiss(b *testing.B) {
	sl := newBenchSkipList(100000)
	b.ResetTimer()
	for b.Loop() {
		_, _ = sl.Get(100000 + rand.Intn(1000)) // 全部未命中，走到链表尾
	}
}

func BenchmarkSkipListContains(b *testing.B) {
	sl := newBenchSkipList(100000)
	b.ResetTimer()
	for b.Loop() {
		_ = sl.Contains(rand.Intn(100000))
	}
}

func BenchmarkSkipListCeiling(b *testing.B) {
	sl := newBenchSkipList(100000)
	b.ResetTimer()
	for b.Loop() {
		_, _, _ = sl.Ceiling(rand.Intn(100000))
	}
}

func BenchmarkSkipListFloor(b *testing.B) {
	sl := newBenchSkipList(100000)
	b.ResetTimer()
	for b.Loop() {
		_, _, _ = sl.Floor(rand.Intn(100000))
	}
}

func BenchmarkSkipListMin(b *testing.B) {
	sl := newBenchSkipList(100000)
	b.ResetTimer()
	for b.Loop() {
		_, _, _ = sl.Min()
	}
}

func BenchmarkSkipListMax(b *testing.B) {
	sl := newBenchSkipList(100000)
	b.ResetTimer()
	for b.Loop() {
		_, _, _ = sl.Max()
	}
}

// 删除类 ----------------------------------------------------------------

func BenchmarkSkipListDelete(b *testing.B) {
	sl := newBenchSkipList(100000)
	b.ResetTimer()
	for b.Loop() {
		sl.Delete(rand.Intn(100000))
	}
}

// 遍历类 ----------------------------------------------------------------

func BenchmarkSkipListRangeAsc(b *testing.B) {
	sl := newBenchSkipList(100000)
	b.ResetTimer()
	for b.Loop() {
		sl.RangeAsc(func(k, v int) bool { return true })
	}
}

func BenchmarkSkipListRangeDesc(b *testing.B) {
	sl := newBenchSkipList(100000)
	b.ResetTimer()
	for b.Loop() {
		sl.RangeDesc(func(k, v int) bool { return true })
	}
}

func BenchmarkSkipListRangeBetween(b *testing.B) {
	sl := newBenchSkipList(100000)
	b.ResetTimer()
	for b.Loop() {
		sl.RangeBetween(20000, 30000, func(k, v int) bool { return true })
	}
}

func BenchmarkSkipListKeysAsc(b *testing.B) {
	sl := newBenchSkipList(100000)
	b.ResetTimer()
	for b.Loop() {
		_ = sl.KeysAsc()
	}
}

func BenchmarkSkipListKeysDesc(b *testing.B) {
	sl := newBenchSkipList(100000)
	b.ResetTimer()
	for b.Loop() {
		_ = sl.KeysDesc()
	}
}

func BenchmarkSkipListIterAsc(b *testing.B) {
	sl := newBenchSkipList(100000)
	b.ResetTimer()
	for b.Loop() {
		it := sl.NewAscIter()
		for {
			_, _, ok := it.Next()
			if !ok {
				break
			}
		}
		it.Close()
	}
}

func BenchmarkSkipListIterDesc(b *testing.B) {
	sl := newBenchSkipList(100000)
	b.ResetTimer()
	for b.Loop() {
		it := sl.NewDescIter()
		for {
			_, _, ok := it.Next()
			if !ok {
				break
			}
		}
		it.Close()
	}
}

// 对比：内置 map ---------------------------------------------------------

func BenchmarkMapSet(b *testing.B) {
	m := make(map[int]int, benchKeySpace)
	b.ResetTimer()
	for b.Loop() {
		m[rand.Intn(benchKeySpace)] = 1
	}
}

func BenchmarkMapGet(b *testing.B) {
	m := make(map[int]int, 100000)
	for i := 0; i < 100000; i++ {
		m[i] = i
	}
	b.ResetTimer()
	for b.Loop() {
		_ = m[rand.Intn(100000)]
	}
}

// 原生降序：验证"原生方向最快" -------------------------------------------------

func BenchmarkSkipListNewDescMax(b *testing.B) {
	sl := sharkskiplist.NewDesc[int, int]()
	for i := 0; i < 100000; i++ {
		sl.SetOrUpdate(i, i)
	}
	b.ResetTimer()
	for b.Loop() {
		_, _, _ = sl.Max()
	}
}

func BenchmarkSkipListNewDescMin(b *testing.B) {
	sl := sharkskiplist.NewDesc[int, int]()
	for i := 0; i < 100000; i++ {
		sl.SetOrUpdate(i, i)
	}
	b.ResetTimer()
	for b.Loop() {
		_, _, _ = sl.Min()
	}
}

func BenchmarkSkipListNewDescRangeDesc(b *testing.B) {
	sl := sharkskiplist.NewDesc[int, int]()
	for i := 0; i < 100000; i++ {
		sl.SetOrUpdate(i, i)
	}
	b.ResetTimer()
	for b.Loop() {
		sl.RangeDesc(func(k, v int) bool { return true })
	}
}

func BenchmarkSkipListNewDescRangeAsc(b *testing.B) {
	sl := sharkskiplist.NewDesc[int, int]()
	for i := 0; i < 100000; i++ {
		sl.SetOrUpdate(i, i)
	}
	b.ResetTimer()
	for b.Loop() {
		sl.RangeAsc(func(k, v int) bool { return true })
	}
}

func BenchmarkSkipListNewDescGet(b *testing.B) {
	sl := sharkskiplist.NewDesc[int, int]()
	for i := 0; i < 100000; i++ {
		sl.SetOrUpdate(i, i)
	}
	b.ResetTimer()
	for b.Loop() {
		_, _ = sl.Get(rand.Intn(100000))
	}
}

func BenchmarkSkipListNewDescSetOrUpdate(b *testing.B) {
	sl := sharkskiplist.NewDesc[int, int]()
	b.ResetTimer()
	for b.Loop() {
		sl.SetOrUpdate(rand.Intn(benchKeySpace), 1)
	}
}

func BenchmarkSkipListNewDescKeysAsc(b *testing.B) {
	sl := sharkskiplist.NewDesc[int, int]()
	for i := 0; i < 100000; i++ {
		sl.SetOrUpdate(i, i)
	}
	b.ResetTimer()
	for b.Loop() {
		_ = sl.KeysAsc()
	}
}

func BenchmarkSkipListNewDescKeysDesc(b *testing.B) {
	sl := sharkskiplist.NewDesc[int, int]()
	for i := 0; i < 100000; i++ {
		sl.SetOrUpdate(i, i)
	}
	b.ResetTimer()
	for b.Loop() {
		_ = sl.KeysDesc()
	}
}

func BenchmarkSkipListNewDescIterAsc(b *testing.B) {
	sl := sharkskiplist.NewDesc[int, int]()
	for i := 0; i < 100000; i++ {
		sl.SetOrUpdate(i, i)
	}
	b.ResetTimer()
	for b.Loop() {
		it := sl.NewAscIter()
		for {
			_, _, ok := it.Next()
			if !ok {
				break
			}
		}
		it.Close()
	}
}

func BenchmarkSkipListNewDescIterDesc(b *testing.B) {
	sl := sharkskiplist.NewDesc[int, int]()
	for i := 0; i < 100000; i++ {
		sl.SetOrUpdate(i, i)
	}
	b.ResetTimer()
	for b.Loop() {
		it := sl.NewDescIter()
		for {
			_, _, ok := it.Next()
			if !ok {
				break
			}
		}
		it.Close()
	}
}

func BenchmarkSkipListNewDescRangeBetweenAsc(b *testing.B) {
	sl := sharkskiplist.NewDesc[int, int]()
	for i := 0; i < 100000; i++ {
		sl.SetOrUpdate(i, i)
	}
	b.ResetTimer()
	for b.Loop() {
		sl.RangeBetween(20000, 30000, func(k, v int) bool { return true })
	}
}

func BenchmarkSkipListNewDescRangeBetweenDesc(b *testing.B) {
	sl := sharkskiplist.NewDesc[int, int]()
	for i := 0; i < 100000; i++ {
		sl.SetOrUpdate(i, i)
	}
	b.ResetTimer()
	for b.Loop() {
		sl.RangeBetween(30000, 20000, func(k, v int) bool { return true })
	}
}
