package service

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"dengdeng/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const opsCollectorJobName = "ops_metrics_collector"

type OpsCollector struct {
	db               *gorm.DB
	runtime          *RuntimeMetrics
	alerts           *AlertService
	stop             chan struct{}
	once             sync.Once
	resourceMu       sync.Mutex
	lastProcessCPU   uint64
	lastHostCPUIdle  uint64
	lastHostCPUTotal uint64
	lastNetworkRX    uint64
	lastNetworkTX    uint64
	lastResourceAt   time.Time
}

func NewOpsCollector(db *gorm.DB, runtimeMetrics *RuntimeMetrics, alerts *AlertService) *OpsCollector {
	return &OpsCollector{db: db, runtime: runtimeMetrics, alerts: alerts, stop: make(chan struct{})}
}

func (c *OpsCollector) Start() {
	if c == nil || c.db == nil {
		return
	}
	c.once.Do(func() { go c.run() })
}

func (c *OpsCollector) run() {
	c.collect()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.collect()
		case <-c.stop:
			return
		}
	}
}

func (c *OpsCollector) collect() {
	started := time.Now().UTC()
	end := started.Truncate(time.Minute)
	start := end.Add(-time.Minute)
	metric, err := c.collectMinute(start, end)
	if err == nil {
		err = c.db.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "bucket_at"}, {Name: "platform"}, {Name: "group_id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"success_count", "error_count", "business_limited_count", "upstream_429_count", "upstream_529_count", "upstream_other_errors",
				"token_consumed", "switch_count", "qps", "tps", "duration_p50_ms", "duration_p90_ms", "duration_p95_ms", "duration_p99_ms",
				"duration_avg_ms", "duration_max_ms", "ttft_p50_ms", "ttft_p90_ms", "ttft_p95_ms", "ttft_p99_ms", "ttft_avg_ms", "ttft_max_ms",
				"cpu_percent", "process_cpu_percent", "cpu_cores", "load_1", "load_5", "load_15", "host_uptime_seconds",
				"memory_used_bytes", "memory_total_bytes", "memory_percent", "process_rss_bytes", "disk_used_bytes", "disk_total_bytes", "disk_percent",
				"network_rx_bytes_per_sec", "network_tx_bytes_per_sec", "db_ok", "db_open_connections", "db_in_use", "db_idle",
				"db_wait_count", "goroutines", "in_flight", "queue_depth", "created_at",
			}),
		}).Create(&metric).Error
	}
	if err == nil {
		err = c.refreshAggregate("hour", end.Truncate(time.Hour), end.Truncate(time.Hour).Add(time.Hour))
	}
	if err == nil {
		day := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, time.UTC)
		err = c.refreshAggregate("day", day, day.Add(24*time.Hour))
	}
	finished := time.Now().UTC()
	heartbeat := model.OpsJobHeartbeat{JobName: opsCollectorJobName, LastRunAt: &started, LastDurationMs: finished.Sub(started).Milliseconds(), UpdatedAt: finished}
	if err != nil {
		heartbeat.LastErrorAt, heartbeat.LastError = &finished, trimOpsText(err.Error(), 2048)
		_ = c.writeSystemLog("error", "ops.collector", err.Error())
	} else {
		heartbeat.LastSuccessAt = &finished
		if c.alerts != nil {
			c.alerts.EvaluateMetricSnapshot(metric)
		}
	}
	_ = c.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "job_name"}}, DoUpdates: clause.AssignmentColumns([]string{"last_run_at", "last_success_at", "last_error_at", "last_error", "last_duration_ms", "updated_at"})}).Create(&heartbeat).Error
	// Retention is intentionally performed by the collector so monitoring data
	// cannot grow without bounds even when backup cleanup is disabled.
	_ = c.db.Where("bucket_at < ?", finished.AddDate(0, 0, -35)).Delete(&model.OpsSystemMetric{}).Error
	_ = c.db.Where("granularity = ? AND bucket_at < ?", "hour", finished.AddDate(0, -6, 0)).Delete(&model.OpsMetricAggregate{}).Error
	_ = c.db.Where("granularity = ? AND bucket_at < ?", "day", finished.AddDate(-2, 0, 0)).Delete(&model.OpsMetricAggregate{}).Error
}

