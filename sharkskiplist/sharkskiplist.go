// Package sharkskiplist 提供基于跳表（Skip List）的有序映射组件。
//
// 跳表是一种概率型数据结构，通过多层索引在有序链表上实现平均
// O(log n) 的查找、插入和删除，且支持高效的有序遍历与范围查询。
//
// 设计特点：
//  1. 泛型实现：SkipList[K, V] 支持任意 key/value 类型，
//     默认使用 cmp.Ordered 比较，也支持传入自定义比较器。
//  2. 并发安全：所有公开方法内部使用 sync.RWMutex 加锁，
//     多个 goroutine 可以安全地并发读写。
//  3. 随机层数：p = 1/4，最大层数 32，足够支撑千万级元素。
//  4. 有序遍历：Range / RangeDesc / RangeBetween 按 key 顺序回调，
//     回调返回 false 可提前终止遍历。
//
// 典型使用场景：有序排行榜、区间查询、需要按 key 排序的内存缓存等。
//
// 注意：Range 系列回调函数在持有读锁期间同步执行，回调内不得再调用
// 会加锁的方法（如 Set/Delete），否则会造成死锁。
package sharkskiplist

import (
	"cmp"
	"math/rand/v2"
	"slices"
	"sync"
)

const (
	// maxLevel 是跳表的最大层数。层数越高索引越稀疏，查询效率越高，
	// 32 层足以支撑 4^(32) 量级的元素数量。
	maxLevel = 32

	// probability 是节点随机晋升到下一层的概率（p = 1/4）。
	// 期望层数约为 1/(1-p) ≈ 1.33，查询复杂度期望为 O(log n)。
	probability = 0.25
)

// node 是跳表内部节点。
// 每个节点持有 key/value 以及指向各层下一个节点的指针切片。
// next[i] 表示第 i 层（从 0 开始）的下一个节点，nil 表示该层末尾。
type node[K any, V any] struct {
	key   K
	value V
	next  []*node[K, V]
}

// SkipList 是基于跳表实现的有序映射。
//
// 它维护一个按 key 升序排列的元素集合，key 必须唯一（重复 Set 会覆盖）。
// 内部通过一个"头节点"（head，不存储业务数据）和多层前向指针组织数据。
//
// 零值 SkipList 不可直接使用，必须通过 New 或 NewWithComparator 创建。
type SkipList[K any, V any] struct {
	mu   sync.RWMutex     // 读写锁，保护并发访问
	head *node[K, V]      // 头节点，next 长度固定为 maxLevel
	size int              // 当前元素数量
	cmp  func(a, b K) int // 比较器：负数表示 a<b，0 表示相等，正数表示 a>b
}

// New 创建一个使用默认升序比较的跳表。
//
// K 必须是可比较的有序类型（如 int、int64、string、float64 等，
// 满足 cmp.Ordered 约束），内部使用 cmp.Compare 进行比较。
//
// 使用示例：
//
//	sl := sharkskiplist.New[int, string]()
//	sl.SetOrUpdate(1, "one")
//	sl.SetOrUpdate(2, "two")
//	v, ok := sl.Get(1) // "one", true
func New[K cmp.Ordered, V any]() *SkipList[K, V] {
	return NewWithComparator[K, V](cmp.Compare)
}

// NewWithComparator 使用自定义比较器创建一个跳表。
//
// compare 用于比较两个 key 的大小：返回负数表示 a < b，0 表示相等，
// 正数表示 a > b。通过自定义比较器可以实现降序、按结构体某字段排序等
// 非默认顺序。
//
// compare 为 nil 时会 panic。
//
// 使用示例：
//
//	// 按字符串长度排序
//	sl := sharkskiplist.NewWithComparator[string, int](
//	    func(a, b string) int {
//	        if len(a) < len(b) { return -1 }
//	        if len(a) > len(b) { return 1 }
//	        return 0
//	    },
//	)
func NewWithComparator[K any, V any](compare func(a, b K) int) *SkipList[K, V] {
	if compare == nil {
		panic("sharkskiplist: comparator must not be nil")
	}
	return &SkipList[K, V]{
		head: &node[K, V]{next: make([]*node[K, V], maxLevel)},
		cmp:  compare,
	}
}

