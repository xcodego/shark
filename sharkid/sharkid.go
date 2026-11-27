// Package sharkid 提供基于 Redis 的用户 ID 和唯一 ID 预生成与获取功能。
// 用户 ID 为固定位数的随机数，用 Redis 位图去重，并预先放入分片列表供快速领取。
package sharkid

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/xcodego/shark/sharkutils"
	"go.uber.org/zap"
)

// Config 是 ID 生成器的配置。RedisClient 与 RedisCluster 至少设置一个，优先使用 RedisClient。
type Config struct {
	Project      string               // 项目名称，用作 Redis key 前缀
	Logger       *zap.Logger          // 日志记录器，必填
	RedisClient  *redis.Client        // Redis 客户端
	RedisCluster *redis.ClusterClient // Redis 集群客户端
	UserIdDigits int                  // 用户 ID 位数，取值 1~10，<= 0 时默认 8
	UniqueIdFunc func() int64         // 唯一 ID 生成函数；为 nil 时不提供唯一 ID，返回值 <= 0 表示生成失败
}

// SharkId 管理用户 ID 和唯一 ID 的预生成队列。
type SharkId struct {
	config       *Config
	ShardCount   int // Redis 分片数量，默认 10
	PreloadCount int // 每个分片列表的预生成数量上限，默认 10000
	userIds      chan int
	uniqueIds    chan int64
}

// New 创建 ID 生成器，需调用 Run 启动预生成。
// config 或 Logger 为空、位数超出范围、未配置 Redis 客户端时返回 nil。
func New(config *Config) *SharkId {
	if config == nil {
		return nil
	}
	if config.Logger == nil {
		return nil
	}
	if config.UserIdDigits <= 0 {
		config.UserIdDigits = 8
	}
	if config.UserIdDigits <= 0 || config.UserIdDigits > 10 {
		config.Logger.Error("用户 id 位数必须在 1 到 10 之间", zap.Int("digits", config.UserIdDigits))
		return nil
	}
	if config.RedisCluster == nil && config.RedisClient == nil {
		config.Logger.Error("Redis 客户端未配置")
		return nil
	}
	return &SharkId{
		config:       config,
		ShardCount:   10,
		PreloadCount: 10000,
		userIds:      make(chan int, 100000),
		uniqueIds:    make(chan int64, 100000),
	}
}

// idRange 返回 UserIdDigits 位数字的取值范围 [min, max]，如 8 位为 10000000~99999999。
// 位数非法或超出 int 范围时返回 0, 0。
func (s *SharkId) idRange() (int, int) {
	if s == nil || s.config == nil || s.config.UserIdDigits < 1 || s.config.UserIdDigits > 18 {
		return 0, 0
	}
	minID, maxID := int64(1), int64(9)
	for i := 1; i < s.config.UserIdDigits; i++ {
		minID *= 10
		maxID = maxID*10 + 9
	}
	if maxID > int64(int(^uint(0)>>1)) {
		return 0, 0
	}
	return int(minID), int(maxID)
}

// bitmapKey 返回记录某分片 ID 是否已被占用的位图 key。
func (s *SharkId) bitmapKey(userId int) string {
	return fmt.Sprintf("%v:sharkid:bitmap:%d", s.config.Project, userId%s.ShardCount)
}

// userIdListKey 返回用户 ID 预生成列表的 key。
func (s *SharkId) userIdListKey(userId int) string {
	return fmt.Sprintf("%v:sharkid:list:%d", s.config.Project, userId%s.ShardCount)
}

// rpush 向列表尾部追加数据，失败时每秒重试直到成功。
func (s *SharkId) rpush(key string, args ...any) {
	for {
		if s.config.RedisClient != nil {
			if err := s.config.RedisClient.RPush(context.Background(), key, args...).Err(); err != nil {
				s.config.Logger.Error("RPush失败", zap.String("key", key), zap.Error(err))
				time.Sleep(time.Second)
				continue
			}
		} else if s.config.RedisCluster != nil {
			if err := s.config.RedisCluster.RPush(context.Background(), key, args...).Err(); err != nil {
				s.config.Logger.Error("RPush失败", zap.String("key", key), zap.Error(err))
				time.Sleep(time.Second)
				continue
			}
		}
		break
	}
}

// llen 返回列表长度，失败时每秒重试直到成功。
func (s *SharkId) llen(key string) int {
	for {
		if s.config.RedisClient != nil {
			value, err := s.config.RedisClient.LLen(context.Background(), key).Result()
			if err != nil {
				s.config.Logger.Error("LLen失败", zap.String("key", key), zap.Error(err))
				time.Sleep(time.Second)
				continue
			}
			return int(value)
		} else if s.config.RedisCluster != nil {
			value, err := s.config.RedisCluster.LLen(context.Background(), key).Result()
			if err != nil {
				s.config.Logger.Error("LLen失败", zap.String("key", key), zap.Error(err))
				time.Sleep(time.Second)
				continue
			}
			return int(value)
		}
	}
}

// lpop 从列表头部弹出一个整数，列表为空或出错时返回 error。
func (s *SharkId) lpop(key string) (int, error) {
	if s.config.RedisClient != nil {
		return s.config.RedisClient.LPop(context.Background(), key).Int()
	} else if s.config.RedisCluster != nil {

		return s.config.RedisCluster.LPop(context.Background(), key).Int()
	}
	return 0, fmt.Errorf("no Redis client available")
}

