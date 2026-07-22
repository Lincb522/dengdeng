package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"dengdeng/internal/config"
	"dengdeng/internal/model"

	"gorm.io/gorm"
)

const systemSettingsKey = "system.settings.v1"

// LegalDocument is deliberately plain text/Markdown. Rendering it as text in
// the SPA prevents administrator-authored policy content from becoming an XSS
// surface while still making the complete document available at a stable URL.
type LegalDocument struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	ContentMD string `json:"content_md"`
}

type LoginAgreementSettings struct {
	Enabled   bool            `json:"enabled"`
	Mode      string          `json:"mode"` // modal | checkbox
	UpdatedAt string          `json:"updated_at"`
	Documents []LegalDocument `json:"documents"`
}

func (a LoginAgreementSettings) Revision() string {
	payload, _ := json.Marshal(struct {
		UpdatedAt string          `json:"updated_at"`
		Documents []LegalDocument `json:"documents"`
	}{UpdatedAt: a.UpdatedAt, Documents: a.Documents})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])[:16]
}

// SystemSettings holds safe, runtime-editable product settings. Deployment
// secrets (database, SMTP credentials, JWT keys) deliberately remain in the
// environment and are never represented here.
type SystemSettings struct {
	SiteName      string `json:"site_name"`
	SiteSubtitle  string `json:"site_subtitle"`
	AllowRegister bool   `json:"allow_register"`
	// RegistrationEmailSuffixes is an optional tenant-style allow-list. An
	// empty list permits all valid email domains; a non-empty list accepts the
	// listed domains and their subdomains only.
	RegistrationEmailSuffixes []string               `json:"registration_email_suffixes"`
	InitBalanceMicro          int64                  `json:"init_balance_micro"`
	LoginAgreement            LoginAgreementSettings `json:"login_agreement"`
	TrustedProxies            []string               `json:"trusted_proxies"`
	ForwardedClientIPHeaders  []string               `json:"forwarded_client_ip_headers"`
}

type AdminSystemSettings struct {
	SystemSettings
	SitePublicURL  string `json:"site_public_url"`
	SMTPConfigured bool   `json:"smtp_configured"`
	SMTPFromName   string `json:"smtp_from_name"`
	SMTPFrom       string `json:"smtp_from"`
}

type SystemSettingsService struct {
	db  *gorm.DB
	cfg *config.Config
}

func NewSystemSettingsService(db *gorm.DB, cfg *config.Config) *SystemSettingsService {
	return &SystemSettingsService{db: db, cfg: cfg}
}

func defaultLegalDocuments() []LegalDocument {
	return []LegalDocument{
		{
			ID: "terms", Title: "服务条款", ContentMD: `# DengDeng AI 服务条款

欢迎使用 DengDeng AI（蹬蹬ai）。使用本服务即表示你同意遵守本条款、使用政策、隐私政策、服务特定条款与免责声明。

## 服务范围

本服务提供统一的 API 接入、用量统计、密钥管理与账户余额能力。不同模型、上游服务与功能的可用性、限额和价格可能随时调整。

## 账户与密钥

你应妥善保管账户、密码和 API 密钥，并对使用它们发起的请求负责。不得出租、转售、共享账户或以任何方式规避限额、风控、计费或访问控制。

## 费用与退款

调用费用以控制台记录为准。已消耗的服务通常不能撤销；如遇重复扣费或系统异常，请及时联系支持人员核查。

## 变更

我们可在必要时调整服务、计费规则或本条款。继续使用更新后的服务，即表示你接受更新后的条款。`,
		},
		{
			ID: "privacy", Title: "隐私政策", ContentMD: `# DengDeng AI 隐私政策

我们仅收集和处理维持服务所必需的信息，例如邮箱、账户资料、密钥元数据、调用记录、设备与安全日志。

## 数据使用

这些信息用于身份验证、计费、故障排查、防滥用和改进服务。除法律要求、保护用户与平台安全，或经你明确同意外，我们不会出售你的个人信息。

## 数据安全

上游凭据等敏感字段会以加密方式保存。你仍应避免在提示词、日志或工单中提交不必要的敏感信息。

## 数据保留

我们会在实现服务目的和履行法律义务所需的期限内保留数据。你可以联系支持人员咨询与账户相关的数据请求。`,
		},
		{
			ID: "usage-policy", Title: "使用政策", ContentMD: `# DengDeng AI 使用政策

你不得使用本服务从事违法、侵权、欺诈、骚扰、绕过安全机制、批量滥用或侵犯他人隐私的活动。

不得利用本服务规避任何模型提供商、平台或地区的访问限制、账户限制、内容政策或商业条款。不得将服务用于传播恶意软件、窃取凭据、攻击系统或生成违法内容。

如发现异常调用、滥用或安全风险，我们可采取限流、暂停密钥、冻结账户或配合依法处理等措施。`,
		},
		{
			ID: "service-specific-terms", Title: "服务特定条款", ContentMD: `# 服务特定条款

## 上游模型

本服务可能连接第三方模型与平台。模型名称、输出质量、上下文长度、缓存行为、图像生成能力和可用性均由对应上游决定，并可能发生变化。

## API 兼容性

我们尽力保持兼容，但不保证所有第三方客户端、SDK、参数或响应字段始终可用。请在生产接入前完成测试，并为调用失败、重试与限流预留处理逻辑。

## 生图与多媒体

生图等请求可能按照张数、分辨率、质量或上游实际消耗计费。请在控制台确认模型定价和对应分组后再进行批量调用。`,
		},
		{
			ID: "disclaimer", Title: "免责声明", ContentMD: `# 免责声明

本服务按“现状”和“可用”基础提供。除非法律另有强制规定，我们不保证服务、上游模型、网络链路或第三方平台持续、无错误、无中断或完全符合你的特定用途。

第三方模型账户、订阅、地区限制、服务条款和风控政策由相应提供商独立制定。因上游策略变化、账户限制、网络问题、错误输出、业务决策或使用本服务造成的间接损失、数据损失、利润损失或账户风险，应由使用者自行评估并承担。

本服务不构成法律、医疗、金融、投资或其他专业意见。涉及高风险用途时，你应取得合格专业人士的独立意见并建立人工审核与风险控制。`,
		},
	}
}

