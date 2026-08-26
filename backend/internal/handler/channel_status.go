package handler

import (
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"dengdeng/internal/middleware"
	"dengdeng/internal/model"
	"dengdeng/internal/util"

	"gorm.io/gorm"
)

const channelStatusBucketCount = 60

const channelStatusCurrentRequestWindow = 15 * time.Minute

type channelStatusBucket struct {
	At    time.Time `json:"at"`
	State string    `json:"state"`
}

type channelGroupStatus struct {
	ID                    int64                 `json:"id"`
	Name                  string                `json:"name"`
	Description           string                `json:"description"`
	Platform              string                `json:"platform"`
	IsPublic              bool                  `json:"is_public"`
	State                 string                `json:"state"`
	StateSource           string                `json:"state_source"`
	AccountTotal          int                   `json:"account_total"`
	AccountEligible       int                   `json:"account_eligible"`
	AccountChecked        int                   `json:"account_checked"`
	AccountAvailable      int                   `json:"account_available"`
	LastProbeAt           *time.Time            `json:"last_probe_at,omitempty"`
	LastRequestAt         *time.Time            `json:"last_request_at,omitempty"`
	AverageProbeLatencyMs int64                 `json:"average_probe_latency_ms"`
	ProbeSuccessRate      float64               `json:"probe_success_rate"`
	ProbeSuccesses        int64                 `json:"probe_successes"`
	ProbeTotal            int64                 `json:"probe_total"`
	AverageTTFTMs         int64                 `json:"average_ttft_ms"`
	RequestSuccessRate    float64               `json:"request_success_rate"`
	RequestSuccesses      int64                 `json:"request_successes"`
	RequestTotal          int64                 `json:"request_total"`
	CurrentRequestRate    float64               `json:"current_request_success_rate"`
	CurrentRequestOK      int64                 `json:"current_request_successes"`
	CurrentRequestTotal   int64                 `json:"current_request_total"`
	TopModel              string                `json:"top_model"`
	Timeline              []channelStatusBucket `json:"timeline"`
}

type channelStatusResponse struct {
	Range                string               `json:"range"`
	Hours                int                  `json:"hours"`
	GeneratedAt          time.Time            `json:"generated_at"`
	LastProbeAt          *time.Time           `json:"last_probe_at,omitempty"`
	AdminView            bool                 `json:"admin_view"`
	ProbeIntervalSeconds int                  `json:"probe_interval_seconds"`
	CurrentWindowMinutes int                  `json:"current_window_minutes"`
	Groups               []channelGroupStatus `json:"groups"`
}

type channelProbeAggregate struct {
	Total          int64
	Successes      int64
	LatencyTotal   int64
	LatencySamples int64
	Timeline       []channelStatusBucket
}

type channelRequestAggregate struct {
	GroupID          int64
	RequestTotal     int64
	RequestSuccesses int64
	AverageTTFTMs    float64
}

type channelTopModelRow struct {
	GroupID int64
	Model   string
	Calls   int64
}

func channelStatusHours(value string) (string, int) {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "3h":
		return "3h", 3
	case "24h", "1d":
		return "24h", 24
	case "7d":
		return "7d", 24 * 7
	case "15d":
		return "15d", 24 * 15
	default:
		return "1h", 1
	}
}

func channelPercentage(successes, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return math.Round(float64(successes)*10000/float64(total)) / 100
}

func channelProbeStateRank(state string) int {
	switch state {
	case "healthy":
		return 4
	case "degraded":
		return 3
	case "down":
		return 2
	case "expired":
		return 1
	default:
		return 0
	}
}

func mergeChannelBucketState(current, next string) string {
	if channelProbeStateRank(next) > channelProbeStateRank(current) {
		return next
	}
	return current
}

func channelTrafficState(successes, total int64) string {
	if total <= 0 {
		return ""
	}
	if successes == 0 && total >= 3 {
		return "down"
	}
	if channelPercentage(successes, total) < 80 {
		return "degraded"
	}
	return "healthy"
}

