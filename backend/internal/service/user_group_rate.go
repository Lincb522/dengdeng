package service

import (
	"fmt"
	"sync"
	"time"

	"dengdeng/internal/model"

	"gorm.io/gorm"
)

const userGroupRateCacheTTL = 30 * time.Second

type userGroupRateCacheEntry struct {
	rate    float64
	found   bool
	expires time.Time
}

// UserGroupRateResolver serves user-specific group rates from a small local
// cache. It caches both overrides and misses: high-throughput groups should
// not add a database query to every relay just because most users use the
// default multiplier.
type UserGroupRateResolver struct {
	db    *gorm.DB
	mu    sync.RWMutex
	cache map[string]userGroupRateCacheEntry
}

func NewUserGroupRateResolver(db *gorm.DB) *UserGroupRateResolver {
	return &UserGroupRateResolver{db: db, cache: make(map[string]userGroupRateCacheEntry)}
}

func userGroupRateKey(userID, groupID int64) string {
	return fmt.Sprintf("%d:%d", userID, groupID)
}

// Resolve returns the override when one exists, otherwise groupDefault. A
// database failure is intentionally fail-soft: routing stays available and
// continues using the group configuration.
func (r *UserGroupRateResolver) Resolve(userID, groupID int64, groupDefault float64) float64 {
	if r == nil || r.db == nil || userID <= 0 || groupID <= 0 {
		return groupDefault
	}
	key := userGroupRateKey(userID, groupID)
	now := time.Now()
	r.mu.RLock()
	entry, ok := r.cache[key]
	r.mu.RUnlock()
	if ok && entry.expires.After(now) {
		if entry.found {
			return entry.rate
		}
		return groupDefault
	}

	var override model.UserGroupRate
	err := r.db.Where("user_id = ? AND group_id = ?", userID, groupID).First(&override).Error
	entry = userGroupRateCacheEntry{expires: now.Add(userGroupRateCacheTTL)}
	if err == nil && override.RateMultiplier > 0 {
		entry.found, entry.rate = true, override.RateMultiplier
	} else if err != nil && err != gorm.ErrRecordNotFound {
		return groupDefault
	}
	r.mu.Lock()
	r.cache[key] = entry
	r.mu.Unlock()
	if entry.found {
		return entry.rate
	}
	return groupDefault
}

// Invalidate drops one cached override/miss after an administrator changes
// the user's group rate. Passing groupID <= 0 clears all overrides for that
// user, which is useful for replacement-style updates.
func (r *UserGroupRateResolver) Invalidate(userID, groupID int64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if groupID > 0 {
		delete(r.cache, userGroupRateKey(userID, groupID))
		return
	}
	prefix := fmt.Sprintf("%d:", userID)
	for key := range r.cache {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			delete(r.cache, key)
		}
	}
}
