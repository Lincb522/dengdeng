// Package store owns database bootstrap: connection, schema migration and
// initial seeding (admin account, default model prices).
package store

import (
	"errors"
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
			if mkErr := os.MkdirAll(dir, 0o700); mkErr != nil {
				return nil, mkErr
			}
		}
		db, err = gorm.Open(sqlite.Open(cfg.Database.Path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"), gcfg)
	default:
		return nil, fmt.Errorf("unsupported database driver: %s", cfg.Database.Driver)
	}
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if (cfg.Database.Driver == "" || cfg.Database.Driver == "sqlite") && cfg.Database.Path != "" {
		if chmodErr := os.Chmod(cfg.Database.Path, 0o600); chmodErr != nil {
			return nil, fmt.Errorf("protect database file: %w", chmodErr)
		}
	}

	if err := db.AutoMigrate(
		&model.User{}, &model.UserOAuthIdentity{}, &model.UserOAuthFlow{}, &model.UserProviderDefaultGrant{}, &model.UserPlatformQuota{}, &model.UserGroupSubscription{}, &model.Group{}, &model.UserGroupRate{}, &model.APIKey{}, &model.APIKeyGroup{}, &model.ReferralCode{}, &model.ReferralBinding{}, &model.ReferralCommission{}, &model.ReferralCashAccount{}, &model.ReferralPayoutAccount{}, &model.ReferralPayoutConfig{}, &model.ReferralPayout{}, &model.Proxy{}, &model.UpstreamAccount{}, &model.UpstreamAccountGroup{}, &model.AccountQuotaSnapshot{}, &model.CodexQuotaSnapshot{},
		&model.AccountProbe{}, &model.AlertRule{}, &model.AlertEvent{},
		&model.ModelPrice{}, &model.ModelConfig{}, &model.UsageLog{}, &model.RedeemCode{}, &model.EmailVerification{}, &model.Setting{}, &model.AuditLog{},
		&model.OpsSystemMetric{}, &model.OpsMetricAggregate{}, &model.OpsErrorLog{}, &model.OpsJobHeartbeat{}, &model.OpsAlertSilence{}, &model.OpsSystemLog{},
		&model.PaymentConfig{}, &model.PaymentProviderInstance{}, &model.PaymentOrder{}, &model.PaymentAuditLog{}, &model.PaymentLedgerEntry{}, &model.BackupRecord{},
		&model.ImageStorageConfig{}, &model.ImageTask{},
	); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	// Older SQLite schemas created an invalid FK from proxy_id=0 to proxies.id.
	// The zero value intentionally means "default egress", so remove that
	// legacy constraint before enforcing the remaining foreign keys.
	if err := dropLegacySQLiteProxyConstraint(db, cfg); err != nil {
		return nil, fmt.Errorf("drop legacy proxy constraint: %w", err)
	}
	// Existing databases receive these columns through ALTER TABLE and can have
	// NULLs in historical rows. Backfill to zero so old usage entries keep the
	// same JSON shape as new entries and never hit a nullable scan edge case.
	if err := db.Model(&model.UsageLog{}).
		Where("cache_write5m_tokens IS NULL OR cache_write1h_tokens IS NULL OR image_count IS NULL OR first_token_ms IS NULL").
		Updates(map[string]any{
			"cache_write5m_tokens": gorm.Expr("COALESCE(cache_write5m_tokens, 0)"),
			"cache_write1h_tokens": gorm.Expr("COALESCE(cache_write1h_tokens, 0)"),
			"image_count":          gorm.Expr("COALESCE(image_count, 0)"),
			"first_token_ms":       gorm.Expr("COALESCE(first_token_ms, 0)"),
		}).Error; err != nil {
		return nil, fmt.Errorf("backfill usage metrics: %w", err)
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
	// Reservations exist only while one gateway process is serving a request.
	// A clean start means no old request can still settle, so clear holds left
	// behind by a crash before accepting new traffic.
	if err := db.Model(&model.User{}).Where("balance_held_micro <> 0").Update("balance_held_micro", 0).Error; err != nil {
		return nil, fmt.Errorf("clear stale balance reservations: %w", err)
	}
	if err := db.Model(&model.APIKey{}).
		Where("quota_held_micro <> 0 OR daily_quota_held_micro <> 0").
		Updates(map[string]any{"quota_held_micro": 0, "daily_quota_held_micro": 0}).Error; err != nil {
		return nil, fmt.Errorf("clear stale key reservations: %w", err)
	}
	if err := backfillPaymentLedger(db); err != nil {
		return nil, fmt.Errorf("backfill payment ledger: %w", err)
	}
	if err := db.Model(&model.PaymentLedgerEntry{}).Where("category IS NULL OR category = ''").Updates(map[string]any{
		"category": gorm.Expr("CASE WHEN kind = ? THEN ? ELSE ? END", model.PaymentLedgerExpense, "refund", "recharge"),
	}).Error; err != nil {
		return nil, fmt.Errorf("backfill payment ledger categories: %w", err)
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
	// Every existing upstream credential starts with its legacy primary group as
	// a membership. The backfill is idempotent and keeps rollback builds usable.
	var legacyAccountGroups []model.UpstreamAccountGroup
	if err := db.Model(&model.UpstreamAccount{}).
		Select("id AS upstream_account_id, group_id AS group_id").
		Where("group_id > 0").
		Scan(&legacyAccountGroups).Error; err != nil {
		return nil, fmt.Errorf("load legacy account groups: %w", err)
	}
	if len(legacyAccountGroups) > 0 {
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(legacyAccountGroups, 500).Error; err != nil {
			return nil, fmt.Errorf("backfill account groups: %w", err)
		}
	}
	if err := normalizeSQLiteUsageTimes(db, cfg); err != nil {
		return nil, fmt.Errorf("normalize usage timestamps: %w", err)
	}
	if err := backfillOpsErrorLogs(db); err != nil {
		return nil, fmt.Errorf("backfill ops errors: %w", err)
	}
	return db, nil
}

func dropLegacySQLiteProxyConstraint(db *gorm.DB, cfg *config.Config) error {
	if cfg.Database.Driver != "" && cfg.Database.Driver != "sqlite" {
		return nil
	}
	if !db.Migrator().HasConstraint(&model.UpstreamAccount{}, "Proxy") {
		return nil
	}

	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	// SQLite's migrator removes a constraint by rebuilding the table. Existing
	// quota snapshot tables legitimately reference upstream_accounts, so the
	// rebuild must run on one connection with FK enforcement temporarily off.
	// Startup is single-threaded here; restoring the pool limit immediately
	// keeps normal request concurrency unchanged.
	previousMaxOpen := sqlDB.Stats().MaxOpenConnections
	sqlDB.SetMaxOpenConns(1)
	defer sqlDB.SetMaxOpenConns(previousMaxOpen)

	if err := db.Exec("PRAGMA foreign_keys = OFF").Error; err != nil {
		return fmt.Errorf("disable foreign keys: %w", err)
	}
	dropErr := db.Migrator().DropConstraint(&model.UpstreamAccount{}, "Proxy")
	enableErr := db.Exec("PRAGMA foreign_keys = ON").Error
	if dropErr != nil {
		return dropErr
	}
	if enableErr != nil {
		return fmt.Errorf("restore foreign keys: %w", enableErr)
	}

	var violations []struct {
		Table string
	}
	if err := db.Raw("PRAGMA foreign_key_check").Scan(&violations).Error; err != nil {
		return fmt.Errorf("check foreign keys: %w", err)
	}
	if len(violations) > 0 {
		return fmt.Errorf("foreign key check returned %d violation(s)", len(violations))
	}
	return nil
}

func backfillOpsErrorLogs(db *gorm.DB) error {
	const pageSize = 500
	var lastID int64
	platforms := map[int64]string{}
	var groups []model.Group
	if err := db.Select("id", "platform").Find(&groups).Error; err != nil {
		return err
	}
	for _, group := range groups {
		platforms[group.ID] = group.Platform
	}
	for {
		var rows []model.UsageLog
		err := db.Where("id > ? AND (status_code < 200 OR status_code >= 400) AND NOT EXISTS (SELECT 1 FROM ops_error_logs WHERE ops_error_logs.usage_log_id = usage_logs.id)", lastID).
			Order("id ASC").Limit(pageSize).Find(&rows).Error
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		items := make([]model.OpsErrorLog, 0, len(rows))
		for _, row := range rows {
			errorType, phase, source := "upstream_error", "upstream", "provider"
			if row.StatusCode == 429 {
				errorType, phase = "rate_limit", "routing"
			}
			if row.StatusCode == 401 || row.StatusCode == 403 {
				errorType, phase = "authentication", "authentication"
			}
			if row.StatusCode == 400 || row.StatusCode == 413 {
				errorType, phase, source = "invalid_request", "request", "client"
			}
			severity := "P2"
			if row.StatusCode >= 500 {
				severity = "P1"
			}
			items = append(items, model.OpsErrorLog{UsageLogID: row.ID, RequestID: row.RequestID, ClientRequestID: row.ClientRequestID, UserID: row.UserID, APIKeyID: row.APIKeyID, AccountID: row.AccountID, GroupID: row.GroupID, Platform: platforms[row.GroupID], Model: row.Model, RequestPath: row.RequestPath, ClientIP: row.ClientIP, IPLocation: row.IPLocation, UserAgent: row.UserAgent, StatusCode: row.StatusCode, ErrorPhase: phase, ErrorType: errorType, ErrorSource: source, Severity: severity, BusinessLimited: row.StatusCode == 429, Retryable: row.StatusCode == 429 || row.StatusCode >= 500, ErrorMessage: row.ErrorMessage, UpstreamErrorChain: row.ErrorMessage, DurationMs: row.DurationMs, FirstTokenMs: row.FirstTokenMs, CreatedAt: row.CreatedAt})
			lastID = row.ID
		}
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(items, pageSize).Error; err != nil {
			return err
		}
	}
}

// backfillPaymentLedger makes the ledger complete on the first upgraded boot.
// EventKey is unique, so this is safe after an interrupted migration and costs
// only one paged pass over orders that reached a financial terminal state.
func backfillPaymentLedger(db *gorm.DB) error {
	const migrationKey = "migration.payment_ledger_v1"
	var marker model.Setting
	if err := db.Where("key = ?", migrationKey).First(&marker).Error; err == nil {
		return nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	const pageSize = 500
	var lastID int64
	for {
		var orders []model.PaymentOrder
		if err := db.Where("id > ? AND (completed_at IS NOT NULL OR refunded_at IS NOT NULL OR status IN ?)", lastID, []string{model.PaymentStatusCompleted, model.PaymentStatusRefunded}).
			Order("id ASC").Limit(pageSize).Find(&orders).Error; err != nil {
			return err
		}
		if len(orders) == 0 {
			break
		}
		entries := make([]model.PaymentLedgerEntry, 0, len(orders)*2)
		for _, order := range orders {
			if order.CompletedAt != nil || order.Status == model.PaymentStatusCompleted || order.Status == model.PaymentStatusRefunded {
				occurredAt := order.CreatedAt.UTC()
				if order.PaidAt != nil {
					occurredAt = order.PaidAt.UTC()
				}
				if order.CompletedAt != nil {
					occurredAt = order.CompletedAt.UTC()
				}
				entries = append(entries, model.PaymentLedgerEntry{
					EventKey:      fmt.Sprintf("%s:%d", model.PaymentLedgerIncome, order.ID),
					OrderID:       order.ID,
					UserID:        order.UserID,
					Kind:          model.PaymentLedgerIncome,
					Currency:      order.Currency,
					AmountMinor:   order.AmountMinor,
					CreditMicro:   order.CreditMicro,
					ProviderKey:   order.ProviderKey,
					PaymentMethod: order.PaymentMethod,
					OccurredAt:    occurredAt,
				})
			}
			if order.RefundedAt != nil || order.Status == model.PaymentStatusRefunded {
				occurredAt := order.UpdatedAt.UTC()
				if order.RefundedAt != nil {
					occurredAt = order.RefundedAt.UTC()
				} else if occurredAt.IsZero() {
					occurredAt = order.CreatedAt.UTC()
				}
				credit := order.RefundedMicro
				if credit <= 0 {
					credit = order.CreditMicro
				}
				entries = append(entries, model.PaymentLedgerEntry{
					EventKey:      fmt.Sprintf("%s:%d", model.PaymentLedgerExpense, order.ID),
					OrderID:       order.ID,
					UserID:        order.UserID,
					Kind:          model.PaymentLedgerExpense,
					Currency:      order.Currency,
					AmountMinor:   order.AmountMinor,
					CreditMicro:   credit,
					ProviderKey:   order.ProviderKey,
					PaymentMethod: order.PaymentMethod,
					OccurredAt:    occurredAt,
				})
			}
			lastID = order.ID
		}
		if len(entries) > 0 {
			if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&entries).Error; err != nil {
				return err
			}
		}
	}
	return db.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.Setting{Key: migrationKey, Value: "done"}).Error
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
		{Match: "claude-opus-5", Platform: model.PlatformAnthropic, InputPrice: 5, OutputPrice: 25, CacheReadPrice: 0.5, CacheWritePrice: 6.25},
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
		{Name: "claude-opus-5", Platform: model.PlatformAnthropic, Kind: "chat", ContextWindow: 1_000_000, MaxOutputTokens: 128_000, Description: "Claude Opus 5"},
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
