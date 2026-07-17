package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"dengdeng/internal/model"

	"gorm.io/gorm"
)

var ErrNoAccount = errors.New("no available upstream account")

const (
	defaultSchedulerSnapshotTTL      = 2 * time.Second
	defaultLastUsedPersistInterval   = 30 * time.Second
	defaultSchedulerSnapshotCapacity = 256
	defaultSessionAffinityTTL        = time.Hour
	defaultSessionAffinityCapacity   = 20_000
	defaultModelFailureCapacity      = 8_192
	modelFailureWindow               = time.Minute
	modelFailureShortCooldown        = 10 * time.Second
	modelFailureLongCooldown         = 45 * time.Second
	modelPermissionCooldown          = 5 * time.Minute
)

type schedulerAccountEntry struct {
	account      model.UpstreamAccount
	lastSelected time.Time
}

type schedulerGroupSnapshot struct {
	loadedAt time.Time
	accounts map[int64]*schedulerAccountEntry
}

type schedulerSessionBinding struct {
	accountID int64
	expiresAt time.Time
}

type schedulerAccountModelKey struct {
	accountID int64
	model     string
}

type schedulerModelFailure struct {
	streak      int
	lastFailure time.Time
	blockUntil  time.Time
	lastTouched time.Time
}

// Scheduler keeps a short, process-local account snapshot. DengDeng currently
// runs as one gateway process, so this removes a SELECT and last_used_at UPDATE
// from the hot path without importing Sub2API's distributed Redis machinery.
// Administrative changes become visible within two seconds; failures update
// the snapshot synchronously before the next request is selected.
type Scheduler struct {
	db     *gorm.DB
	policy *RuntimePolicyService

	mu                    sync.Mutex
	groups                map[int64]*schedulerGroupSnapshot
	lastPersisted         map[int64]time.Time
	sessions              map[string]schedulerSessionBinding
	modelFailures         map[schedulerAccountModelKey]schedulerModelFailure
	snapshotTTL           time.Duration
	lastPersistedInterval time.Duration
	sessionTTL            time.Duration
	sessionCapacity       int
	modelFailureCapacity  int
	lastCacheCleanup      time.Time
	now                   func() time.Time
}

func NewScheduler(db *gorm.DB) *Scheduler {
	return &Scheduler{
		db: db, groups: make(map[int64]*schedulerGroupSnapshot, defaultSchedulerSnapshotCapacity),
		lastPersisted: make(map[int64]time.Time), sessions: make(map[string]schedulerSessionBinding),
		modelFailures: make(map[schedulerAccountModelKey]schedulerModelFailure),
		snapshotTTL:   defaultSchedulerSnapshotTTL, lastPersistedInterval: defaultLastUsedPersistInterval,
		sessionTTL: defaultSessionAffinityTTL, sessionCapacity: defaultSessionAffinityCapacity,
		modelFailureCapacity: defaultModelFailureCapacity, now: time.Now,
	}
}

// SetRuntimePolicy keeps the constructor compatible with embedders and tests
// while allowing a running server to apply operator-selected cooldowns.
func (s *Scheduler) SetRuntimePolicy(policy *RuntimePolicyService) {
	s.policy = policy
}

// Pick retains the original API for callers that do not expose a stable
// conversation identifier.
func (s *Scheduler) Pick(groupID int64, exclude []int64) (*model.UpstreamAccount, error) {
	return s.pick(groupID, "", "", exclude)
}

// PickForSession keeps one conversation on the same upstream account while it
// remains healthy and model-capable. Stable routing improves upstream prompt
// cache reuse and prevents a multi-turn tool session from bouncing between
// unrelated subscription accounts.
func (s *Scheduler) PickForSession(groupID int64, modelName, sessionID string, exclude []int64) (*model.UpstreamAccount, error) {
	return s.pick(groupID, modelName, sessionID, exclude)
}