// bitget 读取位图指定偏移的位，失败时每秒重试直到成功。
func (s *SharkId) bitget(key string, offset int) int {
	for {
		if s.config.RedisClient != nil {
			value, err := s.config.RedisClient.GetBit(context.Background(), key, int64(offset)).Result()
			if err != nil {
				s.config.Logger.Error("GetBit失败", zap.String("key", key), zap.Error(err))
				time.Sleep(time.Second)
				continue
			}
			return int(value)
		} else if s.config.RedisCluster != nil {
			value, err := s.config.RedisCluster.GetBit(context.Background(), key, int64(offset)).Result()
			if err != nil {
				s.config.Logger.Error("GetBit失败", zap.String("key", key), zap.Error(err))
				time.Sleep(time.Second)
				continue
			}
			return int(value)
		}
	}
}

// bitset 设置位图指定偏移的位并返回旧值，失败时每秒重试直到成功。
func (s *SharkId) bitset(key string, offset int, value int) int {
	for {
		if s.config.RedisClient != nil {
			value, err := s.config.RedisClient.SetBit(context.Background(), key, int64(offset), value).Result()
			if err != nil {
				s.config.Logger.Error("SetBit失败", zap.String("key", key), zap.Error(err))
				time.Sleep(time.Second)
				continue
			}
			return int(value)
		} else if s.config.RedisCluster != nil {
			value, err := s.config.RedisCluster.SetBit(context.Background(), key, int64(offset), value).Result()
			if err != nil {
				s.config.Logger.Error("SetBit失败", zap.String("key", key), zap.Error(err))
				time.Sleep(time.Second)
				continue
			}
			return int(value)
		}
	}
}

// getOffset 把用户 ID 映射为位图分片序号和分片内的位偏移。
func (s *SharkId) getOffset(userId int) (int, int) {
	minId, _ := s.idRange()
	offset := userId - minId
	shard := offset % s.ShardCount
	bitOffset := offset / s.ShardCount
	return shard, bitOffset
}

// GetUserId 从预生成列表中领取一个用户 ID，从随机分片开始依次尝试各分片。
// 所有分片都没有可用 ID 时返回 0。
func (s *SharkId) GetUserId() int {
	idx := sharkutils.RandNum(0, s.ShardCount)
	for i := 0; i < s.ShardCount; i++ {
		listKey := s.userIdListKey(idx)
		userId, err := s.lpop(listKey)
		if err != nil {
			idx++
			continue
		}
		minID, maxID := s.idRange()
		if userId < minID || userId > maxID {
			idx++
			continue
		}
		return userId
	}
	return 0
}

// Run 启动预生成后台协程（不阻塞）：随机生成未占用的用户 ID，经位图去重后放入分片列表；
// 配置了 UniqueIdFunc 时同样预生成唯一 ID。协程随进程常驻，没有停止机制。
func (s *SharkId) Run() {
	// 候选生成：随机取号并跳过位图中已占用的，放入内存队列（队列满时阻塞）。
	go func() {
		minId, maxId := s.idRange()
		for {
			userId := sharkutils.RandNum(minId, maxId)
			shard, offset := s.getOffset(userId)
			value := s.bitget(s.bitmapKey(shard), offset)
			if value == 1 {
				continue
			}
			s.userIds <- userId
		}
	}()
	// 入列：列表未满时才标记位图并推入列表；已满则丢弃候选（未标记，之后仍可被选中），等待消耗。
	go func() {
		for {
			userId := <-s.userIds
			shard, offset := s.getOffset(userId)
			listKey := s.userIdListKey(userId)
			listCount := s.llen(listKey)
			if listCount >= s.PreloadCount {
				time.Sleep(time.Second)
				continue
			}
			bitmapKey := s.bitmapKey(shard)
			if s.bitset(bitmapKey, offset, 1) == 0 {
				s.rpush(listKey, userId)
			}
		}
	}()
	if s.config.UniqueIdFunc != nil {
		// 唯一 ID 预生成：UniqueIdFunc 返回 <= 0 视为暂不可用，稍后重试。
		go func() {
			for {
				uniqueId := s.config.UniqueIdFunc()
				if uniqueId <= 0 {
					time.Sleep(time.Second)
					continue
				}
				s.uniqueIds <- uniqueId
			}
		}()
		go func() {
			for {
				uniqueId := <-s.uniqueIds
				listKey := s.uniqueIdListKey(uniqueId)
				listCount := s.llen(listKey)
				if listCount >= s.PreloadCount {
					time.Sleep(time.Second)
					continue
				}
				s.rpush(listKey, uniqueId)
			}
		}()
	}
}

// uniqueIdListKey 返回唯一 ID 预生成列表的 key。
func (s *SharkId) uniqueIdListKey(uniqueId int64) string {
	return fmt.Sprintf("%v:uniqueid:%v", s.config.Project, uniqueId%int64(s.ShardCount))
}

// GetUniqueId 获取一个唯一 ID，未配置 UniqueIdFunc 或获取失败时返回 0。
// 优先直接调用 UniqueIdFunc；其失败（<= 0）时依次回退到内存预生成队列和 Redis 预生成列表。
func (s *SharkId) GetUniqueId() int64 {
	if s.config.UniqueIdFunc == nil {
		return 0
	}
	uniqueId := s.config.UniqueIdFunc()
	if uniqueId > 0 {
		return uniqueId
	}
	select {
	case uniqueId := <-s.uniqueIds:
		return uniqueId
	default:
	}
	idx := sharkutils.RandNum(0, s.ShardCount)
	for i := 0; i < s.ShardCount; i++ {
		listKey := s.uniqueIdListKey(int64(idx))
		value, err := s.lpop(listKey)
		if err == nil && value > 0 {
			return int64(value)
		}
		idx++
	}
	return 0
}
