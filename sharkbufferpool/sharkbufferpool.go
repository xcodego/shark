// Package sharkbufferpool 提供基于双向链表 + LRU 淘汰策略的泛型缓存池。
//
// 核心数据结构
//
// SharkBufferPool 内部维护一个双向链表，按最近访问时间排序：
//   - head 指向最近访问的元素
//   - tail 指向最久未访问的元素
//   - Get/Put 命中的元素都会通过 moveToHead 移到链表头部
//
// 淘汰机制
//
// 当 len(data) > capacity 时触发淘汰，流程如下：
//  1. evictTail 将 tail 标记为 dirty，从链表尾部摘除
//  2. 元素暂不删除 data map，正在读取的 Goroutine 仍可访问
//  3. 当 dirty 队列达到 evictBatchSize 时，startSave 启动异步保存
//  4. 保存成功后从 data map 删除；失败则重新加入 dirty 队列等待重试
//
// 版本号防脏写
//
// 每次 Put 更新元素时 version+1。startSave 深拷贝保存时的版本号，
// 保存成功后只删除版本号匹配的元素。若保存期间元素被再次 Put 更新，
// 版本号不一致则跳过删除，避免覆盖新数据。
//
// 使用方式
//
//	pool := NewSharkBufferPool[string](capacity, evictBatchSize, saveFunc)
//	err := pool.Put("key", value)
//	val, err := pool.Get("key")
//	err := pool.Close()
//
// 错误定义
//
//	ErrNotFound       缓存未命中
//	ErrBufferPoolFull 缓存池超过硬上限
//	ErrPoolClosed     缓存池已关闭

package sharkbufferpool

import (
	"fmt"
	"sync"
	"sync/atomic"
)

var ErrNotFound = fmt.Errorf("not found")
var ErrBufferPoolFull = fmt.Errorf("buffer pool is full")
var ErrPoolClosed = fmt.Errorf("buffer pool is closed")

type element[T any] struct {
	key     string      // 元素的标识
	value   T           // 元素的值
	next    *element[T] // 指向下一个元素
	prev    *element[T] // 指向上一个元素
	dirty   bool        // 表示该元素是否即将要被淘汰
	version int         // 用于标记元素的版本号，便于判断是否被更新
}

type SharkBufferPool[T any] struct {
	mutex           sync.Mutex             // 互斥锁，保证并发安全
	data            map[string]*element[T] // 存储元素的映射，key为元素的标识，value为对应的元素
	head            *element[T]            // 指向链表的头部元素
	tail            *element[T]            // 指向链表的尾部元素
	capacity        int                    // 缓存池的容量，超过该容量时会触发淘汰机制
	dirty           []*element[T]          // 缓存即将要删除的元素
	saving_callback func([]T) error        // 用于保存元素的回调函数，当元素被淘汰时，会调用该函数来保存元素
	evictBatchSize  int                    // 批量淘汰的大小，表示每次淘汰时最多可以淘汰多少个元素
	savingRunning   atomic.Bool            // 标记是否正在保存元素，避免重复触发保存操作
	closed          atomic.Bool            // 标记缓存池是否已经关闭，避免在关闭后继续操作
	waiting         sync.WaitGroup         // 用于等待保存操作完成，确保在关闭缓存池时不会有未完成的保存操作
}

func NewSharkBufferPool[T any](capacity int, evictbatchsize int, saving_callback func([]T) error) *SharkBufferPool[T] {
	if evictbatchsize <= 0 {
		evictbatchsize = 1000
	}
	return &SharkBufferPool[T]{
		capacity:        capacity,
		saving_callback: saving_callback,
		evictBatchSize:  evictbatchsize,
		data:            make(map[string]*element[T], capacity+evictbatchsize*2),
		dirty:           make([]*element[T], 0, evictbatchsize),
	}
}