type collectorUsage struct {
	GroupID          int64
	StatusCode       int
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	CostMicro        int64
	DurationMs       int64
	FirstTokenMs     int64
	AttemptCount     int
}

func (c *OpsCollector) collectMinute(start, end time.Time) (model.OpsSystemMetric, error) {
	var rows []collectorUsage
	err := c.db.Model(&model.UsageLog{}).Select("group_id, status_code, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_micro, duration_ms, first_token_ms, attempt_count").
		Where("created_at >= ? AND created_at < ?", start, end).Find(&rows).Error
	metric := model.OpsSystemMetric{BucketAt: start, WindowMinutes: 1, DBOK: err == nil, CreatedAt: time.Now().UTC()}
	if err != nil {
		return metric, err
	}
	durations, ttfts := make([]int64, 0, len(rows)), make([]int64, 0, len(rows))
	var durationTotal, ttftTotal int64
	for _, row := range rows {
		tokens := row.InputTokens + row.OutputTokens + row.CacheReadTokens + row.CacheWriteTokens
		metric.TokenConsumed += tokens
		if row.AttemptCount > 1 {
			metric.SwitchCount += int64(row.AttemptCount - 1)
		}
		if row.StatusCode >= 200 && row.StatusCode < 400 {
			metric.SuccessCount++
			if row.DurationMs >= 0 {
				durations = append(durations, row.DurationMs)
				durationTotal += row.DurationMs
			}
			if row.FirstTokenMs > 0 {
				ttfts = append(ttfts, row.FirstTokenMs)
				ttftTotal += row.FirstTokenMs
			}
		} else {
			metric.ErrorCount++
			if row.StatusCode == 429 {
				metric.BusinessLimitedCount++
				metric.Upstream429Count++
			} else if row.StatusCode == 529 {
				metric.Upstream529Count++
			} else if row.StatusCode >= 500 {
				metric.UpstreamOtherErrors++
			}
		}
	}
	metric.QPS = float64(len(rows)) / 60
	metric.TPS = float64(metric.TokenConsumed) / 60
	metric.DurationP50Ms, metric.DurationP90Ms = opsPercentile(durations, .50), opsPercentile(durations, .90)
	metric.DurationP95Ms, metric.DurationP99Ms = opsPercentile(durations, .95), opsPercentile(durations, .99)
	if len(durations) > 0 {
		metric.DurationAvgMs, metric.DurationMaxMs = float64(durationTotal)/float64(len(durations)), durations[len(durations)-1]
	}
	metric.TTFTP50Ms, metric.TTFTP90Ms = opsPercentile(ttfts, .50), opsPercentile(ttfts, .90)
	metric.TTFTP95Ms, metric.TTFTP99Ms = opsPercentile(ttfts, .95), opsPercentile(ttfts, .99)
	if len(ttfts) > 0 {
		metric.TTFTAvgMs, metric.TTFTMaxMs = float64(ttftTotal)/float64(len(ttfts)), ttfts[len(ttfts)-1]
	}
	c.collectSystemResources(&metric)
	metric.Goroutines = runtime.NumGoroutine()
	if sqlDB, dbErr := c.db.DB(); dbErr == nil {
		if pingErr := sqlDB.Ping(); pingErr != nil {
			metric.DBOK = false
		}
		stats := sqlDB.Stats()
		metric.DBOpenConnections, metric.DBInUse, metric.DBIdle, metric.DBWaitCount = stats.OpenConnections, stats.InUse, stats.Idle, stats.WaitCount
	}
	if c.runtime != nil {
		snapshot := c.runtime.Snapshot("", 0)
		metric.InFlight, metric.QueueDepth = snapshot.InFlight, snapshot.Waiting
	}
	return metric, nil
}

