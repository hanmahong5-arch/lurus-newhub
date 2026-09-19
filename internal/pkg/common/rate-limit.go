package common

import (
	"sync"
	"time"
)

type InMemoryRateLimiter struct {
	store              map[string]*[]int64
	mutex              sync.Mutex
	expirationDuration time.Duration
	// stop is closed by Stop to end the expiry sweeper Init starts. It is
	// created lazily (stopChanLocked) so Stop is safe on a limiter that was
	// never Init'd, and so a Stop that lands before Init still ends the
	// sweeper Init goes on to start.
	stop     chan struct{}
	stopOnce sync.Once
}

// stopChanLocked returns the limiter's stop channel, creating it on first use.
// The caller must hold l.mutex.
func (l *InMemoryRateLimiter) stopChanLocked() chan struct{} {
	if l.stop == nil {
		l.stop = make(chan struct{})
	}
	return l.stop
}

func (l *InMemoryRateLimiter) Init(expirationDuration time.Duration) {
	if l.store == nil {
		l.mutex.Lock()
		if l.store == nil {
			l.store = make(map[string]*[]int64)
			l.expirationDuration = expirationDuration
			if expirationDuration > 0 {
				go l.clearExpiredItems(l.stopChanLocked())
			}
		}
		l.mutex.Unlock()
	}
}

// Stop ends the expiry sweeper started by Init. It is safe to call more than
// once and safe on a limiter that was never Init'd.
//
// No production code calls it. Enumeration behind that:
// `grep -rn InMemoryRateLimiter --include=*.go .` finds one non-test value,
// middleware/rate-limit.go's package-level inMemoryRateLimiter, which is meant
// to live for the process lifetime; `grep -rn '\.Stop()' --include=*.go
// internal/adapter/middleware/` finds one call on it, in
// middleware_cover_test.go. It exists for tests: without it, a test that
// reaches Init leaves a sweeper goroutine holding this limiter's mutex on a
// timer for the rest of the test binary.
func (l *InMemoryRateLimiter) Stop() {
	l.stopOnce.Do(func() {
		l.mutex.Lock()
		defer l.mutex.Unlock()
		close(l.stopChanLocked())
	})
}

func (l *InMemoryRateLimiter) clearExpiredItems(stop <-chan struct{}) {
	ticker := time.NewTicker(l.expirationDuration)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
		l.mutex.Lock()
		now := time.Now().Unix()
		for key := range l.store {
			queue := l.store[key]
			size := len(*queue)
			if size == 0 || now-(*queue)[size-1] > int64(l.expirationDuration.Seconds()) {
				delete(l.store, key)
			}
		}
		l.mutex.Unlock()
	}
}

// Request parameter duration's unit is seconds
func (l *InMemoryRateLimiter) Request(key string, maxRequestNum int, duration int64) bool {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	// [old <-- new]
	queue, ok := l.store[key]
	now := time.Now().Unix()
	if ok {
		if len(*queue) < maxRequestNum {
			*queue = append(*queue, now)
			return true
		} else {
			if now-(*queue)[0] >= duration {
				*queue = (*queue)[1:]
				*queue = append(*queue, now)
				return true
			} else {
				return false
			}
		}
	} else {
		s := make([]int64, 0, maxRequestNum)
		l.store[key] = &s
		*(l.store[key]) = append(*(l.store[key]), now)
	}
	return true
}