func (p *SharkBufferPool[T]) Get(key string) (T, error) {
	if p.closed.Load() {
		var zero T
		return zero, ErrPoolClosed
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if elem, ok := p.data[key]; ok {
		p.moveToHead(elem)
		return elem.value, nil
	}

	var zero T
	return zero, ErrNotFound
}

func (p *SharkBufferPool[T]) Put(key string, value T) error {
	if p.closed.Load() {
		return ErrPoolClosed
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if elem, ok := p.data[key]; ok {
		// key 已存在：更新 value，移到头部，dirty=false，version+1
		elem.value = value
		elem.dirty = false
		elem.version++
		p.moveToHead(elem)
		return nil
	}

	if len(p.data) >= p.capacity+p.evictBatchSize*2 {
		// 超过硬上限，尝试淘汰
		if !p.savingRunning.Load() {
			p.evictTail()
		}
		return ErrBufferPoolFull
	}

	// key 不存在：创建新元素，插入头部，dirty=false，version=1
	elem := &element[T]{
		key:     key,
		value:   value,
		version: 1,
		dirty:   false,
	}
	p.moveToHead(elem)
	p.data[key] = elem
	// 超过容量，淘汰尾部元素
	if !p.savingRunning.Load() && len(p.data) > p.capacity {
		p.evictTail()
	}
	return nil
}

func (p *SharkBufferPool[T]) evictTail() {
	if p.tail == nil {
		return
	}
	// 将尾部元素标记为 dirty，加入 dirty list，并从队尾删除，不需要从 data map 删除，因为可能还在使用
	p.tail.dirty = true
	p.dirty = append(p.dirty, p.tail)
	// 更新 tail 指针
	if p.tail.prev != nil {
		p.tail = p.tail.prev
		p.tail.next = nil
	} else {
		p.head = nil
		p.tail = nil
	}
	// 如果 dirty list 的大小达到 evictBatchSize，则触发保存操作
	if len(p.dirty) >= p.evictBatchSize {
		p.startSave()
	}
}

func (p *SharkBufferPool[T]) startSave() {
	if p.savingRunning.Load() {
		return
	}
	p.savingRunning.Store(true)

	// 将 dirty list 中的元素移动到 saving list 中，并清空 dirty list
	take := p.evictBatchSize
	if take > len(p.dirty) {
		take = len(p.dirty)
	}
	saving := make([]*element[T], take)
	for i, elem := range p.dirty[:take] {
		// 深拷贝一份元素，避免在保存过程中被修改
		e := element[T]{
			key:     elem.key,
			value:   elem.value,
			dirty:   elem.dirty,
			version: elem.version,
		}
		saving[i] = &e
	}
	p.dirty = p.dirty[take:]
	p.waiting.Add(1)
	go func(data []*element[T]) {
		defer p.waiting.Done()
		defer p.savingRunning.Store(false)

		if p.saving_callback == nil || len(data) == 0 {
			// 没有 callback，直接删除
			p.mutex.Lock()
			for _, elem := range data {
				delete(p.data, elem.key)
			}
			p.mutex.Unlock()
			return
		}

		// 收集值并调用 callback 保存
		d := make([]T, len(data))
		for i, elem := range data {
			d[i] = elem.value
		}
		err := p.saveCallback(d)
		p.mutex.Lock()
		if err == nil {
			// 全部保存成功，从 data 中删除
			for _, elem := range data {
				if e, ok := p.data[elem.key]; ok {
					if e.dirty && e.version == elem.version {
						delete(p.data, elem.key)
					}
				}
			}
		} else {
			// 保存失败，重新加入 dirty list
			for _, elem := range data {
				if e, ok := p.data[elem.key]; ok {
					if e.dirty && e.version == elem.version {
						p.dirty = append(p.dirty, e)
					}
				}
			}
		}
		p.mutex.Unlock()
	}(saving)
}

func (p *SharkBufferPool[T]) moveToHead(elem *element[T]) {
	if p.head == elem {
		return
	}

	// Remove from current position
	if elem.prev != nil {
		elem.prev.next = elem.next
	}
	if elem.next != nil {
		elem.next.prev = elem.prev
	}
	if p.tail == elem {
		p.tail = elem.prev
	}

	// Move to head
	elem.prev = nil
	elem.next = p.head
	if p.head != nil {
		p.head.prev = elem
	}
	p.head = elem

	if p.tail == nil {
		p.tail = elem
	}
}

func (p *SharkBufferPool[T]) saveCallback(data []T) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in saving_callback: %v", r)
		}
	}()
	return p.saving_callback(data)
}

func (p *SharkBufferPool[T]) Close() (err error) {
	if !p.closed.CompareAndSwap(false, true) {
		return ErrPoolClosed
	}
	p.waiting.Wait() // 等待所有保存操作完成
	p.mutex.Lock()
	defer p.mutex.Unlock()

	// 收集 data map 中所有元素的值（不管是否 dirty）
	values := make([]T, 0, len(p.data))
	for _, elem := range p.data {
		values = append(values, elem.value)
	}

	if len(values) == 0 || p.saving_callback == nil {
		return nil
	}

	// 分批保存，每批 evictBatchSize 个，失败继续，返回最后一个错误
	var errlast error
	for i := 0; i < len(values); i += p.evictBatchSize {
		end := i + p.evictBatchSize
		if end > len(values) {
			end = len(values)
		}
		if e := p.saveCallback(values[i:end]); e != nil {
			errlast = e
		}
	}

	return errlast
}
