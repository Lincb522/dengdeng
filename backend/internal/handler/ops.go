package handler

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"dengdeng/internal/model"
	"dengdeng/internal/util"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const opsSampleLimit = 50_000

const opsProbeStaleAfter = 15 * time.Minute

var opsProcessStartedAt = time.Now().UTC()

// opsFilter mirrors the useful part of Sub2API's dashboard contract while
// staying deliberately compact: this service has one usage ledger, so the
// dashboard always reads the same source of truth as billing and exports.
type opsFilter struct {
	Range    string `json:"range"`
	Start    time.Time
	End      time.Time
	Platform string `json:"platform,omitempty"`
	GroupID  int64  `json:"group_id,omitempty"`
}

type opsAggregate struct {
	Requests           int64   `json:"requests"`
	SuccessRequests    int64   `json:"success_requests"`
	ErrorRequests      int64   `json:"error_requests"`
	InputTokens        int64   `json:"input_tokens"`
	OutputTokens       int64   `json:"output_tokens"`
	CacheReadTokens    int64   `json:"cache_read_tokens"`
	CacheWriteTokens   int64   `json:"cache_write_tokens"`
	CacheWrite5mTokens int64   `json:"cache_write_5m_tokens"`
	CacheWrite1hTokens int64   `json:"cache_write_1h_tokens"`
	CostMicro          int64   `json:"cost_micro"`
	AverageLatencyMs   float64 `json:"average_latency_ms"`
}

type opsWindow struct {
	Requests       int64   `json:"requests"`
	SuccessRate    float64 `json:"success_rate"`
	ErrorRate      float64 `json:"error_rate"`
	Tokens         int64   `json:"tokens"`
	CostMicro      int64   `json:"cost_micro"`
	RequestsPerM   float64 `json:"requests_per_minute"`
	RequestsPerSec float64 `json:"requests_per_second"`
	TokensPerSec   float64 `json:"tokens_per_second"`
	AverageLatency float64 `json:"average_latency_ms"`
}

type opsLiveCount struct {
	Scope    string `json:"scope"`
	ID       int64  `json:"id,omitempty"`
	Name     string `json:"name"`
	InFlight int    `json:"in_flight"`
}

// opsRealtime combines exact ledger totals for the last minute with the
// process-local count of requests that have not completed yet. This mirrors
// the two layers used by Sub2API without pretending that an in-flight stream
// already has a final token count or billable cost.
type opsRealtime struct {
	CapturedAt time.Time      `json:"captured_at"`
	InFlight   int            `json:"in_flight"`
	Waiting    int            `json:"waiting"`
	LastMinute *opsWindow     `json:"last_minute"`
	Breakdown  []opsLiveCount `json:"breakdown"`
}

type opsOverview struct {
	opsAggregate
	TotalTokens      int64      `json:"total_tokens"`
	SuccessRate      float64    `json:"success_rate"`
	ErrorRate        float64    `json:"error_rate"`
	P50LatencyMs     int64      `json:"p50_latency_ms"`
	P95LatencyMs     int64      `json:"p95_latency_ms"`
	HealthScore      int        `json:"health_score"`
	AccountTotal     int        `json:"account_total"`
	AccountAvailable int        `json:"account_available"`
	AccountCooling   int        `json:"account_cooling"`
	AccountAttention int        `json:"account_attention"`
	AccountDisabled  int        `json:"account_disabled"`
	Last5Minutes     *opsWindow `json:"last_5_minutes"`
}

type opsTrend struct {
	Start            string  `json:"start"`
	End              string  `json:"end"`
	Label            string  `json:"label"`
	Requests         int64   `json:"requests"`
	SuccessRequests  int64   `json:"success_requests"`
	ErrorRequests    int64   `json:"error_requests"`
	Tokens           int64   `json:"tokens"`
	CostMicro        int64   `json:"cost_micro"`
	AverageLatencyMs float64 `json:"average_latency_ms"`
	latencyTotal     int64
}

type opsRank struct {
	ID                 int64   `json:"id,omitempty"`
	Name               string  `json:"name"`
	Requests           int64   `json:"requests"`
	SuccessRequests    int64   `json:"success_requests"`
	ErrorRequests      int64   `json:"error_requests"`
	Tokens             int64   `json:"tokens"`
	InputTokens        int64   `json:"input_tokens"`
	OutputTokens       int64   `json:"output_tokens"`
	CacheReadTokens    int64   `json:"cache_read_tokens"`
	CacheWriteTokens   int64   `json:"cache_write_tokens"`
	CacheWrite5mTokens int64   `json:"cache_write_5m_tokens"`
	CacheWrite1hTokens int64   `json:"cache_write_1h_tokens"`
	CostMicro          int64   `json:"cost_micro"`
	AverageLatencyMs   float64 `json:"average_latency_ms"`
	latencyTotal       int64
}

