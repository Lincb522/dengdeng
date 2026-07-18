package service

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrConcurrencyQueueFull   = errors.New("concurrency queue is full")
	ErrConcurrencyWaitTimeout = errors.New("concurrency wait timed out")
)

// ClientConcurrencyLimiter is the single-process implementation of user and
// API-key slots. DengDeng currently runs one gateway process; the API is kept
// deliberately independent from HTTP so a Redis-backed implementation can
// replace it when multiple gateway replicas are introduced.
type ClientConcurrencyLimiter struct {
	mu      sync.Mutex
	users   map[int64]int
	keys    map[int64]int
	waiters int
	notify  chan struct{}
}

type ClientConcurrencyLease struct {
	limiter *ClientConcurrencyLimiter
	userID  int64
	keyID   int64
	once    sync.Once
}

func NewClientConcurrencyLimiter() *ClientConcurrencyLimiter {
	return &ClientConcurrencyLimiter{
		users: make(map[int64]int), keys: make(map[int64]int), notify: make(chan struct{}),
	}
}

// Acquire atomically takes the user and key slots. A zero limit is unlimited,
// but its active count is still tracked for monitoring and future policy edits.
func (l *ClientConcurrencyLimiter) Acquire(
	ctx context.Context,
	userID int64,
	userLimit int,
	keyID int64,
	keyLimit int,
	wait time.Duration,
	maxWaiters int,
) (*ClientConcurrencyLease, time.Duration, error) {
	if l == nil {
		return &ClientConcurrencyLease{}, 0, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if wait <= 0 {
		wait = time.Millisecond
	}
	started := time.Now()
	queued := false
	timer := time.NewTimer(wait)
	defer timer.Stop()
	dequeue := func() {
		if !queued {
			return
		}
		l.mu.Lock()
		if l.waiters > 0 {
			l.waiters--
		}
		l.mu.Unlock()
		queued = false
	}
	defer dequeue()

	for {
		l.mu.Lock()
		userAvailable := userLimit <= 0 || l.users[userID] < userLimit
		keyAvailable := keyLimit <= 0 || l.keys[keyID] < keyLimit
		if userAvailable && keyAvailable {
			l.users[userID]++
			l.keys[keyID]++
			if queued && l.waiters > 0 {
				l.waiters--
				queued = false
			}
			l.mu.Unlock()
			return &ClientConcurrencyLease{limiter: l, userID: userID, keyID: keyID}, time.Since(started), nil
		}
		if !queued {
			if maxWaiters <= 0 || l.waiters >= maxWaiters {
				l.mu.Unlock()
				return nil, time.Since(started), ErrConcurrencyQueueFull
			}
			l.waiters++
			queued = true
		}
		notify := l.notify
		l.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, time.Since(started), ctx.Err()
		case <-timer.C:
			return nil, time.Since(started), ErrConcurrencyWaitTimeout
		case <-notify:
		}
	}
}

func (l *ClientConcurrencyLease) Release() {
	if l == nil || l.limiter == nil {
		return
	}
	l.once.Do(func() {
		limiter := l.limiter
		limiter.mu.Lock()
		decrementConcurrencyCount(limiter.users, l.userID)
		decrementConcurrencyCount(limiter.keys, l.keyID)
		limiter.mu.Unlock()
		limiter.signal()
	})
}

func (l *ClientConcurrencyLimiter) signal() {
	if l == nil {
		return
	}
	l.mu.Lock()
	close(l.notify)
	l.notify = make(chan struct{})
	l.mu.Unlock()
}

func decrementConcurrencyCount(values map[int64]int, id int64) {
	if values[id] <= 1 {
		delete(values, id)
		return
	}
	values[id]--
}

type ClientConcurrencySnapshot struct {
	Users   map[int64]int
	APIKeys map[int64]int
	Waiting int
}

func (l *ClientConcurrencyLimiter) Snapshot() ClientConcurrencySnapshot {
	result := ClientConcurrencySnapshot{Users: map[int64]int{}, APIKeys: map[int64]int{}}
	if l == nil {
		return result
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for id, count := range l.users {
		result.Users[id] = count
	}
	for id, count := range l.keys {
		result.APIKeys[id] = count
	}
	result.Waiting = l.waiters
	return result
}