func (s *Scheduler) pick(groupID int64, modelName, sessionID string, exclude []int64) (*model.UpstreamAccount, error) {
	now := s.nowTime()
	snapshot, err := s.snapshot(groupID, now)
	if err != nil {
		return nil, err
	}
	excluded := make(map[int64]struct{}, len(exclude))
	for _, id := range exclude {
		excluded[id] = struct{}{}
	}

	s.mu.Lock()
	s.cleanupCachesLocked(now)
	var selected *schedulerAccountEntry
	sessionKey := schedulerSessionKey(groupID, modelName, sessionID)
	if sessionKey != "" {
		if binding, ok := s.sessions[sessionKey]; ok && binding.expiresAt.After(now) {
			if entry := snapshot.accounts[binding.accountID]; s.entryAvailableLocked(entry, modelName, excluded, now) {
				selected = entry
			} else {
				delete(s.sessions, sessionKey)
			}
		}
	}
	if selected == nil {
		for _, entry := range snapshot.accounts {
			if !s.entryAvailableLocked(entry, modelName, excluded, now) {
				continue
			}
			if selected == nil || schedulerEntryBefore(entry, selected) {
				selected = entry
			}
		}
	}
	if selected == nil {
		s.mu.Unlock()
		return nil, ErrNoAccount
	}
	selected.lastSelected = now
	account := selected.account
	account.LastUsedAt = timePointer(now)
	if sessionKey != "" {
		s.sessions[sessionKey] = schedulerSessionBinding{accountID: account.ID, expiresAt: now.Add(s.sessionTTL)}
	}
	shouldPersist := now.Sub(s.lastPersisted[account.ID]) >= s.lastPersistedInterval
	if shouldPersist {
		s.lastPersisted[account.ID] = now
	}
	s.mu.Unlock()

	if shouldPersist {
		s.persistLastUsed(account.ID, now)
	}
	return &account, nil
}

func (s *Scheduler) entryAvailableLocked(entry *schedulerAccountEntry, modelName string, excluded map[int64]struct{}, now time.Time) bool {
	if entry == nil || entry.account.Status != model.StatusActive {
		return false
	}
	if _, skip := excluded[entry.account.ID]; skip {
		return false
	}
	if entry.account.CooldownUntil != nil && entry.account.CooldownUntil.After(now) {
		return false
	}
	key, ok := schedulerModelKey(entry.account.ID, modelName)
	if !ok {
		return true
	}
	failure, exists := s.modelFailures[key]
	if !exists {
		return true
	}
	if failure.blockUntil.IsZero() || !failure.blockUntil.After(now) {
		if now.Sub(failure.lastTouched) > modelFailureWindow {
			delete(s.modelFailures, key)
		}
		return true
	}
	return false
}

func schedulerEntryBefore(candidate, current *schedulerAccountEntry) bool {
	if candidate.account.Priority != current.account.Priority {
		return candidate.account.Priority > current.account.Priority
	}
	if candidate.lastSelected.IsZero() != current.lastSelected.IsZero() {
		return candidate.lastSelected.IsZero()
	}
	if !candidate.lastSelected.Equal(current.lastSelected) {
		return candidate.lastSelected.Before(current.lastSelected)
	}
	return candidate.account.ID < current.account.ID
}

func (s *Scheduler) snapshot(groupID int64, now time.Time) (*schedulerGroupSnapshot, error) {
	s.mu.Lock()
	if cached := s.groups[groupID]; cached != nil && now.Sub(cached.loadedAt) < s.snapshotTTL {
		s.mu.Unlock()
		return cached, nil
	}
	s.mu.Unlock()

	var accounts []model.UpstreamAccount
	err := s.db.Preload("Proxy").
		Where("group_id = ? AND status = ?", groupID, model.StatusActive).
		Find(&accounts).Error
	if err != nil {
		return nil, err
	}
	fresh := &schedulerGroupSnapshot{loadedAt: now, accounts: make(map[int64]*schedulerAccountEntry, len(accounts))}
	for i := range accounts {
		account := accounts[i]
		lastSelected := time.Time{}
		if account.LastUsedAt != nil {
			lastSelected = *account.LastUsedAt
		}
		fresh.accounts[account.ID] = &schedulerAccountEntry{account: account, lastSelected: lastSelected}
	}

	s.mu.Lock()
	if cached := s.groups[groupID]; cached != nil && cached.loadedAt.After(fresh.loadedAt) {
		fresh = cached
	} else {
		s.groups[groupID] = fresh
	}
	s.mu.Unlock()
	return fresh, nil
}

