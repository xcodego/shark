// Package sharksnowflake 提供基于改进型 Snowflake 算法的全局唯一 ID 生成器。
//
// 设计特点：
//  1. 使用 2025-01-01 作为纪元起点，延长 ID 可用寿命
//  2. 序列号扩展到 19 位（0~524287），支持每秒 52 万个 ID 的生成速率
//  3. 不包含机器 ID 字段，仅适用于单机部署
//  4. ID 结构：41 位时间戳（相对纪元秒数） + 19 位序列号 = 60 位有效数据
//  5. 输出为 int64 类型，JavaScript 前端可以安全使用（无需字符串转换），性能更高
//  6. 理论生命周期约为 69.7 年（2^41 / 1000 / 3600 / 24 / 365）
//
// 典型使用模式（结合 Redis 分片集群）：
//  1. 预生成 N 个 ID，存入 Redis 列表（可使用多个不同 key 的列表实现分片）
//  2. 业务方从 Redis 列表中弹出 ID 使用
//  3. 定时检查 Redis 列表中的 ID 数量，不足时自动补充
package sharksnowflake

import (
	"sync"
	"time"
)

// 纪元时间戳：2025-01-01 00:00:00 UTC+8 对应的 Unix 秒数。
// ID 中的时间戳部分存储的是当前时间相对于此纪元的偏移量，
// 这样可以减少时间戳部分占用的位数，延长可用年限。
const epoch = 1735660800

// 最大序列号：19 位所能表示的最大值 = 2^19 = 524288。
// 但实际上每秒最多生成 520000 个 ID，留有一定余量。
const maxSequenceId = 520000

// Snowflake 是改进型 Snowflake ID 生成器实例。
// 每个实例内部维护时间戳和序列号状态，并通过互斥锁保证并发安全。
//
// 注意：该生成器不包含机器 ID，仅适用于单机部署场景。
// 多机场景需结合外部手段（如 Redis 分片列表）来避免 ID 冲突。
//
// 零值 Snowflake 不可直接使用，必须通过 NewSnowflake() 创建。
type Snowflake struct {
	mu         sync.Mutex // 互斥锁，保护并发调用时的 sequenceId 和 timeStamp
	sequenceId int64      // 当前毫秒内的序列号（0 ~ maxSequenceId-1）
	timeStamp  int64      // 上次生成 ID 时的 Unix 秒级时间戳
}

// NewSnowflake 创建并返回一个新的 Snowflake 生成器实例。
// 初始状态下序列号为 0，时间戳为 0，首次 Generate() 调用会自动校准。
//
// 使用示例：
//
//	// 创建一个 Snowflake 生成器（通常在应用启动时创建，全局单例复用）
//	sf := sharksnowflake.NewSnowflake()
//
//	// 并发安全地生成 ID
//	id := sf.Generate()
//	fmt.Println(id) // 输出类似: 78140312576000
func NewSnowflake() *Snowflake {
	return &Snowflake{
		sequenceId: 0,
		timeStamp:  0,
	}
}

// Generate 生成一个全局唯一的 int64 类型 ID。
//
// 算法流程：
//  1. 加锁保证并发安全
//  2. 获取当前 Unix 秒级时间戳
//  3. 若时间戳回退（时钟回拨），短暂等待 1ms 后重试
//  4. 若与上次时间戳相同：序列号自增；若序列号达到上限，等待 1ms 进入下一秒
//  5. 若进入新的一秒：序列号归零
//  6. 组装 ID：高 41 位为时间偏移量，低 19 位为序列号
//  7. 解锁并返回
//
// 使用示例：
//
//	sf := sharksnowflake.NewSnowflake()
//
//	// 生成单个 ID
//	id := sf.Generate()
//	fmt.Println(id)
//
//	// 批量生成 ID（例如预填充 Redis 列表）
//	ids := make([]int64, 1000)
//	for i := range ids {
//	    ids[i] = sf.Generate()
//	}
func (s *Snowflake) Generate() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		// 获取当前 Unix 秒级时间戳
		timestamp := time.Now().Unix()
		// 检测时钟回拨：如果当前时间小于上次生成时间，等待后重试
		if timestamp < s.timeStamp {
			time.Sleep(time.Millisecond)
			continue
		}
		sleeping := false
		if s.timeStamp == timestamp {
			// 同一秒内：序列号自增
			s.sequenceId++
			// 序列号达上限：等待进入下一秒
			if s.sequenceId >= maxSequenceId {
				time.Sleep(time.Millisecond)
				sleeping = true
			}
		} else {
			// 进入新的一秒：序列号归零
			s.sequenceId = 0
		}
		// 更新内部时间戳记录
		s.timeStamp = timestamp
		// 如果本秒内序列号未耗尽，跳出循环生成 ID
		if !sleeping {
			break
		}
	}
	// ID 组装：高 41 位 = (当前时间 - 纪元)，低 19 位 = 序列号
	id := (s.timeStamp-epoch)<<19 | s.sequenceId
	return id
}