func (s *SystemSettingsService) defaults() SystemSettings {
	name := "DengDeng AI · 蹬蹬ai"
	allowRegister := true
	initBalance := int64(0)
	trustedProxies := []string{}
	forwardedHeaders := []string{"X-Forwarded-For", "X-Real-IP"}
	if s.cfg != nil {
		if strings.TrimSpace(s.cfg.Site.Name) != "" {
			name = strings.TrimSpace(s.cfg.Site.Name)
		}
		allowRegister = s.cfg.Site.AllowRegister
		initBalance = s.cfg.Site.InitBalanceMicro
		trustedProxies = append([]string(nil), s.cfg.Server.TrustedProxies...)
		if len(s.cfg.Server.ForwardedClientIPHeaders) > 0 {
			forwardedHeaders = append([]string(nil), s.cfg.Server.ForwardedClientIPHeaders...)
		}
	}
	return SystemSettings{
		SiteName:                 name,
		SiteSubtitle:             "统一管理模型接入与用量",
		AllowRegister:            allowRegister,
		InitBalanceMicro:         initBalance,
		TrustedProxies:           trustedProxies,
		ForwardedClientIPHeaders: forwardedHeaders,
		LoginAgreement: LoginAgreementSettings{
			Enabled: true, Mode: "modal", UpdatedAt: "2026-07-16", Documents: defaultLegalDocuments(),
		},
	}
}

