package test

import (
	"math/rand"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/xcodego/shark/sharkskiplist"
)

func TestSkipListSetGet(t *testing.T) {
	sl := sharkskiplist.NewAsc[int, string]()
	sl.SetOrUpdate(1, "one")
	sl.SetOrUpdate(2, "two")
	sl.SetOrUpdate(3, "three")

	if v, ok := sl.Get(2); !ok || v != "two" {
		t.Errorf("Get(2) = (%q, %v), want (two, true)", v, ok)
	}
	if _, ok := sl.Get(100); ok {
		t.Error("Get(100) 应未命中")
	}
}

func TestSkipListUpdate(t *testing.T) {
	sl := sharkskiplist.NewAsc[int, string]()
	sl.SetOrUpdate(1, "one")
	sl.SetOrUpdate(1, "uno")
	if v, _ := sl.Get(1); v != "uno" {
		t.Errorf("更新后 Get(1) = %q, want uno", v)
	}
	if n := sl.Len(); n != 1 {
		t.Errorf("Len = %d, want 1", n)
	}
}

func TestSkipListSetIfNotExists(t *testing.T) {
	sl := sharkskiplist.NewAsc[int, string]()

	if !sl.SetIfNotExists(1, "one") {
		t.Error("首次 SetIfNotExists 应成功")
	}
	if v, _ := sl.Get(1); v != "one" {
		t.Errorf("Get(1) = %q, want one", v)
	}

	if sl.SetIfNotExists(1, "uno") {
		t.Error("key 已存在时 SetIfNotExists 应返回 false")
	}
	if v, _ := sl.Get(1); v != "one" {
		t.Errorf("SetIfNotExists 失败后 Get(1) = %q, 不应被覆盖", v)
	}
	if n := sl.Len(); n != 1 {
		t.Errorf("Len = %d, want 1", n)
	}

	if !sl.SetIfNotExists(2, "two") {
		t.Error("新 key 的 SetIfNotExists 应成功")
	}
	if n := sl.Len(); n != 2 {
		t.Errorf("Len = %d, want 2", n)
	}
}

func TestSkipListDelete(t *testing.T) {
	sl := sharkskiplist.NewAsc[int, string]()
	sl.SetOrUpdate(1, "one")
	sl.SetOrUpdate(2, "two")
	if !sl.Delete(1) {
		t.Error("Delete(1) 应成功")
	}
	if _, ok := sl.Get(1); ok {
		t.Error("删除后 Get(1) 不应命中")
	}
	if sl.Delete(1) {
		t.Error("重复 Delete(1) 应返回 false")
	}
	if sl.Delete(999) {
		t.Error("Delete(999) 应返回 false")
	}
	if n := sl.Len(); n != 1 {
		t.Errorf("删除后 Len = %d, want 1", n)
	}
}

func TestSkipListContains(t *testing.T) {
	sl := sharkskiplist.NewAsc[int, string]()
	sl.SetOrUpdate(10, "ten")
	if !sl.Contains(10) {
		t.Error("Contains(10) 应为 true")
	}
	if sl.Contains(11) {
		t.Error("Contains(11) 应为 false")
	}
}

