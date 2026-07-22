package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"dengdeng/internal/model"

	"gorm.io/gorm"
)

var (
	ErrNoAccount              = errors.New("no available upstream account")
	ErrAccountConcurrencyBusy = errors.New("all eligible upstream accounts are at concurrency limit")
	ErrAccountQueueFull       = errors.New("upstream account queue is full")
	ErrAccountWaitTimeout     = errors.New("upstream account concurrency wait timed out")
)

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
	freshQuotaWindow                 = 15 * time.Minute
	schedulerScoreHysteresis         = 50.0
)

type schedulerAccountEntry struct {
	account      model.UpstreamAccount
	lastSelected time.Time
	inFlight     int
	latencyEWMA  float64
	errorEWMA    float64
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

// SchedulerDiagnostics is the safe, credential-free explanation for the most
// recent account-pool exhaustion. Clients still receive the stable generic
// 503, while operators can see exactly why every account was excluded.
type SchedulerDiagnostics struct {
	GroupID   int64          `json:"group_id"`
	Model     string         `json:"model,omitempty"`
	Pool      int            `json:"pool"`
	Reasons   map[string]int `json:"reasons"`
	UpdatedAt time.Time      `json:"updated_at"`
}

func (d SchedulerDiagnostics) Summary() string {
	if len(d.Reasons) == 0 {
		return fmt.Sprintf("pool=%d", d.Pool)
	}
	keys := make([]string, 0, len(d.Reasons))
	for key := range d.Reasons {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys)+1)
	parts = append(parts, fmt.Sprintf("pool=%d", d.Pool))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, d.Reasons[key]))
	}
	return strings.Join(parts, ", ")
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
	diagnostics           map[int64]SchedulerDiagnostics
	snapshotTTL           time.Duration
	lastPersistedInterval time.Duration
	sessionTTL            time.Duration
	sessionCapacity       int
	modelFailureCapacity  int
	lastCacheCleanup      time.Time
	slotNotify            chan struct{}
	waiters               int
	now                   func() time.Time
}

func NewScheduler(db *gorm.DB) *Scheduler {
	return &Scheduler{
		db: db, groups: make(map[int64]*schedulerGroupSnapshot, defaultSchedulerSnapshotCapacity),
		lastPersisted: make(map[int64]time.Time), sessions: make(map[string]schedulerSessionBinding),
		modelFailures: make(map[schedulerAccountModelKey]schedulerModelFailure),
		diagnostics:   make(map[int64]SchedulerDiagnostics),
		snapshotTTL:   defaultSchedulerSnapshotTTL, lastPersistedInterval: defaultLastUsedPersistInterval,
		sessionTTL: defaultSessionAffinityTTL, sessionCapacity: defaultSessionAffinityCapacity,
		modelFailureCapacity: defaultModelFailureCapacity, now: time.Now,
		slotNotify: make(chan struct{}),
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

// PickForSessionWait waits for an upstream slot only when otherwise-eligible
// accounts are saturated. It never queues an empty, cooling or incompatible
// group, so configuration errors still fail immediately.
func (s *Scheduler) PickForSessionWait(ctx context.Context, groupID int64, modelName, sessionID string, exclude []int64, wait time.Duration, maxWaiters int) (*model.UpstreamAccount, time.Duration, error) {
	started := time.Now()
	account, err := s.pick(groupID, modelName, sessionID, exclude)
	if !errors.Is(err, ErrAccountConcurrencyBusy) {
		return account, 0, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if wait <= 0 {
		return nil, 0, ErrAccountWaitTimeout
	}

	s.mu.Lock()
	if maxWaiters <= 0 || s.waiters >= maxWaiters {
		s.mu.Unlock()
		return nil, 0, ErrAccountQueueFull
	}
	s.waiters++
	notify := s.slotNotify
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.waiters > 0 {
			s.waiters--
		}
		s.mu.Unlock()
	}()

	timer := time.NewTimer(wait)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer timer.Stop()
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, time.Since(started), ctx.Err()
		case <-timer.C:
			return nil, time.Since(started), ErrAccountWaitTimeout
		case <-ticker.C:
		case <-notify:
		}
		account, err = s.pick(groupID, modelName, sessionID, exclude)
		if !errors.Is(err, ErrAccountConcurrencyBusy) {
			return account, time.Since(started), err
		}
	}
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
	busy := false
	sessionKey := schedulerSessionKey(groupID, modelName, sessionID)
	if sessionKey != "" {
		if binding, ok := s.sessions[sessionKey]; ok && binding.expiresAt.After(now) {
			if entry := snapshot.accounts[binding.accountID]; s.entryRoutableLocked(entry, modelName, excluded, now) {
				if s.entryHasConcurrencySlot(entry) {
					selected = entry
				} else {
					busy = true
				}
			} else {
				delete(s.sessions, sessionKey)
			}
		}
	}
	if selected == nil {
		for _, entry := range snapshot.accounts {
			if !s.entryRoutableLocked(entry, modelName, excluded, now) {
				continue
			}
			if !s.entryHasConcurrencySlot(entry) {
				busy = true
				continue
			}
			if selected == nil || schedulerEntryBefore(entry, selected, now) {
				selected = entry
			}
		}
	}
	if selected == nil {
		s.diagnostics[groupID] = s.buildDiagnosticsLocked(groupID, modelName, snapshot, excluded, now)
		s.mu.Unlock()
		if busy {
			return nil, ErrAccountConcurrencyBusy
		}
		return nil, ErrNoAccount
	}
	delete(s.diagnostics, groupID)
	selected.lastSelected = now
	selected.inFlight++
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
	return s.entryRoutableLocked(entry, modelName, excluded, now) && s.entryHasConcurrencySlot(entry)
}