// randomLevel 随机生成新节点的层数。
//
// 从第 1 层开始，每次以 probability 的概率晋升一层，直到达到 maxLevel。
// 该函数使用 math/rand/v2 的顶层函数（并发安全、自动播种），
// 因此无需额外加锁，也不会引入共享可变状态。
func randomLevel() int {
	level := 1
	for level < maxLevel && rand.Float64() < probability {
		level++
	}
	return level
}

// findPredecessors 从最高层向下搜索，返回 key 对应的节点前驱信息。
//
// 返回值 candidate 是第 0 层上 key 的"前驱节点的下一个节点"：
//   - 若 candidate.key == key，表示命中，candidate 即目标节点；
//   - 否则 candidate 是第一个 key 更大的节点（或 nil），表示未命中。
//
// 若 preds 非空（长度必须为 maxLevel），则在搜索过程中把每一层的
// 前驱节点写入 preds[i]，供插入/删除时修改指针使用。
func (sl *SkipList[K, V]) findPredecessors(key K, preds []*node[K, V]) *node[K, V] {
	x := sl.head
	for i := maxLevel - 1; i >= 0; i-- {
		for x.next[i] != nil && sl.cmp(x.next[i].key, key) < 0 {
			x = x.next[i]
		}
		if preds != nil {
			preds[i] = x
		}
	}
	return x.next[0]
}

// SetOrUpdate 插入或更新一个 key-value 对。
//
// 若 key 不存在则插入新节点；若 key 已存在则覆盖其 value。
// 时间复杂度期望 O(log n)。
//
// 使用示例：
//
//	sl := sharkskiplist.New[int, string]()
//	sl.SetOrUpdate(1, "one")
//	sl.SetOrUpdate(1, "uno") // 覆盖 "one"
func (sl *SkipList[K, V]) SetOrUpdate(key K, value V) {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	preds := make([]*node[K, V], maxLevel)
	candidate := sl.findPredecessors(key, preds)

	// 已存在相同 key，直接覆盖 value
	if candidate != nil && sl.cmp(candidate.key, key) == 0 {
		candidate.value = value
		return
	}

	sl.insert(key, value, preds)
}

// SetIfNotExists 仅当 key 不存在时才设置 value。
//
// 若 key 已存在，则不做任何修改并返回 false；否则插入新节点并返回 true。
// 该语义等价于 Redis 的 SETNX（set if not exists），适合分布式锁、幂等占位
// 等"先到先得"的场景。时间复杂度期望 O(log n)。
//
// 使用示例：
//
//	sl := sharkskiplist.New[int, string]()
//	ok := sl.SetIfNotExists(1, "one") // true，首次设置成功
//	ok = sl.SetIfNotExists(1, "uno")  // false，key 已存在，未覆盖
//	v, _ := sl.Get(1)              // "one"
func (sl *SkipList[K, V]) SetIfNotExists(key K, value V) bool {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	preds := make([]*node[K, V], maxLevel)
	candidate := sl.findPredecessors(key, preds)
	if candidate != nil && sl.cmp(candidate.key, key) == 0 {
		return false
	}

	sl.insert(key, value, preds)
	return true
}

// insert 在已计算好的各层前驱 preds 基础上插入新节点。
//
// 调用方必须已持有写锁，且已确认 key 不存在。随机生成层数后，
// 在每层把新节点接在其前驱之后，并递增元素计数。
func (sl *SkipList[K, V]) insert(key K, value V, preds []*node[K, V]) {
	level := randomLevel()
	n := &node[K, V]{
		key:   key,
		value: value,
		next:  make([]*node[K, V], level),
	}
	for i := 0; i < level; i++ {
		n.next[i] = preds[i].next[i]
		preds[i].next[i] = n
	}
	sl.size++
}