func normalizeDocumentID(raw string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == ' ':
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
				b.WriteByte('-')
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func (s *SystemSettingsService) normalize(next SystemSettings) (SystemSettings, error) {
	next.SiteName = strings.TrimSpace(next.SiteName)
	next.SiteSubtitle = strings.TrimSpace(next.SiteSubtitle)
	if next.SiteName == "" || len([]rune(next.SiteName)) > 120 {
		return SystemSettings{}, errors.New("site name must be between 1 and 120 characters")
	}
	if len([]rune(next.SiteSubtitle)) > 240 {
		return SystemSettings{}, errors.New("site subtitle must be at most 240 characters")
	}
	if next.InitBalanceMicro < 0 || next.InitBalanceMicro > 1_000_000_000_000 {
		return SystemSettings{}, errors.New("initial balance is out of range")
	}
	seenSuffixes := make(map[string]struct{}, len(next.RegistrationEmailSuffixes))
	suffixes := make([]string, 0, len(next.RegistrationEmailSuffixes))
	for _, raw := range next.RegistrationEmailSuffixes {
		suffix := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(raw)), "@")
		if suffix == "" {
			continue
		}
		if len(suffix) > 253 || strings.ContainsAny(suffix, "@ /\\\t\r\n") || !strings.Contains(suffix, ".") {
			return SystemSettings{}, errors.New("registration email suffixes must be domain names")
		}
		if _, duplicate := seenSuffixes[suffix]; duplicate {
			continue
		}
		seenSuffixes[suffix] = struct{}{}
		suffixes = append(suffixes, suffix)
	}
	if len(suffixes) > 64 {
		return SystemSettings{}, errors.New("at most 64 registration email suffixes are allowed")
	}
	next.RegistrationEmailSuffixes = suffixes

	proxies := make([]string, 0, len(next.TrustedProxies))
	proxySeen := make(map[string]struct{}, len(next.TrustedProxies))
	for _, raw := range next.TrustedProxies {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if len(value) > 128 {
			return SystemSettings{}, errors.New("trusted proxy is too long")
		}
		if ip := net.ParseIP(value); ip == nil {
			if _, _, err := net.ParseCIDR(value); err != nil {
				return SystemSettings{}, errors.New("trusted proxies must be IP addresses or CIDR ranges")
			}
		}
		if _, ok := proxySeen[value]; !ok {
			proxySeen[value] = struct{}{}
			proxies = append(proxies, value)
		}
	}
	if len(proxies) > 64 {
		return SystemSettings{}, errors.New("at most 64 trusted proxies are allowed")
	}
	next.TrustedProxies = proxies

	headers := make([]string, 0, len(next.ForwardedClientIPHeaders))
	headerSeen := make(map[string]struct{}, len(next.ForwardedClientIPHeaders))
	for _, raw := range next.ForwardedClientIPHeaders {
		name := http.CanonicalHeaderKey(strings.TrimSpace(raw))
		if name == "" || strings.ContainsAny(name, " :\t\r\n") {
			return SystemSettings{}, errors.New("forwarded client IP header is invalid")
		}
		if _, ok := headerSeen[name]; !ok {
			headerSeen[name] = struct{}{}
			headers = append(headers, name)
		}
	}
	if len(headers) == 0 || len(headers) > 8 {
		return SystemSettings{}, errors.New("between 1 and 8 forwarded client IP headers are required")
	}
	next.ForwardedClientIPHeaders = headers

	a := &next.LoginAgreement
	if a.Mode != "checkbox" {
		a.Mode = "modal"
	}
	a.UpdatedAt = strings.TrimSpace(a.UpdatedAt)
	if a.UpdatedAt == "" {
		a.UpdatedAt = "2026-07-16"
	}
	if len(a.UpdatedAt) > 32 {
		return SystemSettings{}, errors.New("agreement update date is too long")
	}
	seen := make(map[string]struct{}, len(a.Documents))
	docs := make([]LegalDocument, 0, len(a.Documents))
	for i, doc := range a.Documents {
		doc.ID = normalizeDocumentID(doc.ID)
		if doc.ID == "" {
			doc.ID = fmt.Sprintf("document-%d", i+1)
		}
		doc.Title = strings.TrimSpace(doc.Title)
		doc.ContentMD = strings.TrimSpace(doc.ContentMD)
		if doc.Title == "" || len([]rune(doc.Title)) > 64 {
			return SystemSettings{}, errors.New("each agreement document needs a title of at most 64 characters")
		}
		if len([]rune(doc.ContentMD)) > 16_000 {
			return SystemSettings{}, errors.New("agreement document is too long")
		}
		if _, duplicate := seen[doc.ID]; duplicate {
			return SystemSettings{}, errors.New("agreement document IDs must be unique")
		}
		seen[doc.ID] = struct{}{}
		docs = append(docs, doc)
	}
	if a.Enabled && len(docs) == 0 {
		return SystemSettings{}, errors.New("enable at least one agreement document")
	}
	a.Documents = docs
	return next, nil
}

func (s SystemSettings) AllowsRegistrationEmail(email string) bool {
	if len(s.RegistrationEmailSuffixes) == 0 {
		return true
	}
	parts := strings.Split(strings.ToLower(strings.TrimSpace(email)), "@")
	if len(parts) != 2 || parts[1] == "" {
		return false
	}
	domain := parts[1]
	for _, suffix := range s.RegistrationEmailSuffixes {
		if domain == suffix || strings.HasSuffix(domain, "."+suffix) {
			return true
		}
	}
	return false
}

func (s *SystemSettingsService) Get() (SystemSettings, error) {
	defaults := s.defaults()
	var record model.Setting
	err := s.db.First(&record, "key = ?", systemSettingsKey).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return defaults, nil
	}
	if err != nil {
		return SystemSettings{}, err
	}
	next := defaults
	if err := json.Unmarshal([]byte(record.Value), &next); err != nil {
		return SystemSettings{}, fmt.Errorf("decode system settings: %w", err)
	}
	return s.normalize(next)
}

func (s *SystemSettingsService) Update(next SystemSettings) (SystemSettings, error) {
	next, err := s.normalize(next)
	if err != nil {
		return SystemSettings{}, err
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return SystemSettings{}, err
	}
	if len(raw) > 96_000 {
		return SystemSettings{}, errors.New("system settings are too large")
	}
	record := model.Setting{Key: systemSettingsKey, Value: string(raw)}
	if err := s.db.Save(&record).Error; err != nil {
		return SystemSettings{}, err
	}
	return next, nil
}

func (s *SystemSettingsService) AdminView() (AdminSystemSettings, error) {
	settings, err := s.Get()
	if err != nil {
		return AdminSystemSettings{}, err
	}
	view := AdminSystemSettings{SystemSettings: settings}
	if s.cfg != nil {
		view.SitePublicURL = s.cfg.Site.PublicURL
		view.SMTPConfigured = s.cfg.SMTP.Host != "" && s.cfg.SMTP.Port > 0 && s.cfg.SMTP.User != "" && s.cfg.SMTP.Pass != ""
		view.SMTPFromName = s.cfg.SMTP.FromName
		view.SMTPFrom = s.cfg.SMTP.From
	}
	return view, nil
}