// ChannelStatus returns privacy-safe group health. Administrators can inspect
// every group; ordinary users are filtered to active public groups before any
// account, probe, or usage rows are loaded.
func (h *UserHandler) ChannelStatus(c *gin.Context) {
	user := middleware.CurrentUser(c)
	rangeName, hours := channelStatusHours(c.Query("range"))
	now := time.Now().UTC()
	start := now.Add(-time.Duration(hours) * time.Hour)
	adminView := user.Role == model.RoleAdmin
	probeIntervalSeconds := 300
	if settings, err := h.settings.Get(); err == nil && settings.Features.ChannelMonitorIntervalSeconds >= 15 {
		probeIntervalSeconds = settings.Features.ChannelMonitorIntervalSeconds
	}
	probeFreshness := 3 * time.Duration(probeIntervalSeconds) * time.Second
	if probeFreshness < 3*time.Minute {
		probeFreshness = 3 * time.Minute
	}

	var groups []model.Group
	groupQuery := h.db.Order("platform, name, id")
	if !adminView {
		groupQuery = groupQuery.Where("is_public = ? AND status = ?", true, model.StatusActive)
	}
	if err := groupQuery.Find(&groups).Error; err != nil {
		util.Fail(c, http.StatusInternalServerError, "load channel groups failed")
		return
	}
	if len(groups) == 0 {
		util.OK(c, channelStatusResponse{Range: rangeName, Hours: hours, GeneratedAt: now, AdminView: adminView, ProbeIntervalSeconds: probeIntervalSeconds, CurrentWindowMinutes: int(channelStatusCurrentRequestWindow.Minutes()), Groups: []channelGroupStatus{}})
		return
	}

	groupByID := make(map[int64]model.Group, len(groups))
	groupIDs := make([]int64, 0, len(groups))
	for _, group := range groups {
		groupByID[group.ID] = group
		groupIDs = append(groupIDs, group.ID)
	}

	type accountRow struct {
		ID            int64
		GroupID       int64
		Status        string
		CooldownUntil *time.Time
	}
	var accounts []accountRow
	if err := h.db.Model(&model.UpstreamAccount{}).
		Select("id, group_id, status, cooldown_until").
		Where("group_id IN ? OR id IN (?)", groupIDs, h.db.Model(&model.UpstreamAccountGroup{}).Select("upstream_account_id").Where("group_id IN ?", groupIDs)).
		Find(&accounts).Error; err != nil {
		util.Fail(c, http.StatusInternalServerError, "load channel accounts failed")
		return
	}

	accountByID := make(map[int64]accountRow, len(accounts))
	accountGroups := make(map[int64]map[int64]struct{}, len(accounts))
	accountIDs := make([]int64, 0, len(accounts))
	for _, account := range accounts {
		accountByID[account.ID] = account
		accountIDs = append(accountIDs, account.ID)
		accountGroups[account.ID] = map[int64]struct{}{}
		if _, ok := groupByID[account.GroupID]; ok {
			accountGroups[account.ID][account.GroupID] = struct{}{}
		}
	}
	if len(accountIDs) > 0 {
		var links []model.UpstreamAccountGroup
		if err := h.db.Where("upstream_account_id IN ? AND group_id IN ?", accountIDs, groupIDs).Find(&links).Error; err != nil {
			util.Fail(c, http.StatusInternalServerError, "load channel memberships failed")
			return
		}
		for _, link := range links {
			if accountGroups[link.UpstreamAccountID] == nil {
				accountGroups[link.UpstreamAccountID] = map[int64]struct{}{}
			}
			accountGroups[link.UpstreamAccountID][link.GroupID] = struct{}{}
		}
	}

	bucketDuration := now.Sub(start) / channelStatusBucketCount
	probeAggregates := make(map[int64]*channelProbeAggregate, len(groups))
	for _, group := range groups {
		timeline := make([]channelStatusBucket, channelStatusBucketCount)
		for index := range timeline {
			timeline[index] = channelStatusBucket{At: start.Add(time.Duration(index) * bucketDuration), State: "unknown"}
		}
		probeAggregates[group.ID] = &channelProbeAggregate{Timeline: timeline}
	}

	latestProbeByAccount := make(map[int64]model.AccountProbe, len(accounts))
	var lastProbeAt *time.Time
	if len(accountIDs) > 0 {
		var latestProbes []model.AccountProbe
		if err := h.db.Raw(`
			SELECT probes.account_id, probes.state, probes.latency_ms, probes.checked_at
			FROM account_probes probes
			JOIN (
				SELECT account_id, MAX(checked_at) AS checked_at
				FROM account_probes
				WHERE account_id IN ?
				GROUP BY account_id
			) latest ON latest.account_id = probes.account_id AND latest.checked_at = probes.checked_at
		`, accountIDs).Scan(&latestProbes).Error; err != nil {
			util.Fail(c, http.StatusInternalServerError, "load latest channel probes failed")
			return
		}
		for _, probe := range latestProbes {
			latestProbeByAccount[probe.AccountID] = probe
			if lastProbeAt == nil || probe.CheckedAt.After(*lastProbeAt) {
				checkedAt := probe.CheckedAt
				lastProbeAt = &checkedAt
			}
		}

		var probes []model.AccountProbe
		if err := h.db.Select("account_id, state, latency_ms, checked_at").
			Where("account_id IN ? AND checked_at >= ?", accountIDs, start).
			Order("checked_at DESC").Limit(50000).Find(&probes).Error; err != nil {
			util.Fail(c, http.StatusInternalServerError, "load channel probes failed")
			return
		}
		for _, probe := range probes {
			bucketIndex := int(probe.CheckedAt.Sub(start) / bucketDuration)
			if bucketIndex < 0 {
				continue
			}
			if bucketIndex >= channelStatusBucketCount {
				bucketIndex = channelStatusBucketCount - 1
			}
			for groupID := range accountGroups[probe.AccountID] {
				aggregate := probeAggregates[groupID]
				if aggregate == nil {
					continue
				}
				aggregate.Total++
				if probe.State == "healthy" {
					aggregate.Successes++
				}
				if probe.LatencyMs > 0 {
					aggregate.LatencyTotal += probe.LatencyMs
					aggregate.LatencySamples++
				}
				aggregate.Timeline[bucketIndex].State = mergeChannelBucketState(aggregate.Timeline[bucketIndex].State, probe.State)
			}
		}
	}

	requestByGroup := make(map[int64]channelRequestAggregate, len(groups))
	var requestRows []channelRequestAggregate
	if err := h.db.Model(&model.UsageLog{}).
		Select(`group_id,
			COALESCE(SUM(CASE WHEN (status_code >= 200 AND status_code < 400) OR status_code IN (401, 403, 408, 429) OR status_code >= 500 THEN 1 ELSE 0 END), 0) AS request_total,
			COALESCE(SUM(CASE WHEN status_code >= 200 AND status_code < 400 THEN 1 ELSE 0 END), 0) AS request_successes,
			COALESCE(AVG(CASE WHEN status_code >= 200 AND status_code < 400 AND first_token_ms > 0 THEN first_token_ms END), 0) AS average_ttft_ms`).
		Where("group_id IN ? AND created_at >= ?", groupIDs, start).
		Group("group_id").Scan(&requestRows).Error; err != nil {
		util.Fail(c, http.StatusInternalServerError, "load channel requests failed")
		return
	}
	for _, row := range requestRows {
		requestByGroup[row.GroupID] = row
	}
	lastRequestByGroup := make(map[int64]*time.Time, len(groups))
	for _, groupID := range groupIDs {
		var latest model.UsageLog
		err := h.db.Select("group_id, created_at").
			Where("group_id = ? AND created_at >= ? AND ((status_code >= 200 AND status_code < 400) OR status_code IN (401, 403, 408, 429) OR status_code >= 500)", groupID, start).
			Order("created_at DESC").Take(&latest).Error
		if err == nil {
			createdAt := latest.CreatedAt
			lastRequestByGroup[groupID] = &createdAt
		} else if err != gorm.ErrRecordNotFound {
			util.Fail(c, http.StatusInternalServerError, "load latest channel request failed")
			return
		}
	}

	currentRequestByGroup := make(map[int64]channelRequestAggregate, len(groups))
	var currentRequestRows []channelRequestAggregate
	if err := h.db.Model(&model.UsageLog{}).
		Select(`group_id,
			COALESCE(SUM(CASE WHEN (status_code >= 200 AND status_code < 400) OR status_code IN (401, 403, 408, 429) OR status_code >= 500 THEN 1 ELSE 0 END), 0) AS request_total,
			COALESCE(SUM(CASE WHEN status_code >= 200 AND status_code < 400 THEN 1 ELSE 0 END), 0) AS request_successes`).
		Where("group_id IN ? AND created_at >= ?", groupIDs, now.Add(-channelStatusCurrentRequestWindow)).
		Group("group_id").Scan(&currentRequestRows).Error; err != nil {
		util.Fail(c, http.StatusInternalServerError, "load current channel requests failed")
		return
	}
	for _, row := range currentRequestRows {
		currentRequestByGroup[row.GroupID] = row
	}

	topModelByGroup := make(map[int64]string, len(groups))
	var modelRows []channelTopModelRow
	if err := h.db.Model(&model.UsageLog{}).
		Select("group_id, model, COUNT(*) AS calls").
		Where("group_id IN ? AND created_at >= ? AND model <> ''", groupIDs, start).
		Group("group_id, model").Order("group_id ASC, calls DESC, model ASC").Scan(&modelRows).Error; err != nil {
		util.Fail(c, http.StatusInternalServerError, "load channel models failed")
		return
	}
	for _, row := range modelRows {
		if topModelByGroup[row.GroupID] == "" {
			topModelByGroup[row.GroupID] = row.Model
		}
	}

	accountIDsByGroup := make(map[int64][]int64, len(groups))
	for accountID, memberships := range accountGroups {
		for groupID := range memberships {
			accountIDsByGroup[groupID] = append(accountIDsByGroup[groupID], accountID)
		}
	}

	items := make([]channelGroupStatus, 0, len(groups))
	for _, group := range groups {
		aggregate := probeAggregates[group.ID]
		requests := requestByGroup[group.ID]
		currentRequests := currentRequestByGroup[group.ID]
		accountIDs := accountIDsByGroup[group.ID]
		eligibleAccounts := 0
		checkedAccounts := 0
		availableAccounts := 0
		anyHealthy := false
		anyDegraded := false
		anyExpired := false
		anyProbed := false
		var groupLastProbeAt *time.Time
		for _, accountID := range accountIDs {
			account := accountByID[accountID]
			eligible := group.Status == model.StatusActive && account.Status == model.StatusActive && (account.CooldownUntil == nil || !account.CooldownUntil.After(now))
			if !eligible {
				continue
			}
			eligibleAccounts++
			probe, ok := latestProbeByAccount[accountID]
			if !ok {
				continue
			}
			if groupLastProbeAt == nil || probe.CheckedAt.After(*groupLastProbeAt) {
				checkedAt := probe.CheckedAt
				groupLastProbeAt = &checkedAt
			}
			if probe.CheckedAt.Before(now.Add(-probeFreshness)) {
				continue
			}
			checkedAccounts++
			anyProbed = true
			if probe.State == "healthy" {
				anyHealthy = true
				availableAccounts++
			} else if probe.State == "degraded" {
				anyDegraded = true
				availableAccounts++
			} else if probe.State == "expired" {
				anyExpired = true
			}
		}
		state := "unknown"
		stateSource := "probe"
		switch {
		case group.Status != model.StatusActive:
			state = "disabled"
			stateSource = "group"
		case len(accountIDs) == 0 || eligibleAccounts == 0:
			state = "down"
			stateSource = "accounts"
		case currentRequests.RequestTotal > 0:
			state = channelTrafficState(currentRequests.RequestSuccesses, currentRequests.RequestTotal)
			stateSource = "traffic"
		case anyHealthy:
			state = "healthy"
		case anyDegraded:
			state = "degraded"
		case anyExpired:
			state = "expired"
		case anyProbed:
			state = "down"
		}
		averageProbeLatency := int64(0)
		if aggregate.LatencySamples > 0 {
			averageProbeLatency = aggregate.LatencyTotal / aggregate.LatencySamples
		}
		items = append(items, channelGroupStatus{
			ID: group.ID, Name: group.Name, Description: group.Description, Platform: group.Platform, IsPublic: group.IsPublic,
			State: state, StateSource: stateSource, AccountTotal: len(accountIDs), AccountEligible: eligibleAccounts, AccountChecked: checkedAccounts, AccountAvailable: availableAccounts,
			LastProbeAt: groupLastProbeAt, LastRequestAt: lastRequestByGroup[group.ID],
			AverageProbeLatencyMs: averageProbeLatency, ProbeSuccessRate: channelPercentage(aggregate.Successes, aggregate.Total),
			ProbeSuccesses: aggregate.Successes, ProbeTotal: aggregate.Total, AverageTTFTMs: int64(math.Round(requests.AverageTTFTMs)),
			RequestSuccessRate: channelPercentage(requests.RequestSuccesses, requests.RequestTotal), RequestSuccesses: requests.RequestSuccesses,
			RequestTotal: requests.RequestTotal, CurrentRequestRate: channelPercentage(currentRequests.RequestSuccesses, currentRequests.RequestTotal),
			CurrentRequestOK: currentRequests.RequestSuccesses, CurrentRequestTotal: currentRequests.RequestTotal,
			TopModel: topModelByGroup[group.ID], Timeline: aggregate.Timeline,
		})
	}

	util.OK(c, channelStatusResponse{Range: rangeName, Hours: hours, GeneratedAt: now, LastProbeAt: lastProbeAt, AdminView: adminView, ProbeIntervalSeconds: probeIntervalSeconds, CurrentWindowMinutes: int(channelStatusCurrentRequestWindow.Minutes()), Groups: items})
}