// Get 根据 key 查找 value。
//
// 第二个返回值表示是否命中：命中返回 (value, true)，否则返回 (零值, false)。
// 时间复杂度期望 O(log n)。
//
// 使用示例：
//
//	sl := sharkskiplist.New[int, string]()
//	sl.SetOrUpdate(1, "one")
//	if v, ok := sl.Get(1); ok {
//	    fmt.Println(v) // one
//	}
func (sl *SkipList[K, V]) Get(key K) (V, bool) {
	sl.mu.RLock()
	defer sl.mu.RUnlock()

	var zero V
	candidate := sl.findPredecessors(key, nil)
	if candidate == nil || sl.cmp(candidate.key, key) != 0 {
		return zero, false
	}
	return candidate.value, true
}

// Delete 删除指定 key，返回是否成功删除（key 不存在时返回 false）。
// 时间复杂度期望 O(log n)。
func (sl *SkipList[K, V]) Delete(key K) bool {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	preds := make([]*node[K, V], maxLevel)
	candidate := sl.findPredecessors(key, preds)
	if candidate == nil || sl.cmp(candidate.key, key) != 0 {
		return false
	}

	// 在各层把前驱指向候选节点的下一节点，从而摘除该节点
	for i := 0; i < len(candidate.next); i++ {
		preds[i].next[i] = candidate.next[i]
	}
	candidate.next = nil // 断开引用，便于 GC 回收
	sl.size--
	return true
}

// Contains 判断 key 是否存在。
func (sl *SkipList[K, V]) Contains(key K) bool {
	_, ok := sl.Get(key)
	return ok
}

// Len 返回当前元素数量。时间复杂度 O(1)。
func (sl *SkipList[K, V]) Len() int {
	sl.mu.RLock()
	defer sl.mu.RUnlock()
	return sl.size
}

// Clear 清空跳表中的所有元素。时间复杂度 O(1)。
func (sl *SkipList[K, V]) Clear() {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	sl.head.next = make([]*node[K, V], maxLevel)
	sl.size = 0
}

// Min 返回 key 最小的元素（首元素）。空跳表时返回 (零值, 零值, false)。
func (sl *SkipList[K, V]) Min() (K, V, bool) {
	sl.mu.RLock()
	defer sl.mu.RUnlock()

	var zeroK K
	var zeroV V
	first := sl.head.next[0]
	if first == nil {
		return zeroK, zeroV, false
	}
	return first.key, first.value, true
}

// Max 返回 key 最大的元素（末元素）。空跳表时返回 (零值, 零值, false)。
func (sl *SkipList[K, V]) Max() (K, V, bool) {
	sl.mu.RLock()
	defer sl.mu.RUnlock()

	var zeroK K
	var zeroV V
	// 从最高层沿最右侧一路走到底，x 即为最大 key 的节点
	x := sl.head
	for i := maxLevel - 1; i >= 0; i-- {
		for x.next[i] != nil {
			x = x.next[i]
		}
	}
	if x == sl.head {
		return zeroK, zeroV, false
	}
	return x.key, x.value, true
}

// Ceiling 返回 key 大于等于给定 key 的最小元素（"下限"）。
// 不存在时返回 (零值, 零值, false)。
//
// 使用示例：
//
//	sl.SetOrUpdate(1, "a"); sl.SetOrUpdate(3, "c")
//	k, v, ok := sl.Ceiling(2) // 3, "c", true
func (sl *SkipList[K, V]) Ceiling(key K) (K, V, bool) {
	sl.mu.RLock()
	defer sl.mu.RUnlock()

	var zeroK K
	var zeroV V
	candidate := sl.findPredecessors(key, nil)
	if candidate == nil {
		return zeroK, zeroV, false
	}
	return candidate.key, candidate.value, true
}