// InvalidateGroup makes administrative writes visible immediately when the
// caller has access to the scheduler. The TTL remains a safe fallback for
// older call sites and external database changes.
func (s *Scheduler) InvalidateGroup(groupID int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.groups, groupID)
	s.mu.Unlock()
}

func (s *Scheduler) persistLastUsed(accountID int64, selectedAt time.Time) {
	if s == nil || s.db == nil {
		return
	}
	go func() {
		_ = s.db.Model(&model.UpstreamAccount{}).
			Where("id = ? AND (last_used_at IS NULL OR last_used_at < ?)", accountID, selectedAt).
			Update("last_used_at", selectedAt).Error
	}()
}

// ReportFailure applies an escalating cooldown so a broken account stops
// receiving traffic without being permanently disabled.
func (s *Scheduler) ReportFailure(accountID int64, statusCode int, message string) {
	cooldown := DefaultGatewayRuntimePolicy().CooldownFor(statusCode)
	if s.policy != nil {
		cooldown = s.policy.Current().CooldownFor(statusCode)
	}
	until := s.nowTime().Add(cooldown)
	if len(message) > 1000 {
		message = message[:1000]
	}
	s.mu.Lock()
	s.invalidateSessionsForAccountLocked(accountID)
	for _, group := range s.groups {
		if entry := group.accounts[accountID]; entry != nil {
			entry.account.ErrorCount++
			entry.account.CooldownUntil = timePointer(until)
			entry.account.LastError = message
		}
	}
	s.mu.Unlock()
	_ = s.db.Model(&model.UpstreamAccount{}).Where("id = ?", accountID).Updates(map[string]any{
		"error_count":    gorm.Expr("error_count + 1"),
		"cooldown_until": until,
		"last_error":     message,
	}).Error
}

// ReportFailureForModel distinguishes credential/account failures from
// endpoint-specific failures. A 403/5xx for one model no longer removes the
// account from every other model in its group.
func (s *Scheduler) ReportFailureForModel(accountID int64, modelName string, statusCode int, message string) {
	if !modelScopedSchedulerFailure(statusCode, modelName) {
		s.ReportFailure(accountID, statusCode, message)
		return
	}
	if len(message) > 1000 {
		message = message[:1000]
	}
	now := s.nowTime()
	key, ok := schedulerModelKey(accountID, modelName)
	if !ok {
		s.ReportFailure(accountID, statusCode, message)
		return
	}

	s.mu.Lock()
	entry := s.modelFailures[key]
	if entry.lastFailure.IsZero() || now.Sub(entry.lastFailure) > modelFailureWindow || now.Before(entry.lastFailure) {
		entry.streak = 0
	}
	entry.streak++
	entry.lastFailure = now
	entry.lastTouched = now
	switch {
	case statusCode == 403 || statusCode == 404:
		entry.blockUntil = now.Add(modelPermissionCooldown)
	case entry.streak >= 2:
		entry.blockUntil = now.Add(modelFailureLongCooldown)
	default:
		entry.blockUntil = now.Add(modelFailureShortCooldown)
	}
	if _, exists := s.modelFailures[key]; !exists && s.modelFailureCapacity > 0 && len(s.modelFailures) >= s.modelFailureCapacity {
		s.evictOldestModelFailureLocked()
	}
	s.modelFailures[key] = entry
	for _, group := range s.groups {
		if account := group.accounts[accountID]; account != nil {
			account.account.ErrorCount++
			account.account.LastError = message
		}
	}
	s.mu.Unlock()
	_ = s.db.Model(&model.UpstreamAccount{}).Where("id = ?", accountID).Updates(map[string]any{
		"error_count": gorm.Expr("error_count + 1"),
		"last_error":  message,
	}).Error
}