// opsRateProfile deliberately reports the current group configuration beside
// immutable ledger totals. Historical cost always comes from cost_micro; a
// multiplier may have changed since a request was made, so it must not be
// retroactively applied to old Token rows in the browser.
type opsRateProfile struct {
	ID                     int64   `json:"id"`
	Name                   string  `json:"name"`
	Platform               string  `json:"platform"`
	RateMultiplier         float64 `json:"rate_multiplier"`
	CacheReadMultiplier    float64 `json:"cache_read_multiplier"`
	CacheWrite5mMultiplier float64 `json:"cache_write_5m_multiplier"`
	CacheWrite1hMultiplier float64 `json:"cache_write_1h_multiplier"`
	ImageRateIndependent   bool    `json:"image_rate_independent"`
	ImageRateMultiplier    float64 `json:"image_rate_multiplier"`
}

type opsAccountHealth struct {
	ID            int64      `json:"id"`
	Name          string     `json:"name"`
	Email         string     `json:"email,omitempty"`
	GroupID       int64      `json:"group_id"`
	GroupName     string     `json:"group_name"`
	Platform      string     `json:"platform"`
	Status        string     `json:"status"`
	Health        string     `json:"health"`
	ErrorCount    int        `json:"error_count"`
	CooldownUntil *time.Time `json:"cooldown_until,omitempty"`
	LastUsedAt    *time.Time `json:"last_used_at,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
	ProbeState    string     `json:"probe_state"`
	ProbeMode     string     `json:"probe_mode,omitempty"`
	ProbeStatus   int        `json:"probe_status_code,omitempty"`
	ProbeLatency  int64      `json:"probe_latency_ms,omitempty"`
	ProbeChecked  *time.Time `json:"probe_checked_at,omitempty"`
	ProbeError    string     `json:"probe_error,omitempty"`
}

// opsSystemMetrics intentionally reports only process and database-pool facts
// the service can know reliably. Host CPU/RAM require a node agent and should
// not be fabricated from container limits.
type opsSystemMetrics struct {
	UptimeSeconds     int64  `json:"uptime_seconds"`
	Goroutines        int    `json:"goroutines"`
	MemoryAllocBytes  uint64 `json:"memory_alloc_bytes"`
	HeapInUseBytes    uint64 `json:"heap_in_use_bytes"`
	DBOpenConnections int    `json:"db_open_connections"`
	DBInUse           int    `json:"db_in_use"`
	DBIdle            int    `json:"db_idle"`
	DBWaitCount       int64  `json:"db_wait_count"`
}

type opsSnapshot struct {
	GeneratedAt     time.Time          `json:"generated_at"`
	Range           string             `json:"range"`
	Start           time.Time          `json:"start"`
	End             time.Time          `json:"end"`
	Platform        string             `json:"platform,omitempty"`
	GroupID         int64              `json:"group_id,omitempty"`
	Overview        opsOverview        `json:"overview"`
	Trend           []opsTrend         `json:"trend"`
	TopModels       []opsRank          `json:"top_models"`
	TopGroups       []opsRank          `json:"top_groups"`
	TopUsers        []opsRank          `json:"top_users"`
	TopAccounts     []opsRank          `json:"top_accounts"`
	ModelUsage      []opsRank          `json:"model_usage"`
	RateProfiles    []opsRateProfile   `json:"rate_profiles"`
	Realtime        opsRealtime        `json:"realtime"`
	AccountHealth   []opsAccountHealth `json:"account_health"`
	RecentErrors    []model.UsageLog   `json:"recent_errors"`
	System          opsSystemMetrics   `json:"system"`
	SampleTruncated bool               `json:"sample_truncated"`
}

type opsLogMetric struct {
	CreatedAt          time.Time
	UserID             int64
	GroupID            int64
	AccountID          int64
	Model              string
	InputTokens        int64
	OutputTokens       int64
	CacheReadTokens    int64
	CacheWriteTokens   int64
	CacheWrite5mTokens int64
	CacheWrite1hTokens int64
	CostMicro          int64
	DurationMs         int64
	StatusCode         int
}

func parseOpsFilter(c *gin.Context) (opsFilter, error) {
	now := time.Now().UTC()
	rangeName := strings.TrimSpace(c.DefaultQuery("range", "24h"))
	durations := map[string]time.Duration{
		"1h":  time.Hour,
		"24h": 24 * time.Hour,
		"7d":  7 * 24 * time.Hour,
		"30d": 30 * 24 * time.Hour,
	}
	duration, knownRange := durations[rangeName]
	if !knownRange {
		return opsFilter{}, fmt.Errorf("range must be 1h, 24h, 7d or 30d")
	}
	filter := opsFilter{Range: rangeName, Start: now.Add(-duration), End: now, Platform: strings.TrimSpace(c.Query("platform"))}
	if filter.Platform != "" && !validPlatform(filter.Platform) {
		return opsFilter{}, fmt.Errorf("invalid platform")
	}
	if raw := strings.TrimSpace(c.Query("group_id")); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			return opsFilter{}, fmt.Errorf("group_id must be a positive integer")
		}
		filter.GroupID = id
	}

	startRaw, endRaw := firstQuery(c, "start", "start_time"), firstQuery(c, "end", "end_time")
	if startRaw != "" || endRaw != "" {
		start, startOK, err := parseUsageTime(startRaw, false)
		if err != nil {
			return opsFilter{}, err
		}
		end, endOK, err := parseUsageTime(endRaw, true)
		if err != nil {
			return opsFilter{}, err
		}
		if !startOK || !endOK {
			return opsFilter{}, fmt.Errorf("start and end must be used together")
		}
		if !start.Before(*end) {
			return opsFilter{}, fmt.Errorf("start must be earlier than end")
		}
		if end.Sub(*start) > 31*24*time.Hour {
			return opsFilter{}, fmt.Errorf("monitoring range cannot exceed 31 days")
		}
		filter.Range = "custom"
		filter.Start, filter.End = *start, *end
	}
	return filter, nil
}

func (f opsFilter) usageFilter() usageQuery {
	start, end := f.Start, f.End
	return usageQuery{Page: 1, Size: maxUsagePageSize, Start: &start, End: &end, Platform: f.Platform, GroupID: f.GroupID, Sort: "created_at", Order: "desc"}
}

func isOpsSuccess(statusCode int) bool {
	return statusCode >= 200 && statusCode < 400
}

func opsTokens(m opsLogMetric) int64 {
	return m.InputTokens + m.OutputTokens + m.CacheReadTokens + m.CacheWriteTokens
}

func aggregateOps(db *gorm.DB, filter usageQuery) (opsAggregate, error) {
	var total opsAggregate
	err := usageScope(db, filter, nil).Select(`
		COUNT(*) AS requests,
		COALESCE(SUM(CASE WHEN usage_logs.status_code >= 200 AND usage_logs.status_code < 400 THEN 1 ELSE 0 END), 0) AS success_requests,
		COALESCE(SUM(CASE WHEN usage_logs.status_code < 200 OR usage_logs.status_code >= 400 THEN 1 ELSE 0 END), 0) AS error_requests,
		COALESCE(SUM(usage_logs.input_tokens), 0) AS input_tokens,
		COALESCE(SUM(usage_logs.output_tokens), 0) AS output_tokens,
		COALESCE(SUM(usage_logs.cache_read_tokens), 0) AS cache_read_tokens,
		COALESCE(SUM(usage_logs.cache_write_tokens), 0) AS cache_write_tokens,
		COALESCE(SUM(usage_logs.cache_write5m_tokens), 0) AS cache_write5m_tokens,
		COALESCE(SUM(usage_logs.cache_write1h_tokens), 0) AS cache_write1h_tokens,
		COALESCE(SUM(usage_logs.cost_micro), 0) AS cost_micro,
		COALESCE(AVG(usage_logs.duration_ms), 0) AS average_latency_ms`).Scan(&total).Error
	return total, err
}

// OpsSnapshot is a single, filterable operations view. It intentionally
// aggregates from the billing ledger, which means token, cost and error values
// reconcile exactly with the detail and CSV APIs rather than being a separate
// best-effort counter.
func (h *AdminHandler) OpsSnapshot(c *gin.Context) {
	filter, err := parseOpsFilter(c)
	if err != nil {
		util.Fail(c, 400, err.Error())
		return
	}
	snapshot, err := h.buildOpsSnapshot(filter)
	if err != nil {
		util.Fail(c, 500, "build operations snapshot failed")
		return
	}
	util.OK(c, snapshot)
}

// TriggerAccountProbes schedules a low-cost health-check pass and returns
// immediately. The UI polls the ordinary snapshot endpoint for results, so a
// large account pool never leaves an administrator request hanging.
func (h *AdminHandler) TriggerAccountProbes(c *gin.Context) {
	if h.monitor == nil {
		util.Fail(c, http.StatusServiceUnavailable, "account monitor is unavailable")
		return
	}
	util.OK(c, gin.H{"started": h.monitor.Trigger(), "interval_seconds": int((5 * time.Minute).Seconds())})
}

func (h *AdminHandler) ProbeAccount(c *gin.Context) {
	if h.monitor == nil {
		util.Fail(c, http.StatusServiceUnavailable, "account monitor is unavailable")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		util.Fail(c, http.StatusBadRequest, "invalid account id")
		return
	}
	probe, err := h.monitor.ProbeAccount(c.Request.Context(), id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		util.Fail(c, http.StatusNotFound, "account not found")
		return
	}
	if err != nil {
		util.Fail(c, http.StatusBadGateway, "run account probe failed")
		return
	}
	util.OK(c, probe)
}

func (h *AdminHandler) buildOpsSnapshot(filter opsFilter) (opsSnapshot, error) {
	usageFilter := filter.usageFilter()
	aggregate, err := aggregateOps(h.db, usageFilter)
	if err != nil {
		return opsSnapshot{}, err
	}

	windowStart := constrainedOpsWindowStart(filter, 5*time.Minute)
	last5Filter := filter.usageFilter()
	last5Filter.Start = &windowStart
	lastFive, err := aggregateOps(h.db, last5Filter)
	if err != nil {
		return opsSnapshot{}, err
	}
	minuteStart := constrainedOpsWindowStart(filter, time.Minute)
	lastMinuteFilter := filter.usageFilter()
	lastMinuteFilter.Start = &minuteStart
	lastMinute, err := aggregateOps(h.db, lastMinuteFilter)
	if err != nil {
		return opsSnapshot{}, err
	}

	raw, truncated, err := h.loadOpsMetrics(usageFilter)
	if err != nil {
		return opsSnapshot{}, err
	}

	accountHealth, healthCounts, err := h.loadOpsAccountHealth(filter)
	if err != nil {
		return opsSnapshot{}, err
	}
	recentErrors, err := h.loadRecentOpsErrors(usageFilter)
	if err != nil {
		return opsSnapshot{}, err
	}

	overview := opsOverview{opsAggregate: aggregate, AccountTotal: healthCounts.total, AccountAvailable: healthCounts.available, AccountCooling: healthCounts.cooling, AccountAttention: healthCounts.attention, AccountDisabled: healthCounts.disabled}
	overview.TotalTokens = aggregate.InputTokens + aggregate.OutputTokens + aggregate.CacheReadTokens + aggregate.CacheWriteTokens
	if aggregate.Requests > 0 {
		overview.SuccessRate = ratio(aggregate.SuccessRequests, aggregate.Requests)
		overview.ErrorRate = ratio(aggregate.ErrorRequests, aggregate.Requests)
	}
	latencies := make([]int64, 0, len(raw))
	for _, entry := range raw {
		if isOpsSuccess(entry.StatusCode) && entry.DurationMs >= 0 {
			latencies = append(latencies, entry.DurationMs)
		}
	}
	overview.P50LatencyMs = percentile(latencies, .50)
	overview.P95LatencyMs = percentile(latencies, .95)
	last5Window := opsWindowFor(lastFive, filter.End.Sub(windowStart))
	overview.Last5Minutes = last5Window
	overview.HealthScore = calculateOpsHealth(overview)

	trend, modelRanks, groupRanks, userRanks, accountRanks := buildOpsBreakdown(raw, filter.Start, filter.End)
	fillOpsRankNames(h.db, groupRanks, userRanks, accountRanks, accountHealth)
	rateProfiles, err := h.loadOpsRateProfiles(filter)
	if err != nil {
		return opsSnapshot{}, err
	}
	realtime := h.loadOpsRealtime(filter, opsWindowFor(lastMinute, filter.End.Sub(minuteStart)))

	return opsSnapshot{
		GeneratedAt: time.Now().UTC(), Range: filter.Range, Start: filter.Start, End: filter.End, Platform: filter.Platform, GroupID: filter.GroupID,
		Overview: overview, Trend: trend, TopModels: sortedOpsRanks(modelRanks), TopGroups: sortedOpsRanks(groupRanks), TopUsers: sortedOpsRanks(userRanks), TopAccounts: sortedOpsRanks(accountRanks),
		ModelUsage: detailedOpsRanks(modelRanks), RateProfiles: rateProfiles, Realtime: realtime, AccountHealth: accountHealth, RecentErrors: recentErrors, System: h.opsSystemMetrics(), SampleTruncated: truncated,
	}, nil
}

func constrainedOpsWindowStart(filter opsFilter, duration time.Duration) time.Time {
	start := filter.End.Add(-duration)
	if start.Before(filter.Start) {
		return filter.Start
	}
	return start
}

func opsWindowFor(aggregate opsAggregate, duration time.Duration) *opsWindow {
	tokens := aggregate.InputTokens + aggregate.OutputTokens + aggregate.CacheReadTokens + aggregate.CacheWriteTokens
	window := &opsWindow{Requests: aggregate.Requests, Tokens: tokens, CostMicro: aggregate.CostMicro, AverageLatency: aggregate.AverageLatencyMs}
	if aggregate.Requests > 0 {
		window.SuccessRate = ratio(aggregate.SuccessRequests, aggregate.Requests)
		window.ErrorRate = ratio(aggregate.ErrorRequests, aggregate.Requests)
	}
	if seconds := duration.Seconds(); seconds > 0 {
		window.RequestsPerSec = float64(aggregate.Requests) / seconds
		window.TokensPerSec = float64(tokens) / seconds
		window.RequestsPerM = window.RequestsPerSec * 60
	}
	return window
}

func (h *AdminHandler) loadOpsRealtime(filter opsFilter, lastMinute *opsWindow) opsRealtime {
	result := opsRealtime{CapturedAt: time.Now().UTC(), LastMinute: lastMinute, Breakdown: make([]opsLiveCount, 0)}
	if h.runtime == nil {
		return result
	}
	snapshot := h.runtime.Snapshot(filter.Platform, filter.GroupID)
	result.InFlight = snapshot.InFlight
	result.Waiting = snapshot.Waiting
	for platform, count := range snapshot.Platform {
		result.Breakdown = append(result.Breakdown, opsLiveCount{Scope: "platform", Name: platform, InFlight: count})
	}
	if len(snapshot.Group) > 0 {
		var groups []model.Group
		ids := make([]int64, 0, len(snapshot.Group))
		for id := range snapshot.Group {
			ids = append(ids, id)
		}
		h.db.Where("id IN ?", ids).Find(&groups)
		names := map[int64]string{}
		for _, group := range groups {
			names[group.ID] = group.Name
		}
		for id, count := range snapshot.Group {
			name := names[id]
			if name == "" {
				name = fmt.Sprintf("分组 #%d", id)
			}
			result.Breakdown = append(result.Breakdown, opsLiveCount{Scope: "group", ID: id, Name: name, InFlight: count})
		}
	}
	if len(snapshot.Account) > 0 {
		var accounts []model.UpstreamAccount
		ids := make([]int64, 0, len(snapshot.Account))
		for id := range snapshot.Account {
			ids = append(ids, id)
		}
		h.db.Where("id IN ?", ids).Find(&accounts)
		names := map[int64]string{}
		for _, account := range accounts {
			names[account.ID] = account.Name
		}
		for id, count := range snapshot.Account {
			name := names[id]
			if name == "" {
				name = fmt.Sprintf("账号 #%d", id)
			}
			result.Breakdown = append(result.Breakdown, opsLiveCount{Scope: "account", ID: id, Name: name, InFlight: count})
		}
	}
	sort.Slice(result.Breakdown, func(i, j int) bool {
		if result.Breakdown[i].InFlight == result.Breakdown[j].InFlight {
			return result.Breakdown[i].Name < result.Breakdown[j].Name
		}
		return result.Breakdown[i].InFlight > result.Breakdown[j].InFlight
	})
	return result
}

func (h *AdminHandler) opsSystemMetrics() opsSystemMetrics {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	metrics := opsSystemMetrics{
		UptimeSeconds:    int64(time.Since(opsProcessStartedAt).Seconds()),
		Goroutines:       runtime.NumGoroutine(),
		MemoryAllocBytes: memory.Alloc,
		HeapInUseBytes:   memory.HeapInuse,
	}
	if sqlDB, err := h.db.DB(); err == nil {
		stats := sqlDB.Stats()
		metrics.DBOpenConnections = stats.OpenConnections
		metrics.DBInUse = stats.InUse
		metrics.DBIdle = stats.Idle
		metrics.DBWaitCount = stats.WaitCount
	}
	return metrics
}

func (h *AdminHandler) loadOpsMetrics(filter usageQuery) ([]opsLogMetric, bool, error) {
	var rows []opsLogMetric
	err := usageScope(h.db, filter, nil).
		Select("usage_logs.created_at, usage_logs.user_id, usage_logs.group_id, usage_logs.account_id, usage_logs.model, usage_logs.input_tokens, usage_logs.output_tokens, usage_logs.cache_read_tokens, usage_logs.cache_write_tokens, usage_logs.cache_write5m_tokens, usage_logs.cache_write1h_tokens, usage_logs.cost_micro, usage_logs.duration_ms, usage_logs.status_code").
		Order("usage_logs.created_at DESC").Limit(opsSampleLimit + 1).Find(&rows).Error
	if err != nil {
		return nil, false, err
	}
	truncated := len(rows) > opsSampleLimit
	if truncated {
		rows = rows[:opsSampleLimit]
	}
	return rows, truncated, nil
}

func (h *AdminHandler) loadOpsRateProfiles(filter opsFilter) ([]opsRateProfile, error) {
	var groups []model.Group
	q := h.db.Order("platform ASC, name ASC")
	if filter.GroupID > 0 {
		q = q.Where("id = ?", filter.GroupID)
	}
	if filter.Platform != "" {
		q = q.Where("platform = ?", filter.Platform)
	}
	if err := q.Find(&groups).Error; err != nil {
		return nil, err
	}
	profiles := make([]opsRateProfile, 0, len(groups))
	for _, group := range groups {
		profiles = append(profiles, opsRateProfile{
			ID: group.ID, Name: group.Name, Platform: group.Platform,
			RateMultiplier: group.RateMultiplier, CacheReadMultiplier: group.CacheReadMultiplier,
			CacheWrite5mMultiplier: group.CacheWrite5mMultiplier, CacheWrite1hMultiplier: group.CacheWrite1hMultiplier,
			ImageRateIndependent: group.ImageRateIndependent, ImageRateMultiplier: group.ImageRateMultiplier,
		})
	}
	return profiles, nil
}

type opsHealthCounts struct{ total, available, cooling, attention, disabled int }

func (h *AdminHandler) loadOpsAccountHealth(filter opsFilter) ([]opsAccountHealth, opsHealthCounts, error) {
	var accounts []model.UpstreamAccount
	q := h.db.Preload("Group").Order("priority ASC, id ASC")
	if filter.GroupID > 0 {
		q = q.Where("group_id = ?", filter.GroupID)
	}
	if filter.Platform != "" {
		q = q.Where("platform = ?", filter.Platform)
	}
	if err := q.Find(&accounts).Error; err != nil {
		return nil, opsHealthCounts{}, err
	}
	latest, err := h.loadLatestAccountProbes(accounts)
	if err != nil {
		return nil, opsHealthCounts{}, err
	}
	now := time.Now().UTC()
	result := make([]opsAccountHealth, 0, len(accounts))
	counts := opsHealthCounts{total: len(accounts)}
	for _, account := range accounts {
		health := "ready"
		probe := latest[account.ID]
		switch {
		case account.Status != model.StatusActive:
			health = "disabled"
			counts.disabled++
		case account.CooldownUntil != nil && account.CooldownUntil.After(now):
			health = "cooling"
			counts.cooling++
		case probe == nil:
			health = "checking"
			counts.attention++
		case probe.CheckedAt.Before(now.Add(-opsProbeStaleAfter)):
			health = "stale"
			counts.attention++
		case probe.State != "healthy":
			health = "attention"
			counts.attention++
		default:
			counts.available++
		}
		groupName := ""
		if account.Group != nil {
			groupName = account.Group.Name
		}
		item := opsAccountHealth{ID: account.ID, Name: account.Name, Email: account.Email, GroupID: account.GroupID, GroupName: groupName, Platform: account.Platform, Status: account.Status, Health: health, ErrorCount: account.ErrorCount, CooldownUntil: account.CooldownUntil, LastUsedAt: account.LastUsedAt, LastError: account.LastError}
		if probe != nil {
			checked := probe.CheckedAt
			item.ProbeState, item.ProbeMode, item.ProbeStatus, item.ProbeLatency, item.ProbeChecked, item.ProbeError = probe.State, probe.Mode, probe.StatusCode, probe.LatencyMs, &checked, probe.ErrorMessage
		}
		result = append(result, item)
	}
	return result, counts, nil
}

func (h *AdminHandler) loadLatestAccountProbes(accounts []model.UpstreamAccount) (map[int64]*model.AccountProbe, error) {
	ids := make([]int64, 0, len(accounts))
	for _, account := range accounts {
		ids = append(ids, account.ID)
	}
	if len(ids) == 0 {
		return map[int64]*model.AccountProbe{}, nil
	}
	latestIDs := h.db.Model(&model.AccountProbe{}).Select("MAX(id)").Where("account_id IN ?", ids).Group("account_id")
	var probes []model.AccountProbe
	if err := h.db.Where("id IN (?)", latestIDs).Find(&probes).Error; err != nil {
		return nil, err
	}
	result := make(map[int64]*model.AccountProbe, len(probes))
	for i := range probes {
		result[probes[i].AccountID] = &probes[i]
	}
	return result, nil
}

func (h *AdminHandler) loadRecentOpsErrors(filter usageQuery) ([]model.UsageLog, error) {
	var rows []model.UsageLog
	err := usageScope(h.db, filter, nil).
		Where("usage_logs.status_code < ? OR usage_logs.status_code >= ?", 200, 400).
		Order("usage_logs.created_at DESC").Limit(12).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	decorateUsage(h.db, rows)
	return rows, nil
}

func buildOpsBreakdown(rows []opsLogMetric, start, end time.Time) ([]opsTrend, map[string]*opsRank, map[int64]*opsRank, map[int64]*opsRank, map[int64]*opsRank) {
	step := opsBucketSize(end.Sub(start))
	trend, bucketIndex := makeOpsTrend(start, end, step)
	models := map[string]*opsRank{}
	groups := map[int64]*opsRank{}
	users := map[int64]*opsRank{}
	accounts := map[int64]*opsRank{}
	for _, row := range rows {
		bucketKey := row.CreatedAt.UTC().Truncate(step).Format(time.RFC3339)
		if bucket, ok := bucketIndex[bucketKey]; ok {
			applyOpsTrend(&trend[bucket], row)
		}
		modelName := strings.TrimSpace(row.Model)
		if modelName == "" {
			modelName = "未识别模型"
		}
		if models[modelName] == nil {
			models[modelName] = &opsRank{Name: modelName}
		}
		applyOpsRank(models[modelName], row)
		applyOpsRankForID(groups, row.GroupID, row, "未归属分组")
		applyOpsRankForID(users, row.UserID, row, "未识别用户")
		applyOpsRankForID(accounts, row.AccountID, row, "未记录上游账号")
	}
	for i := range trend {
		if trend[i].Requests > 0 {
			trend[i].AverageLatencyMs = float64(trend[i].latencyTotal) / float64(trend[i].Requests)
		}
	}
	return trend, models, groups, users, accounts
}

// Do not use a map literal keyed by the entity ID here. User, group and
// account IDs come from independent tables and commonly all start at 1; a map
// literal would collapse those three dimensions into one entry.
func applyOpsRankForID(target map[int64]*opsRank, id int64, row opsLogMetric, fallback string) {
	if target[id] == nil {
		name := "已删除或未知"
		if id <= 0 {
			name = fallback
		}
		target[id] = &opsRank{ID: id, Name: name}
	}
	applyOpsRank(target[id], row)
}

func opsBucketSize(window time.Duration) time.Duration {
	switch {
	case window <= 2*time.Hour:
		return 5 * time.Minute
	case window <= 48*time.Hour:
		return time.Hour
	default:
		return 24 * time.Hour
	}
}

func makeOpsTrend(start, end time.Time, step time.Duration) ([]opsTrend, map[string]int) {
	first := start.UTC().Truncate(step)
	items := make([]opsTrend, 0)
	index := map[string]int{}
	for point := first; point.Before(end); point = point.Add(step) {
		finish := point.Add(step)
		item := opsTrend{Start: point.Format(time.RFC3339), End: finish.Format(time.RFC3339), Label: opsTrendLabel(point, step)}
		items = append(items, item)
		index[point.Format(time.RFC3339)] = len(items) - 1
	}
	return items, index
}

func opsTrendLabel(point time.Time, step time.Duration) string {
	if step < 24*time.Hour {
		return point.Format("01-02 15:04")
	}
	return point.Format("01-02")
}

func applyOpsTrend(target *opsTrend, row opsLogMetric) {
	target.Requests++
	target.Tokens += opsTokens(row)
	target.CostMicro += row.CostMicro
	target.latencyTotal += row.DurationMs
	if isOpsSuccess(row.StatusCode) {
		target.SuccessRequests++
	} else {
		target.ErrorRequests++
	}
}

func applyOpsRank(target *opsRank, row opsLogMetric) {
	target.Requests++
	target.Tokens += opsTokens(row)
	target.InputTokens += row.InputTokens
	target.OutputTokens += row.OutputTokens
	target.CacheReadTokens += row.CacheReadTokens
	target.CacheWriteTokens += row.CacheWriteTokens
	target.CacheWrite5mTokens += row.CacheWrite5mTokens
	target.CacheWrite1hTokens += row.CacheWrite1hTokens
	target.CostMicro += row.CostMicro
	target.latencyTotal += row.DurationMs
	if isOpsSuccess(row.StatusCode) {
		target.SuccessRequests++
	} else {
		target.ErrorRequests++
	}
}

func fillOpsRankNames(db *gorm.DB, groups, users, accounts map[int64]*opsRank, health []opsAccountHealth) {
	if len(groups) > 0 {
		var rows []model.Group
		db.Where("id IN ?", rankIDs(groups)).Find(&rows)
		for _, row := range rows {
			groups[row.ID].Name = row.Name
		}
	}
	if len(users) > 0 {
		var rows []model.User
		db.Where("id IN ?", rankIDs(users)).Find(&rows)
		for _, row := range rows {
			users[row.ID].Name = row.Email
		}
	}
	for _, row := range health {
		if rank, ok := accounts[row.ID]; ok {
			rank.Name = row.Name
		}
	}
	if len(accounts) > 0 {
		var rows []model.UpstreamAccount
		db.Where("id IN ?", rankIDs(accounts)).Find(&rows)
		for _, row := range rows {
			accounts[row.ID].Name = row.Name
		}
	}
}

func rankIDs(items map[int64]*opsRank) []int64 {
	ids := make([]int64, 0, len(items))
	for id := range items {
		if id > 0 {
			ids = append(ids, id)
		}
	}
	return ids
}

func sortedOpsRanks[K comparable](items map[K]*opsRank) []opsRank {
	out := make([]opsRank, 0, len(items))
	for _, item := range items {
		if item.Requests > 0 {
			item.AverageLatencyMs = float64(item.latencyTotal) / float64(item.Requests)
		}
		out = append(out, *item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Requests == out[j].Requests {
			return out[i].CostMicro > out[j].CostMicro
		}
		return out[i].Requests > out[j].Requests
	})
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

// detailedOpsRanks exposes a complete, bounded model ledger rather than just
// the eight-card overview. One hundred distinct public models is ample for a
// 31-day operations window and prevents an accidental unbounded response from
// a malformed client model name.
func detailedOpsRanks(items map[string]*opsRank) []opsRank {
	out := make([]opsRank, 0, len(items))
	for _, item := range items {
		if item.Requests > 0 {
			item.AverageLatencyMs = float64(item.latencyTotal) / float64(item.Requests)
		}
		out = append(out, *item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CostMicro == out[j].CostMicro {
			return out[i].Requests > out[j].Requests
		}
		return out[i].CostMicro > out[j].CostMicro
	})
	if len(out) > 100 {
		out = out[:100]
	}
	return out
}

func ratio(numerator, denominator int64) float64 {
	if denominator <= 0 {
		return 0
	}
	return math.Round(float64(numerator)/float64(denominator)*10_000) / 100
}

func percentile(values []int64, percentile float64) int64 {
	if len(values) == 0 {
		return 0
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	index := int(math.Ceil(float64(len(values))*percentile)) - 1
	if index < 0 {
		index = 0
	}
	return values[index]
}

func calculateOpsHealth(overview opsOverview) int {
	if overview.AccountTotal == 0 {
		return 0
	}
	score := 100 - int(math.Round(overview.ErrorRate*1.5))
	if overview.AccountAvailable == 0 {
		score = min(score, 25)
	}
	score -= int(math.Round(float64(overview.AccountCooling) / float64(overview.AccountTotal) * 30))
	score -= int(math.Round(float64(overview.AccountAttention) / float64(overview.AccountTotal) * 12))
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}