func TestSkipListRange(t *testing.T) {
	sl := sharkskiplist.NewAsc[int, int]()
	for _, k := range []int{5, 1, 4, 2, 3} {
		sl.SetOrUpdate(k, k*10)
	}

	var got []int
	sl.RangeAsc(func(key, value int) bool {
		got = append(got, key)
		return true
	})
	want := []int{1, 2, 3, 4, 5}
	if len(got) != len(want) {
		t.Fatalf("Range 元素数 = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Range[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestSkipListRangeDesc(t *testing.T) {
	sl := sharkskiplist.NewAsc[int, int]()
	for k := 1; k <= 5; k++ {
		sl.SetOrUpdate(k, k)
	}

	var got []int
	sl.RangeDesc(func(key, value int) bool {
		got = append(got, key)
		return true
	})
	want := []int{5, 4, 3, 2, 1}
	if len(got) != len(want) {
		t.Fatalf("RangeDesc 元素数 = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("RangeDesc[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestSkipListRangeBetween(t *testing.T) {
	sl := sharkskiplist.NewAsc[int, int]()
	for k := 1; k <= 10; k++ {
		sl.SetOrUpdate(k, k)
	}

	var got []int
	sl.RangeBetween(4, 7, func(key, value int) bool {
		got = append(got, key)
		return true
	})
	want := []int{4, 5, 6, 7}
	if len(got) != len(want) {
		t.Fatalf("RangeBetween 元素数 = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("RangeBetween[%d] = %d, want %d", i, got[i], want[i])
		}
	}

	// start > end 时按 key 降序遍历 [end, start]
	var got2 []int
	sl.RangeBetween(7, 4, func(key, value int) bool {
		got2 = append(got2, key)
		return true
	})
	want2 := []int{7, 6, 5, 4}
	if len(got2) != len(want2) {
		t.Fatalf("RangeBetween(7,4) 元素数 = %d, want %d", len(got2), len(want2))
	}
	for i := range want2 {
		if got2[i] != want2[i] {
			t.Fatalf("RangeBetween(7,4)[%d] = %d, want %d", i, got2[i], want2[i])
		}
	}

	// start == end 时仅遍历等于该 key 的元素
	var got3 []int
	sl.RangeBetween(5, 5, func(key, value int) bool {
		got3 = append(got3, key)
		return true
	})
	if len(got3) != 1 || got3[0] != 5 {
		t.Fatalf("RangeBetween(5,5) = %v, want [5]", got3)
	}
}

func TestSkipListRangeEarlyStop(t *testing.T) {
	sl := sharkskiplist.NewAsc[int, int]()
	for k := 1; k <= 10; k++ {
		sl.SetOrUpdate(k, k)
	}

	count := 0
	sl.RangeAsc(func(key, value int) bool {
		count++
		return key < 3 // key=3 时返回 false 停止
	})
	if count != 3 {
		t.Errorf("提前终止应遍历 3 个元素, got %d", count)
	}
}

func TestSkipListMinMax(t *testing.T) {
	sl := sharkskiplist.NewAsc[int, string]()
	sl.SetOrUpdate(3, "c")
	sl.SetOrUpdate(1, "a")
	sl.SetOrUpdate(2, "b")

	if k, v, ok := sl.Min(); !ok || k != 1 || v != "a" {
		t.Errorf("Min = (%d, %q, %v), want (1, a, true)", k, v, ok)
	}
	if k, v, ok := sl.Max(); !ok || k != 3 || v != "c" {
		t.Errorf("Max = (%d, %q, %v), want (3, c, true)", k, v, ok)
	}
}

func TestSkipListMinMaxEmpty(t *testing.T) {
	sl := sharkskiplist.NewAsc[int, string]()
	if _, _, ok := sl.Min(); ok {
		t.Error("空跳表 Min 应返回 false")
	}
	if _, _, ok := sl.Max(); ok {
		t.Error("空跳表 Max 应返回 false")
	}
}

func TestSkipListCeilingFloor(t *testing.T) {
	sl := sharkskiplist.NewAsc[int, string]()
	sl.SetOrUpdate(1, "a")
	sl.SetOrUpdate(3, "c")
	sl.SetOrUpdate(5, "e")

	if k, v, ok := sl.Ceiling(3); !ok || k != 3 || v != "c" {
		t.Errorf("Ceiling(3) = (%d, %q, %v), want (3, c, true)", k, v, ok)
	}
	if k, _, ok := sl.Ceiling(4); !ok || k != 5 {
		t.Errorf("Ceiling(4) key = %d, want 5", k)
	}
	if k, _, ok := sl.Ceiling(6); ok {
		t.Errorf("Ceiling(6) 应未命中, got key %d", k)
	}

	if k, v, ok := sl.Floor(3); !ok || k != 3 || v != "c" {
		t.Errorf("Floor(3) = (%d, %q, %v), want (3, c, true)", k, v, ok)
	}
	if k, _, ok := sl.Floor(4); !ok || k != 3 {
		t.Errorf("Floor(4) key = %d, want 3", k)
	}
	if k, _, ok := sl.Floor(0); ok {
		t.Errorf("Floor(0) 应未命中, got key %d", k)
	}
}

func TestSkipListKeysValues(t *testing.T) {
	sl := sharkskiplist.NewAsc[int, string]()
	sl.SetOrUpdate(2, "two")
	sl.SetOrUpdate(1, "one")
	sl.SetOrUpdate(3, "three")

	keys := sl.KeysAsc()
	wantKeys := []int{1, 2, 3}
	if len(keys) != len(wantKeys) {
		t.Fatalf("Keys 长度 = %d, want %d", len(keys), len(wantKeys))
	}
	for i := range wantKeys {
		if keys[i] != wantKeys[i] {
			t.Fatalf("Keys[%d] = %d, want %d", i, keys[i], wantKeys[i])
		}
	}

	values := sl.ValuesAsc()
	wantValues := []string{"one", "two", "three"}
	for i := range wantValues {
		if values[i] != wantValues[i] {
			t.Fatalf("Values[%d] = %q, want %q", i, values[i], wantValues[i])
		}
	}
}

func TestSkipListKeysValuesDesc(t *testing.T) {
	sl := sharkskiplist.NewAsc[int, string]()
	sl.SetOrUpdate(2, "two")
	sl.SetOrUpdate(1, "one")
	sl.SetOrUpdate(3, "three")

	keys := sl.KeysDesc()
	wantKeys := []int{3, 2, 1}
	if len(keys) != len(wantKeys) {
		t.Fatalf("KeysDesc 长度 = %d, want %d", len(keys), len(wantKeys))
	}
	for i := range wantKeys {
		if keys[i] != wantKeys[i] {
			t.Fatalf("KeysDesc[%d] = %d, want %d", i, keys[i], wantKeys[i])
		}
	}

	values := sl.ValuesDesc()
	wantValues := []string{"three", "two", "one"}
	for i := range wantValues {
		if values[i] != wantValues[i] {
			t.Fatalf("ValuesDesc[%d] = %q, want %q", i, values[i], wantValues[i])
		}
	}

	// 空跳表返回空切片（非 nil）
	empty := sharkskiplist.NewAsc[int, string]()
	if k := empty.KeysDesc(); len(k) != 0 {
		t.Errorf("空跳表 KeysDesc 长度 = %d, want 0", len(k))
	}
	if v := empty.ValuesDesc(); len(v) != 0 {
		t.Errorf("空跳表 ValuesDesc 长度 = %d, want 0", len(v))
	}
}

func TestSkipListClear(t *testing.T) {
	sl := sharkskiplist.NewAsc[int, int]()
	for k := 1; k <= 10; k++ {
		sl.SetOrUpdate(k, k)
	}
	sl.Clear()
	if n := sl.Len(); n != 0 {
		t.Errorf("Clear 后 Len = %d, want 0", n)
	}
	if sl.Contains(5) {
		t.Error("Clear 后不应包含任何元素")
	}
}

func TestSkipListWithComparator(t *testing.T) {
	// 降序比较器
	sl := sharkskiplist.NewWithComparator[int, string](
		func(a, b int) int {
			switch {
			case a > b:
				return -1
			case a < b:
				return 1
			default:
				return 0
			}
		},
	)
	sl.SetOrUpdate(1, "a")
	sl.SetOrUpdate(3, "c")
	sl.SetOrUpdate(2, "b")

	var got []int
	sl.RangeAsc(func(key int, value string) bool {
		got = append(got, key)
		return true
	})
	want := []int{3, 2, 1}
	if len(got) != len(want) {
		t.Fatalf("降序 Range 长度 = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("降序 Range[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestSkipListNilComparatorPanic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("nil 比较器应 panic")
		}
	}()
	_ = sharkskiplist.NewWithComparator[int, int](nil)
}

func TestSkipListLargeScale(t *testing.T) {
	const n = 100000
	sl := sharkskiplist.NewAsc[int, int]()
	perm := rand.Perm(n)
	for _, k := range perm {
		sl.SetOrUpdate(k, k*2)
	}

	if got := sl.Len(); got != n {
		t.Fatalf("Len = %d, want %d", got, n)
	}

	// 随机抽样验证
	for i := 0; i < 1000; i++ {
		k := rand.Intn(n)
		if v, ok := sl.Get(k); !ok || v != k*2 {
			t.Fatalf("Get(%d) = (%d, %v), want (%d, true)", k, v, ok, k*2)
		}
	}

	// 验证遍历严格升序
	prev := -1
	sl.RangeAsc(func(key, value int) bool {
		if key <= prev {
			t.Fatalf("遍历非严格升序: prev=%d, key=%d", prev, key)
		}
		if value != key*2 {
			t.Fatalf("value = %d, want %d", value, key*2)
		}
		prev = key
		return true
	})
}

func TestSkipListConcurrent(t *testing.T) {
	sl := sharkskiplist.NewAsc[int, int]()
	const goroutines = 8
	const perGoroutine = 2000

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			base := g * perGoroutine
			for i := 0; i < perGoroutine; i++ {
				sl.SetOrUpdate(base+i, base+i)
			}
			for i := 0; i < perGoroutine; i++ {
				if v, ok := sl.Get(base + i); !ok || v != base+i {
					t.Errorf("并发 Get(%d) = (%d, %v)", base+i, v, ok)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	if got := sl.Len(); got != goroutines*perGoroutine {
		t.Errorf("Len = %d, want %d", got, goroutines*perGoroutine)
	}
}

func TestSkipListIter(t *testing.T) {
	sl := sharkskiplist.NewAsc[int, int]()
	for k := 1; k <= 5; k++ {
		sl.SetOrUpdate(k, k*10)
	}

	it := sl.NewAscIter()
	defer it.Close()

	var got []int
	for {
		k, v, ok := it.Next()
		if !ok {
			break
		}
		if v != k*10 {
			t.Fatalf("Iter value = %d, want %d", v, k*10)
		}
		got = append(got, k)
	}
	want := []int{1, 2, 3, 4, 5}
	if len(got) != len(want) {
		t.Fatalf("Iter 元素数 = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Iter[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestSkipListIterDesc(t *testing.T) {
	sl := sharkskiplist.NewAsc[int, int]()
	for k := 1; k <= 5; k++ {
		sl.SetOrUpdate(k, k*10)
	}

	it := sl.NewDescIter()
	defer it.Close()

	var got []int
	for {
		k, v, ok := it.Next()
		if !ok {
			break
		}
		if v != k*10 {
			t.Fatalf("IterDesc value = %d, want %d", v, k*10)
		}
		got = append(got, k)
	}
	want := []int{5, 4, 3, 2, 1}
	if len(got) != len(want) {
		t.Fatalf("IterDesc 元素数 = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("IterDesc[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestSkipListIterCloseIdempotent(t *testing.T) {
	sl := sharkskiplist.NewAsc[int, string]()
	sl.SetOrUpdate(1, "one")

	it := sl.NewAscIter()
	it.Close()
	it.Close() // 幂等，不应 panic

	if _, _, ok := it.Next(); ok {
		t.Error("Close 后 Next 应返回 ok=false")
	}
}

func TestSkipListIterEmpty(t *testing.T) {
	sl := sharkskiplist.NewAsc[int, string]()

	it := sl.NewAscIter()
	defer it.Close()
	if _, _, ok := it.Next(); ok {
		t.Error("空跳表 Iter 首次 Next 应返回 false")
	}

	it2 := sl.NewDescIter()
	defer it2.Close()
	if _, _, ok := it2.Next(); ok {
		t.Error("空跳表 IterDesc 首次 Next 应返回 false")
	}
}

func TestSkipListIterHoldsLock(t *testing.T) {
	sl := sharkskiplist.NewAsc[int, int]()
	sl.SetOrUpdate(1, 1)

	it := sl.NewAscIter()

	done := make(chan struct{})
	go func() {
		sl.SetOrUpdate(2, 2) // 写操作应被迭代器持有的读锁阻塞
		close(done)
	}()

	select {
	case <-done:
		t.Error("迭代器持锁期间写操作不应完成")
	case <-time.After(50 * time.Millisecond):
		// 符合预期：写操作被阻塞
	}

	it.Close()

	select {
	case <-done:
		// Close 后写操作完成
	case <-time.After(time.Second):
		t.Error("Close 后写操作应能完成")
	}
}

func TestSkipListNewDesc(t *testing.T) {
	sl := sharkskiplist.NewDesc[int, string]()
	for _, k := range []int{5, 1, 4, 2, 3} {
		sl.SetOrUpdate(k, "v")
	}

	// 点操作语义不变
	if v, ok := sl.Get(3); !ok || v != "v" {
		t.Errorf("Get(3) = (%q, %v), want (v, true)", v, ok)
	}
	if !sl.Contains(5) || sl.Contains(99) {
		t.Error("Contains 语义错误")
	}

	// Min/Max 仍返回自然最小/最大
	if k, _, ok := sl.Min(); !ok || k != 1 {
		t.Errorf("Min = %d, want 1", k)
	}
	if k, _, ok := sl.Max(); !ok || k != 5 {
		t.Errorf("Max = %d, want 5", k)
	}

	// RangeAsc 自然升序
	var asc []int
	sl.RangeAsc(func(k int, v string) bool { asc = append(asc, k); return true })
	if !slices.Equal(asc, []int{1, 2, 3, 4, 5}) {
		t.Errorf("RangeAsc = %v, want [1 2 3 4 5]", asc)
	}

	// RangeDesc 自然降序
	var desc []int
	sl.RangeDesc(func(k int, v string) bool { desc = append(desc, k); return true })
	if !slices.Equal(desc, []int{5, 4, 3, 2, 1}) {
		t.Errorf("RangeDesc = %v, want [5 4 3 2 1]", desc)
	}

	// Keys
	if got := sl.KeysAsc(); !slices.Equal(got, []int{1, 2, 3, 4, 5}) {
		t.Errorf("KeysAsc = %v, want [1 2 3 4 5]", got)
	}
	if got := sl.KeysDesc(); !slices.Equal(got, []int{5, 4, 3, 2, 1}) {
		t.Errorf("KeysDesc = %v, want [5 4 3 2 1]", got)
	}

	// 迭代器
	itDesc := sl.NewDescIter()
	var iterDesc []int
	for {
		k, _, ok := itDesc.Next()
		if !ok {
			break
		}
		iterDesc = append(iterDesc, k)
	}
	itDesc.Close()
	if !slices.Equal(iterDesc, []int{5, 4, 3, 2, 1}) {
		t.Errorf("NewDescIter = %v, want [5 4 3 2 1]", iterDesc)
	}

	itAsc := sl.NewAscIter()
	var iterAsc []int
	for {
		k, _, ok := itAsc.Next()
		if !ok {
			break
		}
		iterAsc = append(iterAsc, k)
	}
	itAsc.Close()
	if !slices.Equal(iterAsc, []int{1, 2, 3, 4, 5}) {
		t.Errorf("NewAscIter = %v, want [1 2 3 4 5]", iterAsc)
	}

	// RangeBetween：端点决定方向
	var rb []int
	sl.RangeBetween(2, 4, func(k int, v string) bool { rb = append(rb, k); return true })
	if !slices.Equal(rb, []int{2, 3, 4}) {
		t.Errorf("RangeBetween(2,4) = %v, want [2 3 4]", rb)
	}
	rb = rb[:0]
	sl.RangeBetween(4, 2, func(k int, v string) bool { rb = append(rb, k); return true })
	if !slices.Equal(rb, []int{4, 3, 2}) {
		t.Errorf("RangeBetween(4,2) = %v, want [4 3 2]", rb)
	}

	// Ceiling/Floor 语义不变
	if k, _, ok := sl.Ceiling(2); !ok || k != 2 {
		t.Errorf("Ceiling(2) = %d, want 2", k)
	}
	if k, _, ok := sl.Floor(4); !ok || k != 4 {
		t.Errorf("Floor(4) = %d, want 4", k)
	}
}

func TestSkipListNewDescMinMaxEmpty(t *testing.T) {
	sl := sharkskiplist.NewDesc[int, string]()
	if _, _, ok := sl.Min(); ok {
		t.Error("空 NewDesc Min 应返回 false")
	}
	if _, _, ok := sl.Max(); ok {
		t.Error("空 NewDesc Max 应返回 false")
	}
}

func TestSkipListNewDescWithComparator(t *testing.T) {
	sl := sharkskiplist.NewDescWithComparator[int, string](
		func(a, b int) int {
			if a < b {
				return -1
			}
			if a > b {
				return 1
			}
			return 0
		},
	)
	sl.SetOrUpdate(3, "c")
	sl.SetOrUpdate(1, "a")
	sl.SetOrUpdate(2, "b")

	if k, _, ok := sl.Max(); !ok || k != 3 {
		t.Errorf("Max = %d, want 3", k)
	}
	if k, _, ok := sl.Min(); !ok || k != 1 {
		t.Errorf("Min = %d, want 1", k)
	}

	var desc []int
	sl.RangeDesc(func(k int, v string) bool { desc = append(desc, k); return true })
	if !slices.Equal(desc, []int{3, 2, 1}) {
		t.Errorf("RangeDesc = %v, want [3 2 1]", desc)
	}
}
