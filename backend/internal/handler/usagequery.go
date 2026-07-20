package handler

import (
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"time"

	"dengdeng/internal/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const maxUsagePageSize = 100

// usageQuery is shared by the user-facing history, the admin ledger, CSV
// export and the operations dashboard. Keeping all filters in one place
// prevents a dashboard number and the corresponding detail view disagreeing.
type usageQuery struct {
	Page      int
	Size      int
	Model     string
	RequestID string
	Platform  string
	Status    string
	Start     *time.Time
	End       *time.Time
	UserID    int64
	APIKeyID  int64
	GroupID   int64
	AccountID int64
	Stream    *bool
	Sort      string
	Order     string
}

func parseUsageQuery(c *gin.Context) (usageQuery, error) {
	page, err := parsePositiveInt(c.DefaultQuery("page", "1"), "page", 1, 1_000_000)
	if err != nil {
		return usageQuery{}, err
	}
	size, err := parsePositiveInt(c.DefaultQuery("size", "20"), "size", 1, maxUsagePageSize)
	if err != nil {
		return usageQuery{}, err
	}

	q := usageQuery{
		Page:      page,
		Size:      size,
		Model:     strings.TrimSpace(c.Query("model")),
		RequestID: strings.TrimSpace(c.Query("request_id")),
		Platform:  strings.TrimSpace(c.Query("platform")),
		Status:    strings.TrimSpace(c.Query("status")),
		Sort:      strings.TrimSpace(c.DefaultQuery("sort", "created_at")),
		Order:     strings.ToLower(strings.TrimSpace(c.DefaultQuery("order", "desc"))),
	}
	if len(q.RequestID) > 64 {
		return usageQuery{}, fmt.Errorf("request_id is too long")
	}
	if q.Platform != "" && !validPlatform(q.Platform) {
		return usageQuery{}, fmt.Errorf("invalid platform")
	}
	if q.Status != "" && q.Status != "success" && q.Status != "error" {
		return usageQuery{}, fmt.Errorf("status must be success or error")
	}
	if q.Sort != "created_at" && q.Sort != "cost_micro" && q.Sort != "first_token_ms" && q.Sort != "duration_ms" && q.Sort != "status_code" {
		return usageQuery{}, fmt.Errorf("invalid sort")
	}
	if q.Order != "asc" && q.Order != "desc" {
		return usageQuery{}, fmt.Errorf("order must be asc or desc")
	}

	var ok bool
	if q.Start, ok, err = parseUsageTime(firstQuery(c, "start", "start_time"), false); err != nil {
		return usageQuery{}, err
	} else if !ok {
		q.Start = nil
	}
	if q.End, ok, err = parseUsageTime(firstQuery(c, "end", "end_time"), true); err != nil {
		return usageQuery{}, err
	} else if !ok {
		q.End = nil
	}
	if q.Start != nil && q.End != nil && !q.Start.Before(*q.End) {
		return usageQuery{}, fmt.Errorf("start must be earlier than end")
	}

	for key, target := range map[string]*int64{
		"user_id": &q.UserID, "api_key_id": &q.APIKeyID, "group_id": &q.GroupID, "account_id": &q.AccountID,
	} {
		if raw := strings.TrimSpace(c.Query(key)); raw != "" {
			id, parseErr := strconv.ParseInt(raw, 10, 64)
			if parseErr != nil || id <= 0 {
				return usageQuery{}, fmt.Errorf("%s must be a positive integer", key)
			}
			*target = id
		}
	}
	if raw := strings.TrimSpace(c.Query("stream")); raw != "" {
		value, parseErr := strconv.ParseBool(raw)
		if parseErr != nil {
			return usageQuery{}, fmt.Errorf("stream must be true or false")
		}
		q.Stream = &value
	}
	return q, nil
}

func firstQuery(c *gin.Context, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(c.Query(name)); value != "" {
			return value
		}
	}
	return ""
}

