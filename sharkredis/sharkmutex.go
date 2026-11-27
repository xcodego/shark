package sharkredis

import (
	"fmt"
	"sync"
	"time"

	"github.com/go-redsync/redsync/v4"
)

// SharkMutex 是可配置的 Redis 分布式互斥锁，由 Helper.NewMutex 创建。
// 过期时间、重试间隔和尝试次数需在首次 Lock 之前设置，此后不再生效；
// 默认过期 1 分钟、只尝试 1 次。
type SharkMutex struct {
	mutex      *redsync.Redsync
	key        string
	expire     *time.Duration
	retryDelay *time.Duration
	trys       *int
	instance   *redsync.Mutex
	locker     sync.Mutex
}

// Expire 设置锁的过期时间，默认 1 分钟。
func (o *SharkMutex) Expire(expire time.Duration) *SharkMutex {
	o.expire = &expire
	return o
}

// WithRetryDelay 设置两次加锁尝试之间的间隔，未设置时使用 redsync 默认值。
func (o *SharkMutex) WithRetryDelay(retryDelay time.Duration) *SharkMutex {
	o.retryDelay = &retryDelay
	return o
}

// WithTries 设置加锁的最大尝试次数，默认 1。
func (o *SharkMutex) WithTries(tries int) *SharkMutex {
	o.trys = &tries
	return o
}

// Lock 获取分布式锁，首次调用时按已配置参数创建底层 redsync 锁；获取失败返回错误。
func (o *SharkMutex) Lock() error {
	o.locker.Lock()
	defer o.locker.Unlock()
	if o.instance == nil {
		trys := 1
		expire := time.Minute
		if o.trys != nil {
			trys = *o.trys
		}
		if o.expire != nil {
			expire = *o.expire
		}
		if o.retryDelay != nil {
			o.instance = o.mutex.NewMutex(o.key, redsync.WithExpiry(expire), redsync.WithRetryDelay(*o.retryDelay), redsync.WithTries(trys))
		} else {
			o.instance = o.mutex.NewMutex(o.key, redsync.WithExpiry(expire), redsync.WithTries(trys))
		}
	}
	return o.instance.Lock()
}

// Unlock 释放分布式锁，返回是否释放成功；未调用过 Lock 时返回错误。
func (o *SharkMutex) Unlock() (bool, error) {
	o.locker.Lock()
	defer o.locker.Unlock()
	if o.instance == nil {
		return false, fmt.Errorf("mutex not initialized")
	}
	return o.instance.Unlock()
}
