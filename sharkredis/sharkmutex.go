package sharkredis

import (
	"fmt"
	"sync"
	"time"

	"github.com/go-redsync/redsync/v4"
)

type SharkMutex struct {
	mutex      *redsync.Redsync
	key        string
	expire     *time.Duration
	retryDelay *time.Duration
	trys       *int
	instance   *redsync.Mutex
	locker     sync.Mutex
}

func (o *SharkMutex) Expire(expire time.Duration) *SharkMutex {
	o.expire = &expire
	return o
}

func (o *SharkMutex) WithRetryDelay(retryDelay time.Duration) *SharkMutex {
	o.retryDelay = &retryDelay
	return o
}

func (o *SharkMutex) WithTries(tries int) *SharkMutex {
	o.trys = &tries
	return o
}

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

func (o *SharkMutex) Unlock() (bool, error) {
	o.locker.Lock()
	defer o.locker.Unlock()
	if o.instance == nil {
		return false, fmt.Errorf("mutex not initialized")
	}
	return o.instance.Unlock()
}
