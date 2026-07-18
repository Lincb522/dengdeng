// Package store owns database bootstrap: connection, schema migration and
// initial seeding (admin account, default model prices).
package store

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"dengdeng/internal/config"
	"dengdeng/internal/model"
	"dengdeng/internal/util"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

func Open(cfg *config.Config) (*gorm.DB, error) {
	// Make automatic GORM timestamps match the UTC API/query contract. SQLite
	// stores time values as text, so mixing host-local offsets with UTC filters
	// otherwise creates invisible gaps in dashboards and exports.
	gcfg := &gorm.Config{Logger: logger.Default.LogMode(logger.Warn), NowFunc: func() time.Time { return time.Now().UTC() }}

	var (
		db  *gorm.DB
		err error
	)
	switch cfg.Database.Driver {
	case "postgres":
		db, err = gorm.Open(postgres.Open(cfg.PostgresDSN()), gcfg)
	case "", "sqlite":
		if dir := filepath.Dir(cfg.Database.Path); dir != "." {
			if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
				return nil, mkErr
			}
		}
		db, err = gorm.Open(sqlite.Open(cfg.Database.Path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"), gcfg)
	default:
		return nil, fmt.Errorf("unsupported database driver: %s", cfg.Database.Driver)
	}
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if err := db.AutoMigrate(
		&model.User{}, &model.Group{}, &model.UserGroupRate{}, &model.APIKey{}, &model.APIKeyGroup{}, &model.ReferralCode{}, &model.ReferralBinding{}, &model.ReferralCommission{}, &model.Proxy{}, &model.UpstreamAccount{}, &model.AccountQuotaSnapshot{}, &model.CodexQuotaSnapshot{},
		&model.AccountProbe{}, &model.AlertRule{}, &model.AlertEvent{},
		&model.ModelPrice{}, &model.ModelConfig{}, &model.UsageLog{}, &model.RedeemCode{}, &model.EmailVerification{}, &model.Setting{}, &model.AuditLog{},
		&model.PaymentConfig{}, &model.PaymentProviderInstance{}, &model.PaymentOrder{}, &model.PaymentAuditLog{}, &model.BackupRecord{},
	); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	// Existing databases receive these columns through ALTER TABLE and can have
	// NULLs in historical rows. Backfill to zero so old usage entries keep the
	// same JSON shape as new entries and never hit a nullable scan edge case.
	if err := db.Model(&model.UsageLog{}).
		Where("cache_write5m_tokens IS NULL OR cache_write1h_tokens IS NULL OR image_count IS NULL").
		Updates(map[string]any{
			"cache_write5m_tokens": gorm.Expr("COALESCE(cache_write5m_tokens, 0)"),
			"cache_write1h_tokens": gorm.Expr("COALESCE(cache_write1h_tokens, 0)"),
			"image_count":          gorm.Expr("COALESCE(image_count, 0)"),
		}).Error; err != nil {
		return nil, fmt.Errorf("backfill cache TTL usage: %w", err)
	}
	// SQLite adds the non-null default only for newly written rows on older
	// databases. Make existing API keys explicit too, so every serialized key
	// has a stable setting and gateway behaviour is deterministic.
	if err := db.Model(&model.APIKey{}).
		Where("reasoning_effort IS NULL OR reasoning_effort = ''").
		Update("reasoning_effort", "auto").Error; err != nil {
		return nil, fmt.Errorf("backfill key reasoning effort: %w", err)
	}
	if err := db.Model(&model.APIKey{}).
		Where("reasoning_effort IN ?", []string{"fast", "minimal"}).
		Update("reasoning_effort", "low").Error; err != nil {
		return nil, fmt.Errorf("migrate legacy reasoning effort: %w", err)
	}
	// Every pre-multi-group key starts with its existing group selected. This is
	// idempotent, so it also repairs a partially completed deployment safely.
	var legacyKeyGroups []model.APIKeyGroup
	if err := db.Model(&model.APIKey{}).
		Select("id AS api_key_id, group_id AS group_id").
		Where("group_id > 0").
		Scan(&legacyKeyGroups).Error; err != nil {
		return nil, fmt.Errorf("load legacy key groups: %w", err)
	}
	if len(legacyKeyGroups) > 0 {
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(legacyKeyGroups, 500).Error; err != nil {
			return nil, fmt.Errorf("backfill key groups: %w", err)
		}
	}
	if err := normalizeSQLiteUsageTimes(db, cfg); err != nil {
		return nil, fmt.Errorf("normalize usage timestamps: %w", err)
	}
	return db, nil
}

const usageUTCMigrationKey = "migration.usage_utc_v1"

// normalizeSQLiteUsageTimes converts legacy local-offset UsageLog timestamps
// once. The GORM scanner understands the stored offset and UpdateColumn then
// writes its UTC equivalent with the same driver formatting used by all new
// records. This preserves the actual instant and lets indexed lexical SQLite
// comparisons line up with monitoring's UTC range bounds.
func normalizeSQLiteUsageTimes(db *gorm.DB, cfg *config.Config) error {
	if cfg == nil || (cfg.Database.Driver != "" && cfg.Database.Driver != "sqlite") {
		return nil
	}
	var marker model.Setting
	if err := db.Where("key = ?", usageUTCMigrationKey).First(&marker).Error; err == nil {
		return nil
	} else if err != nil && err != gorm.ErrRecordNotFound {
		return err
	}
	const pageSize = 500
	var lastID int64
	for {
		var rows []model.UsageLog
		if err := db.Select("id", "created_at").Where("id > ?", lastID).Order("id ASC").Limit(pageSize).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			if err := db.Model(&model.UsageLog{}).Where("id = ?", row.ID).UpdateColumn("created_at", row.CreatedAt.UTC()).Error; err != nil {
				return err
			}
			lastID = row.ID
		}
	}
	return db.Create(&model.Setting{Key: usageUTCMigrationKey, Value: time.Now().UTC().Format(time.RFC3339)}).Error
}