func parsePositiveInt(raw, field string, min, max int) (int, error) {
	n, err := strconv.Atoi(raw)
	if err != nil || n < min || n > max {
		return 0, fmt.Errorf("%s must be between %d and %d", field, min, max)
	}
	return n, nil
}

// parseUsageTime accepts RFC3339 and a convenient date-only form. A date used
// as an end boundary is exclusive of the following midnight, so
// ?start=2026-07-01&end=2026-07-01 means the entire calendar day.
func parseUsageTime(raw string, endOfDate bool) (*time.Time, bool, error) {
	if raw == "" {
		return nil, false, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			utc := parsed.UTC()
			return &utc, true, nil
		}
	}
	if parsed, err := time.Parse("2006-01-02", raw); err == nil {
		if endOfDate {
			parsed = parsed.AddDate(0, 0, 1)
		}
		return &parsed, true, nil
	}
	return nil, false, fmt.Errorf("time must be RFC3339 or YYYY-MM-DD")
}

// usageScope contains the complete filter vocabulary. userID is supplied by
// the authenticated user route and deliberately wins over a user_id query
// parameter, so a regular user can never inspect another account's history.
func usageScope(db *gorm.DB, filter usageQuery, userID *int64) *gorm.DB {
	q := db.Model(&model.UsageLog{})
	if userID != nil {
		q = q.Where("usage_logs.user_id = ?", *userID)
	} else if filter.UserID > 0 {
		q = q.Where("usage_logs.user_id = ?", filter.UserID)
	}
	if filter.APIKeyID > 0 {
		q = q.Where("usage_logs.api_key_id = ?", filter.APIKeyID)
	}
	if filter.GroupID > 0 {
		q = q.Where("usage_logs.group_id = ?", filter.GroupID)
	}
	if filter.AccountID > 0 {
		q = q.Where("usage_logs.account_id = ?", filter.AccountID)
	}
	if filter.Platform != "" {
		q = q.Joins("JOIN groups usage_groups ON usage_groups.id = usage_logs.group_id").Where("usage_groups.platform = ?", filter.Platform)
	}
	if filter.Model != "" {
		q = q.Where("usage_logs.model LIKE ?", "%"+filter.Model+"%")
	}
	if filter.RequestID != "" {
		q = q.Where("usage_logs.request_id = ?", filter.RequestID)
	}
	if filter.Start != nil {
		q = q.Where("usage_logs.created_at >= ?", *filter.Start)
	}
	if filter.End != nil {
		q = q.Where("usage_logs.created_at < ?", *filter.End)
	}
	if filter.Status == "success" {
		q = q.Where("usage_logs.status_code >= ? AND usage_logs.status_code < ?", 200, 400)
	} else if filter.Status == "error" {
		q = q.Where("usage_logs.status_code < ? OR usage_logs.status_code >= ?", 200, 400)
	}
	if filter.Stream != nil {
		q = q.Where("usage_logs.stream = ?", *filter.Stream)
	}
	return q
}