func (s *Scheduler) entryRoutableLocked(entry *schedulerAccountEntry, modelName string, excluded map[int64]struct{}, now time.Time) bool {
	return s.entryExclusionReasonLocked(entry, modelName, excluded, now) == ""
}

func (s *Scheduler) entryExclusionReasonLocked(entry *schedulerAccountEntry, modelName string, excluded map[int64]struct{}, now time.Time) string {
	if entry == nil {
		return "account_missing"
	}
	if entry.account.Status != model.StatusActive {
		return "disabled"
	}
	if _, skip := excluded[entry.account.ID]; skip {
		return "attempted"
	}
	if entry.account.CooldownUntil != nil && entry.account.CooldownUntil.After(now) {
		return "cooldown"
	}
	if quotaDefinitelyExhausted(&entry.account, now) {
		return "quota_exhausted"
	}
	key, ok := schedulerModelKey(entry.account.ID, modelName)
	if !ok {
		return ""
	}
	failure, exists := s.modelFailures[key]
	if !exists {
		return ""
	}
	if failure.blockUntil.IsZero() || !failure.blockUntil.After(now) {
		if now.Sub(failure.lastTouched) > modelFailureWindow {
			delete(s.modelFailures, key)
		}
		return ""
	}
	return "model_cooldown"
}

func (s *Scheduler) buildDiagnosticsLocked(groupID int64, modelName string, snapshot *schedulerGroupSnapshot, excluded map[int64]struct{}, now time.Time) SchedulerDiagnostics {
	diagnostic := SchedulerDiagnostics{GroupID: groupID, Model: modelName, Reasons: map[string]int{}, UpdatedAt: now}
	if snapshot == nil {
		return diagnostic
	}
	diagnostic.Pool = len(snapshot.accounts)
	for _, entry := range snapshot.accounts {
		reason := s.entryExclusionReasonLocked(entry, modelName, excluded, now)
		if reason == "" && !s.entryHasConcurrencySlot(entry) {
			reason = "concurrency_full"
		}
		if reason == "" {
			reason = "selection_exhausted"
		}
		diagnostic.Reasons[reason]++
	}
	return diagnostic
}

func (s *Scheduler) Diagnostic(groupID int64) (SchedulerDiagnostics, bool) {
	if s == nil {
		return SchedulerDiagnostics{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	diagnostic, ok := s.diagnostics[groupID]
	if !ok {
		return SchedulerDiagnostics{}, false
	}
	diagnostic.Reasons = cloneReasonCounts(diagnostic.Reasons)
	return diagnostic, true
}

func (s *Scheduler) Diagnostics() []SchedulerDiagnostics {
	if s == nil {
		return []SchedulerDiagnostics{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]SchedulerDiagnostics, 0, len(s.diagnostics))
	for _, diagnostic := range s.diagnostics {
		diagnostic.Reasons = cloneReasonCounts(diagnostic.Reasons)
		result = append(result, diagnostic)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].UpdatedAt.After(result[j].UpdatedAt) })
	return result
}

