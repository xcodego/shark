package sharkid

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/xcodego/shark/sharkutils"
	"go.uber.org/zap"
)

type Config struct {
	Project      string
	Logger       *zap.Logger
	RedisClient  *redis.Client
	RedisCluster *redis.ClusterClient
	UserIdDigits int          // 用户 id 位数
	UniqueIdFunc func() int64 // 生成用户 id 的函数
}

type SharkId struct {
	config       *Config
	ShardCount   int
	PreloadCount int
	userIds      chan int
	uniqueIds    chan int64
}

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

func (s *SharkId) bitmapKey(userId int) string {
	return fmt.Sprintf("%v:sharkid:bitmap:%d", s.config.Project, userId%s.ShardCount)
}

func (s *SharkId) userIdListKey(userId int) string {
	return fmt.Sprintf("%v:sharkid:list:%d", s.config.Project, userId%s.ShardCount)
}

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

func (s *SharkId) lpop(key string) (int, error) {
	if s.config.RedisClient != nil {
		return s.config.RedisClient.LPop(context.Background(), key).Int()
	} else if s.config.RedisCluster != nil {

		return s.config.RedisCluster.LPop(context.Background(), key).Int()
	}
	return 0, fmt.Errorf("no Redis client available")
}

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

func (s *SharkId) getOffset(userId int) (int, int) {
	minId, _ := s.idRange()
	offset := userId - minId
	shard := offset % s.ShardCount
	bitOffset := offset / s.ShardCount
	return shard, bitOffset
}

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

func (s *SharkId) Run() {
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

func (s *SharkId) uniqueIdListKey(uniqueId int64) string {
	return fmt.Sprintf("%v:uniqueid:%v", s.config.Project, uniqueId%int64(s.ShardCount))
}

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