// Floor 返回 key 小于等于给定 key 的最大元素（"上限"）。
// 不存在时返回 (零值, 零值, false)。
//
// 使用示例：
//
//	sl.SetOrUpdate(1, "a"); sl.SetOrUpdate(3, "c")
//	k, v, ok := sl.Floor(2) // 1, "a", true
func (sl *SkipList[K, V]) Floor(key K) (K, V, bool) {
	sl.mu.RLock()
	defer sl.mu.RUnlock()

	var zeroK K
	var zeroV V
	preds := make([]*node[K, V], maxLevel)
	candidate := sl.findPredecessors(key, preds)
	if candidate != nil && sl.cmp(candidate.key, key) == 0 {
		return candidate.key, candidate.value, true
	}
	// 未命中时 preds[0] 是 key 的前驱节点，即小于 key 的最大节点
	prev := preds[0]
	if prev == sl.head {
		return zeroK, zeroV, false
	}
	return prev.key, prev.value, true
}

// RangeAsc 按 key 升序遍历所有元素，并对每个元素调用 fn。
//
// fn 返回 false 可提前终止遍历。遍历过程中持有读锁，回调内不得调用
// 本实例的写方法（Set/Delete/Clear），否则会死锁。
//
// 使用示例：
//
//	sl.RangeAsc(func(key int, value string) bool {
//	    fmt.Println(key, value)
//	    return true // 返回 false 则停止遍历
//	})
func (sl *SkipList[K, V]) RangeAsc(fn func(key K, value V) bool) {
	sl.mu.RLock()
	defer sl.mu.RUnlock()

	for x := sl.head.next[0]; x != nil; x = x.next[0] {
		if !fn(x.key, x.value) {
			return
		}
	}
}

// RangeDesc 按 key 降序遍历所有元素，并对每个元素调用 fn。
//
// fn 返回 false 可提前终止遍历。实现上先收集元素快照再倒序回调，
// 因此相比 Range 需要 O(n) 的额外内存。遍历持有读锁，回调内不得调用
// 本实例的写方法。
func (sl *SkipList[K, V]) RangeDesc(fn func(key K, value V) bool) {
	sl.mu.RLock()
	keys := make([]K, 0, sl.size)
	values := make([]V, 0, sl.size)
	for x := sl.head.next[0]; x != nil; x = x.next[0] {
		keys = append(keys, x.key)
		values = append(values, x.value)
	}
	sl.mu.RUnlock()

	for i := len(keys) - 1; i >= 0; i-- {
		if !fn(keys[i], values[i]) {
			return
		}
	}
}

// RangeBetween 遍历 [start, end] 闭区间内的元素，并调用 fn。
//
// 遍历方向由端点顺序决定：
//   - start < end：按 key 升序遍历 [start, end]；
//   - start > end：按 key 降序遍历 [end, start]；
//   - start == end：仅遍历等于该 key 的元素（若存在）。
//
// fn 返回 false 可提前终止遍历。遍历持有读锁，回调内不得调用本实例的写方法。
//
// 使用示例：
//
//	sl.RangeBetween(10, 20, func(key int, value string) bool {
//	    fmt.Println(key, value) // 升序: 10, 11, ..., 20
//	    return true
//	})
//	sl.RangeBetween(20, 10, fn) // 降序: 20, 19, ..., 10
func (sl *SkipList[K, V]) RangeBetween(start, end K, fn func(key K, value V) bool) {
	sl.mu.RLock()
	defer sl.mu.RUnlock()

	if sl.cmp(start, end) > 0 {
		sl.rangeBetweenDesc(start, end, fn)
		return
	}
	sl.rangeBetweenAsc(start, end, fn)
}

// rangeBetweenAsc 按升序遍历 [start, end] 闭区间（要求 start <= end）。
// 调用方必须已持有读锁。
func (sl *SkipList[K, V]) rangeBetweenAsc(start, end K, fn func(key K, value V) bool) {
	// 定位第一个 >= start 的节点
	x := sl.head
	for i := maxLevel - 1; i >= 0; i-- {
		for x.next[i] != nil && sl.cmp(x.next[i].key, start) < 0 {
			x = x.next[i]
		}
	}
	for x = x.next[0]; x != nil; x = x.next[0] {
		if sl.cmp(x.key, end) > 0 {
			return
		}
		if !fn(x.key, x.value) {
			return
		}
	}
}

