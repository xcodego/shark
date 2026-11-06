package sharkmutex

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-redsync/redsync/v4"
	goredis "github.com/go-redsync/redsync/v4/redis/goredis/v9"
	"github.com/redis/go-redis/v9"
)

type SharkMutex struct {
	project    string
	redisMutex *redsync.Redsync
}

type SharkMutexOption struct {
	mutex      *redsync.Redsync
	key        string
	expire     *time.Duration
	retryDelay *time.Duration
	trys       *int
}

func (o *SharkMutexOption) Expire(expire time.Duration) *SharkMutexOption {
	o.expire = &expire
	return o
}

func (o *SharkMutexOption) WithRetryDelay(retryDelay time.Duration) *SharkMutexOption {
	o.retryDelay = &retryDelay
	return o
}

func (o *SharkMutexOption) WithTries(tries int) *SharkMutexOption {
	o.trys = &tries
	return o
}

func NewWithClient(project string, client *redis.Client) *SharkMutex {
	return &SharkMutex{
		project:    project,
		redisMutex: redsync.New(goredis.NewPool(client)),
	}
}

func NewWithCluster(project string, cluster *redis.ClusterClient) *SharkMutex {
	return &SharkMutex{
		project:    project,
		redisMutex: redsync.New(goredis.NewPool(cluster)),
	}
}

func (m *SharkMutex) lockKey(value ...any) string {
	sv := make([]string, len(value))
	for i, v := range value {
		sv[i] = fmt.Sprint(v)
	}
	s := strings.Join(sv, ":")
	return fmt.Sprintf("%v:mutex:%v", m.project, s)
}

// 默认过期时间 1 分钟, 重试间隔 100 毫秒, 尝试 1 次
func (m *SharkMutex) New(value ...any) *SharkMutexOption {
	key := m.lockKey(value...)
	return &SharkMutexOption{
		mutex: m.redisMutex,
		key:   key,
	}
}

func (o *SharkMutexOption) Mutex() *redsync.Mutex {
	trys := 1
	expire := time.Minute
	if o.trys != nil {
		trys = *o.trys
	}
	if o.expire != nil {
		expire = *o.expire
	}
	if o.retryDelay != nil {
		return o.mutex.NewMutex(o.key, redsync.WithExpiry(expire), redsync.WithRetryDelay(*o.retryDelay), redsync.WithTries(trys))
	} else {
		return o.mutex.NewMutex(o.key, redsync.WithExpiry(expire), redsync.WithTries(trys))
	}
}