func cloneReasonCounts(source map[string]int) map[string]int {
	result := make(map[string]int, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func (s *Scheduler) entryHasConcurrencySlot(entry *schedulerAccountEntry) bool {
	return entry != nil && (entry.account.Concurrency <= 0 || entry.inFlight < entry.account.Concurrency)
}

func schedulerEntryBefore(candidate, current *schedulerAccountEntry, now time.Time) bool {
	if candidate.account.Priority != current.account.Priority {
		return candidate.account.Priority > current.account.Priority
	}
	candidateScore := schedulerEntryScore(candidate, now)
	currentScore := schedulerEntryScore(current, now)
	// Small score differences keep the old least-recently-used rotation. Only a
	// material health/load/quota advantage overrides fair distribution.
	if math.Abs(candidateScore-currentScore) > schedulerScoreHysteresis {
		return candidateScore > currentScore
	}
	if candidate.lastSelected.IsZero() != current.lastSelected.IsZero() {
		return candidate.lastSelected.IsZero()
	}
	if !candidate.lastSelected.Equal(current.lastSelected) {
		return candidate.lastSelected.Before(current.lastSelected)
	}
	return candidate.account.ID < current.account.ID
}

// schedulerEntryScore combines live load, observed latency/error rate and
// provider quota headroom inside an administrator priority tier. Priority stays
// authoritative, while equal-priority accounts no longer rotate blindly into
// a nearly exhausted or consistently slow upstream.
func schedulerEntryScore(entry *schedulerAccountEntry, now time.Time) float64 {
	if entry == nil {
		return math.Inf(-1)
	}
	headroom := accountQuotaHeadroom(&entry.account, now)
	latencyPenalty := math.Min(entry.latencyEWMA, 10_000) / 20
	loadPenalty := float64(entry.inFlight) * 250
	errorPenalty := entry.errorEWMA*400 + float64(entry.account.ErrorCount)*20
	return headroom*8 - latencyPenalty - loadPenalty - errorPenalty
}

func accountQuotaHeadroom(account *model.UpstreamAccount, now time.Time) float64 {
	if account == nil {
		return 0
	}
	// Unknown quota is neutral rather than bad. API-key providers commonly do
	// not expose balance data and must remain routable.
	headroom := 50.0
	if quota := account.CodexQuota; quota != nil && schedulerSnapshotFresh(quota.FetchedAt, now) {
		values := make([]float64, 0, 2)
		if quota.HasPrimaryWindow {
			values = append(values, 100-schedulerClampPercent(quota.PrimaryUsedPercent))
		}
		if quota.HasSecondaryWindow {
			values = append(values, 100-schedulerClampPercent(quota.SecondaryUsedPercent))
		}
		if len(values) > 0 {
			headroom = minFloat(values)
		}
	}
	if quota := account.Quota; quota != nil && quota.FetchedAt != nil && schedulerSnapshotFresh(*quota.FetchedAt, now) {
		values := make([]float64, 0, len(quota.Windows))
		for _, window := range quota.Windows {
			switch {
			case window.UsedPercent != nil:
				values = append(values, 100-schedulerClampPercent(*window.UsedPercent))
			case window.Remaining != nil && window.Limit != nil && *window.Limit > 0:
				values = append(values, schedulerClampPercent(*window.Remaining / *window.Limit * 100))
			}
		}
		if len(values) > 0 {
			headroom = math.Min(headroom, minFloat(values))
		}
	}
	return headroom
}

func quotaDefinitelyExhausted(account *model.UpstreamAccount, now time.Time) bool {
	quota := account.CodexQuota
	if quota == nil || !schedulerSnapshotFresh(quota.FetchedAt, now) {
		return false
	}
	if !quota.Allowed {
		return true
	}
	if !quota.LimitReached {
		return false
	}
	// Do not keep an account excluded after a cached reset boundary. The quota
	// refresher will replace the snapshot shortly; allowing a probe request here
	// avoids turning a stale limit flag into a false 503.
	if quota.PrimaryResetAt != nil && !quota.PrimaryResetAt.After(now) {
		return false
	}
	if quota.SecondaryResetAt != nil && !quota.SecondaryResetAt.After(now) {
		return false
	}
	return true
}

func schedulerSnapshotFresh(fetchedAt, now time.Time) bool {
	if fetchedAt.IsZero() {
		return false
	}
	return !fetchedAt.Before(now.Add(-freshQuotaWindow)) && !fetchedAt.After(now.Add(5*time.Minute))
}

func schedulerClampPercent(value float64) float64 {
	return math.Max(0, math.Min(100, value))
}

func minFloat(values []float64) float64 {
	result := values[0]
	for _, value := range values[1:] {
		result = math.Min(result, value)
	}
	return result
}

func (s *Scheduler) snapshot(groupID int64, now time.Time) (*schedulerGroupSnapshot, error) {
	s.mu.Lock()
	if cached := s.groups[groupID]; cached != nil && now.Sub(cached.loadedAt) < s.snapshotTTL {
		s.mu.Unlock()
		return cached, nil
	}
	s.mu.Unlock()

	var accounts []model.UpstreamAccount
	err := s.db.Preload("Proxy").Preload("Quota").Preload("CodexQuota").
		Where("group_id = ?", groupID).
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
		// Preserve process-local routing observations when the database snapshot
		// refreshes. They intentionally reset on process restart.
		if cached := s.groups[groupID]; cached != nil {
			for id, entry := range fresh.accounts {
				if previous := cached.accounts[id]; previous != nil {
					if previous.lastSelected.After(entry.lastSelected) {
						entry.lastSelected = previous.lastSelected
					}
					entry.inFlight = previous.inFlight
					entry.latencyEWMA = previous.latencyEWMA
					entry.errorEWMA = previous.errorEWMA
				}
			}
		}
		s.groups[groupID] = fresh
	}
	s.mu.Unlock()
	return fresh, nil
}

// Release removes one live request from the account load signal. It is safe to
// call after every relay attempt, including failed attempts.
func (s *Scheduler) Release(accountID int64) {
	if s == nil || accountID <= 0 {
		return
	}
	released := false
	s.mu.Lock()
	for _, group := range s.groups {
		if entry := group.accounts[accountID]; entry != nil && entry.inFlight > 0 {
			entry.inFlight--
			released = true
		}
	}
	s.mu.Unlock()
	if released {
		s.signalSlot()
	}
}

func (s *Scheduler) signalSlot() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.slotNotify != nil {
		close(s.slotNotify)
	}
	s.slotNotify = make(chan struct{})
	s.mu.Unlock()
}