// rangeBetweenDesc 按降序遍历 [end, start] 闭区间（要求 start > end）。
// 跳表仅维护前向（升序）指针，故先收集区间内节点的升序快照，再倒序回调。
// 调用方必须已持有读锁。
func (sl *SkipList[K, V]) rangeBetweenDesc(start, end K, fn func(key K, value V) bool) {
	// 定位第一个 >= end（降序区间的下界）的节点
	x := sl.head
	for i := maxLevel - 1; i >= 0; i-- {
		for x.next[i] != nil && sl.cmp(x.next[i].key, end) < 0 {
			x = x.next[i]
		}
	}

	// 收集 [end, start] 区间内的节点（升序）
	nodes := make([]*node[K, V], 0)
	for x = x.next[0]; x != nil; x = x.next[0] {
		if sl.cmp(x.key, start) > 0 {
			break
		}
		nodes = append(nodes, x)
	}

	// 倒序回调，实现降序遍历
	for i := len(nodes) - 1; i >= 0; i-- {
		if !fn(nodes[i].key, nodes[i].value) {
			return
		}
	}
}

// KeysAsc 返回按 key 升序排列的所有 key 的副本。
func (sl *SkipList[K, V]) KeysAsc() []K {
	sl.mu.RLock()
	defer sl.mu.RUnlock()

	keys := make([]K, 0, sl.size)
	for x := sl.head.next[0]; x != nil; x = x.next[0] {
		keys = append(keys, x.key)
	}
	return keys
}

// ValuesAsc 返回按 key 升序排列的所有 value 的副本。
func (sl *SkipList[K, V]) ValuesAsc() []V {
	sl.mu.RLock()
	defer sl.mu.RUnlock()

	values := make([]V, 0, sl.size)
	for x := sl.head.next[0]; x != nil; x = x.next[0] {
		values = append(values, x.value)
	}
	return values
}

// KeysDesc 返回按 key 降序排列的所有 key 的副本。
//
// 跳表仅维护前向（升序）指针，因此先按升序收集后再反转。
// 时间复杂度 O(n)，需要 O(n) 额外内存。
//
// 使用示例：
//
//	sl := sharkskiplist.New[int, string]()
//	sl.SetOrUpdate(1, "a"); sl.SetOrUpdate(2, "b"); sl.SetOrUpdate(3, "c")
//	keys := sl.KeysDesc() // [3, 2, 1]
func (sl *SkipList[K, V]) KeysDesc() []K {
	sl.mu.RLock()
	defer sl.mu.RUnlock()

	keys := make([]K, 0, sl.size)
	for x := sl.head.next[0]; x != nil; x = x.next[0] {
		keys = append(keys, x.key)
	}
	slices.Reverse(keys)
	return keys
}

// ValuesDesc 返回按 key 降序排列的所有 value 的副本。
//
// 与 KeysDesc 类似，先按升序收集 value 再反转，从而得到与 key 降序
// 一致的值序列。时间复杂度 O(n)，需要 O(n) 额外内存。
//
// 使用示例：
//
//	sl := sharkskiplist.New[int, string]()
//	sl.SetOrUpdate(1, "a"); sl.SetOrUpdate(2, "b"); sl.SetOrUpdate(3, "c")
//	values := sl.ValuesDesc() // ["c", "b", "a"]
func (sl *SkipList[K, V]) ValuesDesc() []V {
	sl.mu.RLock()
	defer sl.mu.RUnlock()

	values := make([]V, 0, sl.size)
	for x := sl.head.next[0]; x != nil; x = x.next[0] {
		values = append(values, x.value)
	}
	slices.Reverse(values)
	return values
}