// queryUsage returns a paginated usage log slice, optionally scoped to one
// user (nil = all users, admin view).
func queryUsage(db *gorm.DB, filter usageQuery, userID *int64) ([]model.UsageLog, int64, error) {
	q := usageScope(db, filter, userID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var logs []model.UsageLog
	order := "usage_logs." + filter.Sort + " " + strings.ToUpper(filter.Order) + ", usage_logs.id DESC"
	if err := q.Order(order).Offset((filter.Page - 1) * filter.Size).Limit(filter.Size).Find(&logs).Error; err != nil {
		return nil, 0, err
	}
	decorateUsage(db, logs)
	return logs, total, nil
}

// decorateUsage fills display-only fields resolved from related tables.
func decorateUsage(db *gorm.DB, logs []model.UsageLog) {
	if len(logs) == 0 {
		return
	}
	userIDs := map[int64]bool{}
	keyIDs := map[int64]bool{}
	groupIDs := map[int64]bool{}
	accountIDs := map[int64]bool{}
	for _, l := range logs {
		userIDs[l.UserID] = true
		keyIDs[l.APIKeyID] = true
		groupIDs[l.GroupID] = true
		accountIDs[l.AccountID] = true
	}
	users := map[int64]string{}
	var us []model.User
	db.Where("id IN ?", keys(userIDs)).Find(&us)
	for _, u := range us {
		users[u.ID] = u.Email
	}
	keyNames := map[int64]string{}
	var ks []model.APIKey
	db.Unscoped().Where("id IN ?", keys(keyIDs)).Find(&ks)
	for _, k := range ks {
		keyNames[k.ID] = k.Name
	}
	groupNames := map[int64]string{}
	var gs []model.Group
	db.Where("id IN ?", keys(groupIDs)).Find(&gs)
	for _, g := range gs {
		groupNames[g.ID] = g.Name
	}
	accountNames := map[int64]string{}
	var accounts []model.UpstreamAccount
	db.Where("id IN ?", keys(accountIDs)).Find(&accounts)
	for _, account := range accounts {
		accountNames[account.ID] = account.Name
	}
	for i := range logs {
		logs[i].UserEmail = users[logs[i].UserID]
		logs[i].KeyName = keyNames[logs[i].APIKeyID]
		logs[i].GroupName = groupNames[logs[i].GroupID]
		logs[i].AccountName = accountNames[logs[i].AccountID]
	}
}

func keys(m map[int64]bool) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		if k > 0 {
			out = append(out, k)
		}
	}
	return out
}

const maxUsageExportRows = 10_000

// prepareUsageExport applies a finite default range and cap before writing any
// response headers. The same limits are used for user and admin exports so a
// spreadsheet download cannot turn into an unbounded database scan.
func prepareUsageExport(filter *usageQuery) error {
	if filter.Start == nil {
		start := time.Now().UTC().AddDate(0, 0, -30)
		filter.Start = &start
	}
	if filter.End == nil {
		end := time.Now().UTC()
		filter.End = &end
	}
	if filter.End.Sub(*filter.Start) > 92*24*time.Hour {
		return fmt.Errorf("export range cannot exceed 92 days")
	}
	filter.Page = 1
	filter.Size = maxUsageExportRows
	return nil
}