// ReportSuccess clears persisted failure state only when the cached account
// was previously unhealthy. Healthy requests therefore remain write-free.
func (s *Scheduler) ReportSuccess(accountID int64) {
	needsPersist := false
	s.mu.Lock()
	for _, group := range s.groups {
		if entry := group.accounts[accountID]; entry != nil {
			if entry.account.ErrorCount > 0 || entry.account.CooldownUntil != nil || entry.account.LastError != "" {
				needsPersist = true
			}
			entry.account.ErrorCount = 0
			entry.account.CooldownUntil = nil
			entry.account.LastError = ""
		}
	}
	s.mu.Unlock()
	if !needsPersist {
		return
	}
	_ = s.db.Model(&model.UpstreamAccount{}).Where("id = ?", accountID).Updates(map[string]any{
		"error_count":    0,
		"cooldown_until": nil,
		"last_error":     "",
	}).Error
}

// ReportSuccessForModel clears the short-lived model circuit and then repairs
// any persisted account failure state.
func (s *Scheduler) ReportSuccessForModel(accountID int64, modelName string) {
	if key, ok := schedulerModelKey(accountID, modelName); ok {
		s.mu.Lock()
		delete(s.modelFailures, key)
		s.mu.Unlock()
	}
	s.ReportSuccess(accountID)
}

func schedulerSessionKey(groupID int64, modelName, sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if groupID <= 0 || sessionID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(sessionID))
	return strings.Join([]string{
		strconv.FormatInt(groupID, 10),
		strings.ToLower(strings.TrimSpace(modelName)),
		hex.EncodeToString(sum[:16]),
	}, ":")
}

func schedulerModelKey(accountID int64, modelName string) (schedulerAccountModelKey, bool) {
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	if accountID <= 0 || modelName == "" || len(modelName) > 256 {
		return schedulerAccountModelKey{}, false
	}
	return schedulerAccountModelKey{accountID: accountID, model: modelName}, true
}

func modelScopedSchedulerFailure(statusCode int, modelName string) bool {
	if strings.TrimSpace(modelName) == "" {
		return false
	}
	switch {
	case statusCode == 403, statusCode == 404, statusCode == 408, statusCode == 409, statusCode == 425:
		return true
	case statusCode >= 500:
		return true
	default:
		return false
	}
}

func (s *Scheduler) cleanupCachesLocked(now time.Time) {
	if !s.lastCacheCleanup.IsZero() && now.Sub(s.lastCacheCleanup) < time.Minute &&
		len(s.sessions) < s.sessionCapacity && len(s.modelFailures) < s.modelFailureCapacity {
		return
	}
	for key, binding := range s.sessions {
		if !binding.expiresAt.After(now) {
			delete(s.sessions, key)
		}
	}
	for key, failure := range s.modelFailures {
		if now.Sub(failure.lastTouched) > modelFailureWindow && !failure.blockUntil.After(now) {
			delete(s.modelFailures, key)
		}
	}
	for s.sessionCapacity > 0 && len(s.sessions) >= s.sessionCapacity {
		for key := range s.sessions {
			delete(s.sessions, key)
			break
		}
	}
	s.lastCacheCleanup = now
}

func (s *Scheduler) invalidateSessionsForAccountLocked(accountID int64) {
	for key, binding := range s.sessions {
		if binding.accountID == accountID {
			delete(s.sessions, key)
		}
	}
}

func (s *Scheduler) evictOldestModelFailureLocked() {
	var oldestKey schedulerAccountModelKey
	var oldest time.Time
	found := false
	for key, failure := range s.modelFailures {
		if !found || failure.lastTouched.Before(oldest) {
			oldestKey, oldest, found = key, failure.lastTouched, true
		}
	}
	if found {
		delete(s.modelFailures, oldestKey)
	}
}

func (s *Scheduler) nowTime() time.Time {
	if s != nil && s.now != nil {
		return s.now()
	}
	return time.Now()
}

func timePointer(value time.Time) *time.Time {
	copy := value
	return &copy
}