// Seed creates the admin user on first boot and installs default pricing.
func Seed(db *gorm.DB, cfg *config.Config) error {
	var adminCount int64
	if err := db.Model(&model.User{}).Where("role = ?", model.RoleAdmin).Count(&adminCount).Error; err != nil {
		return err
	}
	if adminCount == 0 {
		password := cfg.Admin.Password
		generated := false
		if password == "" {
			password = util.RandomToken(12)
			generated = true
		}
		hash, err := util.HashPassword(password)
		if err != nil {
			return err
		}
		admin := &model.User{Email: cfg.Admin.Email, PasswordHash: hash, Role: model.RoleAdmin, Status: model.StatusActive, RateMultiplier: 1}
		if err := db.Create(admin).Error; err != nil {
			return err
		}
		if generated {
			log.Printf("[seed] admin account created: %s  initial password: %s  (change it after first login)", cfg.Admin.Email, password)
		} else {
			log.Printf("[seed] admin account created: %s", cfg.Admin.Email)
		}
	}

	// These official list prices are a bootstrap catalogue, not an overwrite of
	// operator edits. New rows are added on upgrade; existing rules stay under
	// administrator control.
	prices := []model.ModelPrice{
		{Match: "gpt-5.6", Platform: model.PlatformOpenAI, InputPrice: 5, OutputPrice: 30, CacheReadPrice: 0.5, CacheWritePrice: 6.25},
		{Match: "gpt-5.6-sol", Platform: model.PlatformOpenAI, InputPrice: 5, OutputPrice: 30, CacheReadPrice: 0.5, CacheWritePrice: 6.25},
		{Match: "gpt-5.6-terra", Platform: model.PlatformOpenAI, InputPrice: 2.5, OutputPrice: 15, CacheReadPrice: 0.25, CacheWritePrice: 3.125},
		{Match: "gpt-5.6-luna", Platform: model.PlatformOpenAI, InputPrice: 1, OutputPrice: 6, CacheReadPrice: 0.1, CacheWritePrice: 1.25},
		{Match: "gpt-5.5", Platform: model.PlatformOpenAI, InputPrice: 5, OutputPrice: 30, CacheReadPrice: 0.5},
		{Match: "gpt-5.5-pro", Platform: model.PlatformOpenAI, InputPrice: 30, OutputPrice: 180},
		{Match: "gpt-5.4", Platform: model.PlatformOpenAI, InputPrice: 2.5, OutputPrice: 15, CacheReadPrice: 0.25},
		{Match: "gpt-5.4-mini", Platform: model.PlatformOpenAI, InputPrice: 0.75, OutputPrice: 4.5, CacheReadPrice: 0.075},
		{Match: "gpt-5.4-nano", Platform: model.PlatformOpenAI, InputPrice: 0.2, OutputPrice: 1.25, CacheReadPrice: 0.02},
		{Match: "gpt-image-2", Platform: model.PlatformOpenAI, InputPrice: 5, CacheReadPrice: 1.25, ImageInputPrice: 8, ImageCacheReadPrice: 2, ImageOutputPrice: 30},
		{Match: "gpt-image-1.5", Platform: model.PlatformOpenAI, InputPrice: 5, OutputPrice: 10, CacheReadPrice: 1.25, ImageInputPrice: 8, ImageCacheReadPrice: 2, ImageOutputPrice: 32},
		// Anthropic first-party pricing, USD per MTok, checked July 2026.
		// Model-specific rules intentionally beat the legacy family wildcards below.
		{Match: "claude-fable-5", Platform: model.PlatformAnthropic, InputPrice: 10, OutputPrice: 50, CacheReadPrice: 1, CacheWritePrice: 12.5},
		{Match: "claude-mythos-5", Platform: model.PlatformAnthropic, InputPrice: 10, OutputPrice: 50, CacheReadPrice: 1, CacheWritePrice: 12.5},
		{Match: "claude-mythos-preview", Platform: model.PlatformAnthropic, InputPrice: 10, OutputPrice: 50, CacheReadPrice: 1, CacheWritePrice: 12.5},
		{Match: "claude-opus-4-8", Platform: model.PlatformAnthropic, InputPrice: 5, OutputPrice: 25, CacheReadPrice: 0.5, CacheWritePrice: 6.25},
		{Match: "claude-opus-4-7", Platform: model.PlatformAnthropic, InputPrice: 5, OutputPrice: 25, CacheReadPrice: 0.5, CacheWritePrice: 6.25},
		{Match: "claude-opus-4-6", Platform: model.PlatformAnthropic, InputPrice: 5, OutputPrice: 25, CacheReadPrice: 0.5, CacheWritePrice: 6.25},
		{Match: "claude-opus-4-5-20251101", Platform: model.PlatformAnthropic, InputPrice: 5, OutputPrice: 25, CacheReadPrice: 0.5, CacheWritePrice: 6.25},
		{Match: "claude-sonnet-5", Platform: model.PlatformAnthropic, InputPrice: 2, OutputPrice: 10, CacheReadPrice: 0.2, CacheWritePrice: 2.5},
		{Match: "claude-sonnet-4-6", Platform: model.PlatformAnthropic, InputPrice: 3, OutputPrice: 15, CacheReadPrice: 0.3, CacheWritePrice: 3.75},
		{Match: "claude-sonnet-4-5-20250929", Platform: model.PlatformAnthropic, InputPrice: 3, OutputPrice: 15, CacheReadPrice: 0.3, CacheWritePrice: 3.75},
		{Match: "claude-haiku-4-5-20251001", Platform: model.PlatformAnthropic, InputPrice: 1, OutputPrice: 5, CacheReadPrice: 0.1, CacheWritePrice: 1.25},
		{Match: "claude-opus-*", Platform: model.PlatformAnthropic, InputPrice: 5, OutputPrice: 25, CacheReadPrice: 0.5, CacheWritePrice: 6.25},
		{Match: "claude-sonnet-*", Platform: model.PlatformAnthropic, InputPrice: 3, OutputPrice: 15, CacheReadPrice: 0.3, CacheWritePrice: 3.75},
		{Match: "claude-haiku-*", Platform: model.PlatformAnthropic, InputPrice: 0.8, OutputPrice: 4, CacheReadPrice: 0.08, CacheWritePrice: 1},
		{Match: "gemini-2.5-pro", Platform: model.PlatformGemini, InputPrice: 1.25, OutputPrice: 10, CacheReadPrice: 0.125},
		// Gemini reports generated image tokens as candidate/output tokens, so
		// their image models use OutputPrice directly instead of OpenAI's
		// separately reported image-token fields.
		{Match: "gemini-2.5-flash-image", Platform: model.PlatformGemini, InputPrice: 0.3, OutputPrice: 30},
		{Match: "gemini-3.1-flash-image", Platform: model.PlatformGemini, InputPrice: 0.5, OutputPrice: 60},
		{Match: "gemini-3.1-flash-lite-image", Platform: model.PlatformGemini, InputPrice: 0.25, OutputPrice: 30},
		{Match: "gemini-3-pro-image", Platform: model.PlatformGemini, InputPrice: 2, OutputPrice: 120},
		// xAI / Grok. Model-specific rows beat the grok-* family wildcard.
		{Match: "grok-4.5", Platform: model.PlatformGrok, InputPrice: 3, OutputPrice: 15, CacheReadPrice: 0.75},
		{Match: "grok-4.3", Platform: model.PlatformGrok, InputPrice: 3, OutputPrice: 15, CacheReadPrice: 0.75},
		{Match: "grok-build-0.1", Platform: model.PlatformGrok, InputPrice: 1, OutputPrice: 5, CacheReadPrice: 0.25},
		{Match: "grok-composer-2.5-fast", Platform: model.PlatformGrok, InputPrice: 1, OutputPrice: 5, CacheReadPrice: 0.25},
		{Match: "grok-imagine-image", Platform: model.PlatformGrok, ImageOutputPrice: 40},
		{Match: "grok-imagine*", Platform: model.PlatformGrok, ImageOutputPrice: 40},
		{Match: "grok-*", Platform: model.PlatformGrok, InputPrice: 3, OutputPrice: 15, CacheReadPrice: 0.75},
		// Unknown-model fallback. A relay meant for external operation must not
		// silently bill any un-catalogued model at zero: this catch-all is the
		// lowest-priority match (a bare "*" prefix has length 0, so every named
		// or family rule above still wins). Operators can retune or delete it in
		// the Prices console.
		{Match: "*", InputPrice: 1, OutputPrice: 3, CacheReadPrice: 0.1, CacheWritePrice: 1.25},
	}
	for _, price := range prices {
		var existing model.ModelPrice
		if err := db.Where("match = ?", price.Match).First(&existing).Error; err == gorm.ErrRecordNotFound {
			if err := db.Create(&price).Error; err != nil {
				return err
			}
		}
	}
	if err := seedDefaultModelConfigs(db); err != nil {
		return err
	}
	return nil
}