func writeUsageCSV(c *gin.Context, db *gorm.DB, filter usageQuery, userID *int64, includeInternal bool) error {
	q := usageScope(db, filter, userID)
	var logs []model.UsageLog
	order := "usage_logs." + filter.Sort + " " + strings.ToUpper(filter.Order) + ", usage_logs.id DESC"
	if err := q.Order(order).Limit(filter.Size).Find(&logs).Error; err != nil {
		return err
	}
	decorateUsage(db, logs)

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=usage-"+time.Now().UTC().Format("20060102")+".csv")
	c.Writer.Write([]byte{0xEF, 0xBB, 0xBF}) // Excel's UTF-8 marker
	w := csv.NewWriter(c.Writer)
	header := []string{"请求 ID", "时间", "密钥", "分组", "模型", "思考强度 Reasoning Effort", "流式", "输入 Token", "输出 Token", "缓存读", "缓存写", "5m 缓存写", "1h 缓存写", "图片数", "费用(USD)", "首字耗时(ms)", "总耗时(ms)", "状态码", "错误"}
	if includeInternal {
		header = []string{"请求 ID", "时间", "用户", "密钥", "分组", "上游账号", "模型", "思考强度 Reasoning Effort", "流式", "输入 Token", "输出 Token", "缓存读", "缓存写", "5m 缓存写", "1h 缓存写", "图片数", "费用(USD)", "首字耗时(ms)", "总耗时(ms)", "排队(ms)", "调度(ms)", "上游(ms)", "尝试次数", "状态码", "错误"}
	}
	_ = w.Write(header)
	for _, entry := range logs {
		row := make([]string, 0, len(header))
		if includeInternal {
			row = append(row,
				entry.RequestID, entry.CreatedAt.UTC().Format(time.RFC3339), entry.UserEmail, entry.KeyName, entry.GroupName, entry.AccountName,
			)
		} else {
			row = append(row,
				entry.RequestID, entry.CreatedAt.UTC().Format(time.RFC3339), entry.KeyName, entry.GroupName,
			)
		}
		row = append(row,
			entry.Model, entry.ReasoningEffort, strconv.FormatBool(entry.Stream),
			strconv.FormatInt(entry.InputTokens, 10), strconv.FormatInt(entry.OutputTokens, 10),
			strconv.FormatInt(entry.CacheReadTokens, 10), strconv.FormatInt(entry.CacheWriteTokens, 10),
			strconv.FormatInt(entry.CacheWrite5mTokens, 10), strconv.FormatInt(entry.CacheWrite1hTokens, 10),
			strconv.FormatInt(entry.ImageCount, 10), fmt.Sprintf("%.6f", float64(entry.CostMicro)/1_000_000),
			strconv.FormatInt(entry.FirstTokenMs, 10), strconv.FormatInt(entry.DurationMs, 10),
		)
		if includeInternal {
			row = append(row,
				strconv.FormatInt(entry.QueueMs, 10), strconv.FormatInt(entry.ScheduleMs, 10),
				strconv.FormatInt(entry.UpstreamMs, 10), strconv.Itoa(entry.AttemptCount),
			)
		}
		row = append(row, strconv.Itoa(entry.StatusCode), entry.ErrorMessage)
		_ = w.Write(row)
	}
	w.Flush()
	return w.Error()
}

type summaryRow struct {
	Requests     int64 `json:"requests"`
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	CostMicro    int64 `json:"cost_micro"`
}

type dailyRow struct {
	Day       string `json:"day"`
	Requests  int64  `json:"requests"`
	Tokens    int64  `json:"tokens"`
	CostMicro int64  `json:"cost_micro"`
}

// usageSummary aggregates today / last 30 days plus a per-day series used by
// dashboard charts.
func usageSummary(db *gorm.DB, userID *int64) gin.H {
	scope := func() *gorm.DB {
		q := db.Model(&model.UsageLog{})
		if userID != nil {
			q = q.Where("user_id = ?", *userID)
		}
		return q
	}
	agg := "COUNT(*) AS requests, COALESCE(SUM(input_tokens),0) AS input_tokens, COALESCE(SUM(output_tokens),0) AS output_tokens, COALESCE(SUM(cost_micro),0) AS cost_micro"

	today := time.Now().Truncate(24 * time.Hour)
	var todayRow, monthRow summaryRow
	scope().Select(agg).Where("created_at >= ?", today).Scan(&todayRow)
	scope().Select(agg).Where("created_at >= ?", today.AddDate(0, 0, -29)).Scan(&monthRow)

	var daily []dailyRow
	var raw []model.UsageLog
	scope().Select("created_at, input_tokens, output_tokens, cost_micro").
		Where("created_at >= ?", today.AddDate(0, 0, -13)).Find(&raw)
	byDay := map[string]*dailyRow{}
	for _, l := range raw {
		day := l.CreatedAt.Format("01-02")
		row, ok := byDay[day]
		if !ok {
			row = &dailyRow{Day: day}
			byDay[day] = row
		}
		row.Requests++
		row.Tokens += l.InputTokens + l.OutputTokens
		row.CostMicro += l.CostMicro
	}
	for i := 13; i >= 0; i-- {
		day := today.AddDate(0, 0, -i).Format("01-02")
		if row, ok := byDay[day]; ok {
			daily = append(daily, *row)
		} else {
			daily = append(daily, dailyRow{Day: day})
		}
	}

	return gin.H{"today": todayRow, "month": monthRow, "daily": daily}
}
