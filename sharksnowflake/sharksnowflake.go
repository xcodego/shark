// Package sharksnowflake 提供按秒生成 int64 ID 的 Snowflake 生成器。
// 故意不使用 worker id：ID 仅保证同一实例内唯一，多实例间的冲突需由使用方避免。
package sharksnowflake

import (
	"sync"
	"time"
)

// epoch 是纪元的 Unix 秒级时间戳：2025-01-01 00:00:00 UTC+8。
const epoch = 1735660800

// maxSequenceId 是每秒最多生成的 ID 数，小于 19 位序列号的容量（2^19）。
const maxSequenceId = 520000

// Snowflake 是并发安全的 ID 生成器。ID 仅保证同一实例内唯一。
type Snowflake struct {
	mu         sync.Mutex
	sequenceId int64 // 当前秒内的序列号。
	timeStamp  int64 // 最近处理的 Unix 秒级时间戳。
}

// New 创建一个 Snowflake 生成器。
func New() *Snowflake {
	return &Snowflake{
		sequenceId: 0,
		timeStamp:  0,
	}
}

// Generate 返回下一个 ID：高位为距纪元的秒数，低 19 位为秒内序列号。
// 当前秒的序列号用尽时，持锁等待到下一秒（故意设计，调用方会被阻塞）。
func (s *Snowflake) Generate() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		timestamp := time.Now().Unix()
		if timestamp < s.timeStamp {
			// 等待时钟恢复到上次生成的时间。
			time.Sleep(time.Millisecond)
			continue
		}
		sleeping := false
		if s.timeStamp == timestamp {
			s.sequenceId++
			if s.sequenceId >= maxSequenceId {
				// 达到每秒上限，等到下一秒再重置序列号。
				time.Sleep(time.Millisecond)
				sleeping = true
			}
		} else {
			s.sequenceId = 0
		}
		s.timeStamp = timestamp
		if !sleeping {
			break
		}
	}
	id := (s.timeStamp-epoch)<<19 | s.sequenceId
	return id
}