func (s *Scheduler) WaitingCount() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.waiters
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
			entry.errorEWMA = ewma(entry.errorEWMA, 1)
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
			account.errorEWMA = ewma(account.errorEWMA, 1)
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
	s.ReportSuccessForModelWithLatency(accountID, modelName, 0)
}

// ReportSuccessForModelWithLatency updates the adaptive scheduling signals and
// then clears the model/account failure circuit exactly like the legacy API.
func (s *Scheduler) ReportSuccessForModelWithLatency(accountID int64, modelName string, latencyMs int64) {
	s.mu.Lock()
	for _, group := range s.groups {
		if entry := group.accounts[accountID]; entry != nil {
			if latencyMs > 0 {
				entry.latencyEWMA = ewma(entry.latencyEWMA, float64(latencyMs))
			}
			entry.errorEWMA = ewma(entry.errorEWMA, 0)
		}
	}
	s.mu.Unlock()
	if key, ok := schedulerModelKey(accountID, modelName); ok {
		s.mu.Lock()
		delete(s.modelFailures, key)
		s.mu.Unlock()
	}
	s.ReportSuccess(accountID)
}

func ewma(current, sample float64) float64 {
	if current == 0 {
		return sample
	}
	return current*0.8 + sample*0.2
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
	case statusCode == 400, statusCode == 403, statusCode == 404, statusCode == 405,
		statusCode == 408, statusCode == 409, statusCode == 413, statusCode == 422, statusCode == 425:
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