// Iter 是 SkipList 的迭代器，用于在一次持锁期间按序遍历元素。
//
// 迭代器在创建（NewAscIter / NewDescIter）时持有跳表的读锁，直到调用 Close
// 才释放。因此在遍历期间，其他 goroutine 的写操作（SetOrUpdate / SetIfNotExists
// / Delete / Clear）会被阻塞。使用完毕后务必调用 Close 释放锁，否则会造成
// 锁泄漏，进而导致后续写操作永久阻塞。
//
// 正向迭代通过前向指针逐节点前进，额外内存 O(1)；反向迭代由于跳表仅维护
// 前向指针，创建时先收集升序节点快照再倒序返回，额外内存 O(n)。
//
// 推荐用法（defer Close 确保锁被释放）：
//
//	it := sl.NewAscIter()
//	defer it.Close()
//	for {
//	    k, v, ok := it.Next()
//	    if !ok {
//	        break
//	    }
//	    fmt.Println(k, v)
//	}
type Iter[K any, V any] struct {
	sl      *SkipList[K, V] // 所属跳表，Close 时用于释放读锁
	cur     *node[K, V]     // 正向迭代：下一个要返回的节点
	nodes   []*node[K, V]   // 反向迭代：升序节点快照
	idx     int             // 反向迭代：下一个要返回的快照索引（从尾到头）
	reverse bool            // 是否为反向迭代
	closed  bool            // 是否已调用 Close 释放锁
}

// NewAscIter 创建一个正向迭代器，按 key 升序遍历所有元素。
//
// 调用后即持有跳表的读锁，必须配合 Close 释放。遍历过程中对跳表的写操作
// 会被阻塞，因此请尽快完成遍历并 Close。
//
// 使用示例：
//
//	it := sl.NewAscIter()
//	defer it.Close()
//	for k, v, ok := it.Next(); ok; k, v, ok = it.Next() {
//	    fmt.Println(k, v)
//	}
func (sl *SkipList[K, V]) NewAscIter() *Iter[K, V] {
	sl.mu.RLock()
	return &Iter[K, V]{
		sl:  sl,
		cur: sl.head.next[0],
	}
}

// NewDescIter 创建一个反向迭代器，按 key 降序遍历所有元素。
//
// 与 NewAscIter 一样持有读锁（必须 Close 释放）。由于跳表仅维护前向指针，
// 创建时会收集全部节点的升序快照（O(n) 内存），再倒序返回。
//
// 使用示例：
//
//	it := sl.NewDescIter()
//	defer it.Close()
//	for {
//	    k, v, ok := it.Next()
//	    if !ok {
//	        break
//	    }
//	    fmt.Println(k, v)
//	}
func (sl *SkipList[K, V]) NewDescIter() *Iter[K, V] {
	sl.mu.RLock()
	nodes := make([]*node[K, V], 0, sl.size)
	for x := sl.head.next[0]; x != nil; x = x.next[0] {
		nodes = append(nodes, x)
	}
	return &Iter[K, V]{
		sl:      sl,
		nodes:   nodes,
		idx:     len(nodes) - 1,
		reverse: true,
	}
}

// Next 返回下一个元素。
//
// 返回值：(key, value, ok)。ok 为 true 表示取到元素；ok 为 false 表示遍历
// 结束或迭代器已 Close。迭代器在 Close 之后调用 Next 始终返回零值。
func (it *Iter[K, V]) Next() (K, V, bool) {
	var zeroK K
	var zeroV V
	if it.closed {
		return zeroK, zeroV, false
	}

	if it.reverse {
		if it.idx < 0 {
			return zeroK, zeroV, false
		}
		n := it.nodes[it.idx]
		it.idx--
		return n.key, n.value, true
	}

	if it.cur == nil {
		return zeroK, zeroV, false
	}
	n := it.cur
	it.cur = it.cur.next[0]
	return n.key, n.value, true
}

// Close 释放迭代器持有的读锁。
//
// Close 是幂等的：重复调用不会 panic。Close 之后再次调用 Next 返回零值。
// 遍历结束或提前退出时都应调用 Close，推荐使用 defer it.Close()。
func (it *Iter[K, V]) Close() {
	if it.closed {
		return
	}
	it.closed = true
	it.cur = nil
	it.nodes = nil
	it.sl.mu.RUnlock()
}