// defaultModelConfigs is the public catalogue shipped with DengDeng. Token
// limits follow the providers' synchronous APIs. A zero max-output value is
// intentional when the provider publishes no fixed text-token ceiling (for
// example, pure image output or current xAI models).
func defaultModelConfigs() []model.ModelConfig {
	return []model.ModelConfig{
		{Name: "gpt-5.6", Platform: model.PlatformOpenAI, Kind: "chat", UpstreamModel: "gpt-5.6-sol", ContextWindow: 1_050_000, MaxOutputTokens: 128_000, Description: "OpenAI 默认旗舰推理模型"},
		{Name: "gpt-5.6-sol", Platform: model.PlatformOpenAI, Kind: "chat", ContextWindow: 1_050_000, MaxOutputTokens: 128_000, Description: "OpenAI 旗舰推理与编码模型"},
		{Name: "gpt-5.6-terra", Platform: model.PlatformOpenAI, Kind: "chat", ContextWindow: 1_050_000, MaxOutputTokens: 128_000, Description: "OpenAI 均衡型模型"},
		{Name: "gpt-5.6-luna", Platform: model.PlatformOpenAI, Kind: "chat", ContextWindow: 1_050_000, MaxOutputTokens: 128_000, Description: "OpenAI 高吞吐低成本模型"},
		{Name: "gpt-5.5", Platform: model.PlatformOpenAI, Kind: "chat", ContextWindow: 1_050_000, MaxOutputTokens: 128_000, Description: "OpenAI 当前专业推理模型"},
		{Name: "gpt-5.5-pro", Platform: model.PlatformOpenAI, Kind: "chat", ContextWindow: 1_050_000, MaxOutputTokens: 128_000, Description: "OpenAI 高精度专业模型"},
		{Name: "gpt-image-2", Platform: model.PlatformOpenAI, Kind: "image", Description: "OpenAI 最新图像生成与编辑模型"},
		{Name: "claude-fable-5", Platform: model.PlatformAnthropic, Kind: "chat", ContextWindow: 1_000_000, MaxOutputTokens: 128_000, Description: "Claude 最新旗舰智能体模型"},
		{Name: "claude-opus-4-8", Platform: model.PlatformAnthropic, Kind: "chat", ContextWindow: 1_000_000, MaxOutputTokens: 128_000, Description: "Claude Opus 4.8，高级推理与代码"},
		{Name: "claude-opus-4-7", Platform: model.PlatformAnthropic, Kind: "chat", ContextWindow: 1_000_000, MaxOutputTokens: 128_000, Description: "Claude Opus 4.7，高级推理与代码"},
		{Name: "claude-opus-4-6", Platform: model.PlatformAnthropic, Kind: "chat", ContextWindow: 1_000_000, MaxOutputTokens: 128_000, Description: "Claude Opus 4.6，高级推理与代码"},
		{Name: "claude-opus-4-5-20251101", Platform: model.PlatformAnthropic, Kind: "chat", ContextWindow: 200_000, MaxOutputTokens: 64_000, Description: "Claude Opus 4.5 固定版本"},
		{Name: "claude-sonnet-5", Platform: model.PlatformAnthropic, Kind: "chat", ContextWindow: 1_000_000, MaxOutputTokens: 128_000, Description: "Claude Sonnet 5，速度与能力均衡"},
		{Name: "claude-sonnet-4-6", Platform: model.PlatformAnthropic, Kind: "chat", ContextWindow: 1_000_000, MaxOutputTokens: 64_000, Description: "Claude Sonnet 4.6"},
		{Name: "claude-sonnet-4-5-20250929", Platform: model.PlatformAnthropic, Kind: "chat", ContextWindow: 200_000, MaxOutputTokens: 64_000, Description: "Claude Sonnet 4.5 固定版本"},
		{Name: "claude-haiku-4-5-20251001", Platform: model.PlatformAnthropic, Kind: "chat", ContextWindow: 200_000, MaxOutputTokens: 64_000, Description: "Claude Haiku 4.5，高吞吐低成本"},
		// These models require explicit Anthropic approval. Keeping them disabled
		// makes the catalogue complete without sending ordinary traffic to a
		// model the account cannot access.
		{Name: "claude-mythos-5", Platform: model.PlatformAnthropic, Kind: "chat", ContextWindow: 1_000_000, MaxOutputTokens: 128_000, Description: "Claude Mythos 5，受邀可用", Status: model.StatusDisabled},
		{Name: "claude-mythos-preview", Platform: model.PlatformAnthropic, Kind: "chat", ContextWindow: 1_000_000, MaxOutputTokens: 128_000, Description: "Claude Mythos Preview，受邀预览", Status: model.StatusDisabled},
		{Name: "gemini-2.5-flash-image", Platform: model.PlatformGemini, Kind: "image", ContextWindow: 65_536, MaxOutputTokens: 32_768, Description: "Gemini Nano Banana 图像模型"},
		{Name: "gemini-3-pro-image", Platform: model.PlatformGemini, Kind: "image", ContextWindow: 65_536, MaxOutputTokens: 32_768, Description: "Gemini 高质量图像模型"},
		{Name: "grok-4.5", Platform: model.PlatformGrok, Kind: "chat", ContextWindow: 500_000, Description: "xAI Grok 4.5 旗舰模型"},
		{Name: "grok-4.3", Platform: model.PlatformGrok, Kind: "chat", ContextWindow: 1_000_000, Description: "xAI Grok 4.3"},
		// grok-composer-2.5-fast is the public relay alias for grok-build-0.1.
		{Name: "grok-composer-2.5-fast", Platform: model.PlatformGrok, Kind: "chat", ContextWindow: 256_000, Description: "xAI Grok 高速编码模型"},
		{Name: "grok-imagine-image", Platform: model.PlatformGrok, Kind: "image", ContextWindow: 1_024, Description: "xAI Grok 图像生成模型"},
	}
}

// seedDefaultModelConfigs adds newly shipped models and fills only missing
// limits on existing rows. Operator-entered non-zero limits remain untouched.
func seedDefaultModelConfigs(db *gorm.DB) error {
	for _, cfg := range defaultModelConfigs() {
		var existing model.ModelConfig
		err := db.Where("name = ?", cfg.Name).First(&existing).Error
		if err == gorm.ErrRecordNotFound {
			if err := db.Create(&cfg).Error; err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		updates := map[string]any{}
		if existing.ContextWindow == 0 && cfg.ContextWindow > 0 {
			updates["context_window"] = cfg.ContextWindow
		}
		if existing.MaxOutputTokens == 0 && cfg.MaxOutputTokens > 0 {
			updates["max_output_tokens"] = cfg.MaxOutputTokens
		}
		if len(updates) > 0 {
			if err := db.Model(&existing).Updates(updates).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
