package service

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"dengdeng/internal/model"

	"gorm.io/gorm"
)

// IPGeoResolver enriches usage asynchronously. A geo provider outage must
// never add latency to an API response, so relay completion only enqueues a
// small immutable job and a single worker performs lookups with a timeout.
type IPGeoResolver struct {
	db     *gorm.DB
	client *http.Client
	jobs   chan ipGeoJob
	cache  sync.Map // map[ip]ipGeoResult
}

type ipGeoJob struct {
	usageLogID int64
	errorLogID int64
	ip         string
}

type ipGeoResult struct {
	Country  string
	Region   string
	City     string
	Location string
	ISP      string
}

func NewIPGeoResolver(db *gorm.DB) *IPGeoResolver {
	r := &IPGeoResolver{db: db, client: &http.Client{Timeout: 3 * time.Second}, jobs: make(chan ipGeoJob, 1024)}
	go r.run()
	return r
}

func (r *IPGeoResolver) Enrich(usageLogID, errorLogID int64, rawIP string) {
	if r == nil || r.db == nil || usageLogID <= 0 {
		return
	}
	ip := normalizeClientIP(rawIP)
	if ip == "" {
		return
	}
	select {
	case r.jobs <- ipGeoJob{usageLogID: usageLogID, errorLogID: errorLogID, ip: ip}:
	default:
		// The ledger already contains the IP. Dropping optional geo enrichment is
		// safer than blocking billing when a provider is slow.
	}
}

func (r *IPGeoResolver) run() {
	for job := range r.jobs {
		result := r.resolve(job.ip)
		updates := map[string]any{
			"client_ip": job.ip, "ip_country": result.Country, "ip_region": result.Region,
			"ip_city": result.City, "ip_location": result.Location, "ip_isp": result.ISP,
		}
		_ = r.db.Model(&model.UsageLog{}).Where("id = ?", job.usageLogID).Updates(updates).Error
		if job.errorLogID > 0 {
			_ = r.db.Model(&model.OpsErrorLog{}).Where("id = ?", job.errorLogID).
				Updates(map[string]any{"client_ip": job.ip, "ip_location": result.Location}).Error
		}
	}
}

func (r *IPGeoResolver) resolve(ip string) ipGeoResult {
	if cached, ok := r.cache.Load(ip); ok {
		return cached.(ipGeoResult)
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ipGeoResult{}
	}
	if parsed.IsLoopback() || parsed.IsPrivate() || parsed.IsLinkLocalUnicast() {
		result := ipGeoResult{Location: "局域网 / Local network"}
		r.cache.Store(ip, result)
		return result
	}
	// Reuse an already enriched ledger row before calling the public resolver.
	var previous model.UsageLog
	if err := r.db.Select("ip_country", "ip_region", "ip_city", "ip_location", "ip_isp").
		Where("client_ip = ? AND ip_location <> ''", ip).Order("id DESC").First(&previous).Error; err == nil {
		if containsHan(previous.IPLocation) {
			result := ipGeoResult{Country: previous.IPCountry, Region: previous.IPRegion, City: previous.IPCity, Location: previous.IPLocation, ISP: previous.IPISP}
			r.cache.Store(ip, result)
			return result
		}
	}

	req, err := http.NewRequest(http.MethodGet, "https://ipwho.is/"+ip+"?lang=zh-CN", nil)
	if err != nil {
		return ipGeoResult{}
	}
	req.Header.Set("Accept", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return ipGeoResult{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ipGeoResult{}
	}
	var payload struct {
		Success    bool   `json:"success"`
		Country    string `json:"country"`
		Region     string `json:"region"`
		City       string `json:"city"`
		Connection struct {
			ISP string `json:"isp"`
		} `json:"connection"`
	}
	if json.NewDecoder(resp.Body).Decode(&payload) != nil || !payload.Success {
		return ipGeoResult{}
	}
	parts := make([]string, 0, 3)
	for _, part := range []string{payload.Country, payload.Region, payload.City} {
		part = strings.TrimSpace(part)
		if part != "" && (len(parts) == 0 || parts[len(parts)-1] != part) {
			parts = append(parts, part)
		}
	}
	result := ipGeoResult{Country: payload.Country, Region: payload.Region, City: payload.City, Location: strings.Join(parts, " · "), ISP: payload.Connection.ISP}
	r.cache.Store(ip, result)
	return result
}

func containsHan(value string) bool {
	for _, current := range value {
		if unicode.Is(unicode.Han, current) {
			return true
		}
	}
	return false
}

func normalizeClientIP(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}
	if parsed := net.ParseIP(strings.Trim(raw, "[]")); parsed != nil {
		return parsed.String()
	}
	return ""
}