func (c *OpsCollector) refreshAggregate(granularity string, start, end time.Time) error {
	var rows []collectorUsage
	if err := c.db.Model(&model.UsageLog{}).Select("group_id, status_code, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_micro, duration_ms, first_token_ms, attempt_count").
		Where("created_at >= ? AND created_at < ?", start, end).Find(&rows).Error; err != nil {
		return err
	}
	var groups []model.Group
	if err := c.db.Select("id", "platform").Find(&groups).Error; err != nil {
		return err
	}
	platformByGroup := make(map[int64]string, len(groups))
	for _, group := range groups {
		platformByGroup[group.ID] = group.Platform
	}
	type scope struct {
		platform string
		groupID  int64
	}
	values := map[scope]*model.OpsMetricAggregate{}
	ensure := func(key scope) *model.OpsMetricAggregate {
		if values[key] == nil {
			values[key] = &model.OpsMetricAggregate{Granularity: granularity, BucketAt: start, Platform: key.platform, GroupID: key.groupID}
		}
		return values[key]
	}
	ensure(scope{})
	for _, row := range rows {
		platform := platformByGroup[row.GroupID]
		for _, key := range []scope{{}, {platform: platform}, {platform: platform, groupID: row.GroupID}} {
			item := ensure(key)
			item.Requests++
			if row.StatusCode >= 200 && row.StatusCode < 400 {
				item.SuccessCount++
			} else {
				item.ErrorCount++
			}
			item.InputTokens += row.InputTokens
			item.OutputTokens += row.OutputTokens
			item.CacheTokens += row.CacheReadTokens + row.CacheWriteTokens
			item.CostMicro += row.CostMicro
			item.DurationTotal += row.DurationMs
			if row.DurationMs > item.DurationMax {
				item.DurationMax = row.DurationMs
			}
			if row.FirstTokenMs > 0 {
				item.TTFTTotal += row.FirstTokenMs
				item.TTFTSamples++
			}
			if row.AttemptCount > 1 {
				item.SwitchCount += int64(row.AttemptCount - 1)
			}
		}
	}
	for _, item := range values {
		if err := c.db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "granularity"}, {Name: "bucket_at"}, {Name: "platform"}, {Name: "group_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"requests", "success_count", "error_count", "input_tokens", "output_tokens", "cache_tokens", "cost_micro", "duration_total", "duration_max", "ttft_total", "ttft_samples", "switch_count", "updated_at"}),
		}).Create(item).Error; err != nil {
			return err
		}
	}
	return nil
}

func opsPercentile(values []int64, quantile float64) int64 {
	if len(values) == 0 {
		return 0
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	index := int(float64(len(values)-1)*quantile + .5)
	if index < 0 {
		index = 0
	}
	if index >= len(values) {
		index = len(values) - 1
	}
	return values[index]
}

func (c *OpsCollector) collectSystemResources(metric *model.OpsSystemMetric) {
	if metric == nil {
		return
	}
	metric.CPUCores = runtime.NumCPU()
	metric.MemoryUsedBytes, metric.MemoryTotalBytes = linuxMemory()
	if metric.MemoryTotalBytes > 0 {
		metric.MemoryPercent = percentOf(metric.MemoryUsedBytes, metric.MemoryTotalBytes)
	}
	metric.ProcessRSSBytes = processRSSBytes()
	metric.Load1, metric.Load5, metric.Load15 = linuxLoadAverage()
	metric.HostUptimeSeconds = linuxUptimeSeconds()
	metric.DiskUsedBytes, metric.DiskTotalBytes = diskUsage("/var/lib/dengdeng")
	if metric.DiskTotalBytes == 0 {
		metric.DiskUsedBytes, metric.DiskTotalBytes = diskUsage("/")
	}
	if metric.DiskTotalBytes > 0 {
		metric.DiskPercent = percentOf(metric.DiskUsedBytes, metric.DiskTotalBytes)
	}

	now := time.Now()
	processTicks := processCPUTicks()
	hostIdle, hostTotal := linuxCPUTicks()
	networkRX, networkTX := linuxNetworkBytes()
	c.resourceMu.Lock()
	if c.lastHostCPUTotal > 0 {
		metric.CPUPercent = hostCPUPercent(c.lastHostCPUIdle, c.lastHostCPUTotal, hostIdle, hostTotal)
	}
	if !c.lastResourceAt.IsZero() {
		seconds := now.Sub(c.lastResourceAt).Seconds()
		if seconds > 0 {
			if processTicks >= c.lastProcessCPU {
				metric.ProcessCPUPercent = float64(processTicks-c.lastProcessCPU) / 100 / seconds / float64(max(runtime.NumCPU(), 1)) * 100
			}
			if networkRX >= c.lastNetworkRX {
				metric.NetworkRXBytesPerSec = float64(networkRX-c.lastNetworkRX) / seconds
			}
			if networkTX >= c.lastNetworkTX {
				metric.NetworkTXBytesPerSec = float64(networkTX-c.lastNetworkTX) / seconds
			}
		}
	}
	c.lastProcessCPU, c.lastHostCPUIdle, c.lastHostCPUTotal = processTicks, hostIdle, hostTotal
	c.lastNetworkRX, c.lastNetworkTX, c.lastResourceAt = networkRX, networkTX, now
	c.resourceMu.Unlock()
}

func percentOf(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(used) / float64(total) * 100
}

func hostCPUPercent(previousIdle, previousTotal, idle, total uint64) float64 {
	if total <= previousTotal || idle < previousIdle {
		return 0
	}
	totalDelta, idleDelta := total-previousTotal, idle-previousIdle
	if totalDelta == 0 || idleDelta > totalDelta {
		return 0
	}
	return float64(totalDelta-idleDelta) / float64(totalDelta) * 100
}

func linuxCPUTicks() (idle, total uint64) {
	file, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		return 0, 0
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0
	}
	values := make([]uint64, 0, len(fields)-1)
	for _, field := range fields[1:] {
		value, _ := strconv.ParseUint(field, 10, 64)
		values = append(values, value)
		total += value
	}
	idle = values[3]
	if len(values) > 4 {
		idle += values[4]
	}
	return idle, total
}

func linuxLoadAverage() (one, five, fifteen float64) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0, 0
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return 0, 0, 0
	}
	one, _ = strconv.ParseFloat(fields[0], 64)
	five, _ = strconv.ParseFloat(fields[1], 64)
	fifteen, _ = strconv.ParseFloat(fields[2], 64)
	return
}

func linuxUptimeSeconds() int64 {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0
	}
	seconds, _ := strconv.ParseFloat(fields[0], 64)
	return int64(seconds)
}

func processRSSBytes() uint64 {
	data, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return 0
	}
	pages, _ := strconv.ParseUint(fields[1], 10, 64)
	return pages * uint64(os.Getpagesize())
}

func diskUsage(path string) (used, total uint64) {
	var stats syscall.Statfs_t
	if err := syscall.Statfs(path, &stats); err != nil {
		return 0, 0
	}
	total = stats.Blocks * uint64(stats.Bsize)
	available := stats.Bavail * uint64(stats.Bsize)
	if total >= available {
		used = total - available
	}
	return used, total
}

func linuxNetworkBytes() (received, transmitted uint64) {
	file, err := os.Open("/proc/net/dev")
	if err != nil {
		return 0, 0
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		separator := strings.IndexByte(line, ':')
		if separator < 0 {
			continue
		}
		name := strings.TrimSpace(line[:separator])
		if name == "lo" {
			continue
		}
		fields := strings.Fields(line[separator+1:])
		if len(fields) < 9 {
			continue
		}
		rx, _ := strconv.ParseUint(fields[0], 10, 64)
		tx, _ := strconv.ParseUint(fields[8], 10, 64)
		received, transmitted = received+rx, transmitted+tx
	}
	return received, transmitted
}

func linuxMemory() (used, total uint64) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		return mem.Sys, 0
	}
	defer file.Close()
	var available uint64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		value, _ := strconv.ParseUint(fields[1], 10, 64)
		switch strings.TrimSuffix(fields[0], ":") {
		case "MemTotal":
			total = value * 1024
		case "MemAvailable":
			available = value * 1024
		}
	}
	if total >= available {
		used = total - available
	}
	return
}

func processCPUTicks() uint64 {
	data, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) <= 14 {
		return 0
	}
	user, _ := strconv.ParseUint(fields[13], 10, 64)
	system, _ := strconv.ParseUint(fields[14], 10, 64)
	return user + system
}

func (c *OpsCollector) writeSystemLog(level, component, message string) error {
	return c.db.Create(&model.OpsSystemLog{Level: level, Component: component, Message: trimOpsText(message, 2048), CreatedAt: time.Now().UTC()}).Error
}

func trimOpsText(value string, maxLength int) string {
	value = strings.TrimSpace(value)
	if len(value) > maxLength {
		return value[:maxLength]
	}
	return value
}

func (c *OpsCollector) String() string { return fmt.Sprintf("%s", opsCollectorJobName) }
