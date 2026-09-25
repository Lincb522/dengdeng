package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dengdeng/internal/config"
	"dengdeng/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestOpenRemovesLegacyZeroProxyForeignKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-proxy.db")
	cfg := config.Default()
	cfg.Database.Path = path
	db, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	group := model.Group{Name: "legacy", Platform: model.PlatformOpenAI, Status: model.StatusActive, RateMultiplier: 1}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	account := model.UpstreamAccount{
		GroupID: group.ID, Name: "legacy", Platform: model.PlatformOpenAI,
		AuthType: model.AuthAPIKey, Status: model.StatusActive, ProxyID: 0,
	}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	closeTestDB(t, db)

	legacy, err := gorm.Open(sqlite.Open(path+"?_pragma=foreign_keys(0)"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	var createSQL string
	if err := legacy.Raw("SELECT sql FROM sqlite_master WHERE type='table' AND name='upstream_accounts'").Scan(&createSQL).Error; err != nil {
		t.Fatal(err)
	}
	closing := strings.LastIndex(createSQL, ")")
	if closing < 0 {
		t.Fatalf("unexpected upstream schema: %s", createSQL)
	}
	createSQL = createSQL[:closing] + ", CONSTRAINT `fk_upstream_accounts_proxy` FOREIGN KEY (`proxy_id`) REFERENCES `proxies`(`id`)" + createSQL[closing:]
	if err := legacy.Exec("ALTER TABLE upstream_accounts RENAME TO upstream_accounts_without_proxy_fk").Error; err != nil {
		t.Fatal(err)
	}
	if err := legacy.Exec(createSQL).Error; err != nil {
		t.Fatal(err)
	}
	if err := legacy.Exec("INSERT INTO upstream_accounts SELECT * FROM upstream_accounts_without_proxy_fk").Error; err != nil {
		t.Fatal(err)
	}
	if err := legacy.Exec("DROP TABLE upstream_accounts_without_proxy_fk").Error; err != nil {
		t.Fatal(err)
	}
	if err := legacy.Exec(`CREATE TABLE legacy_account_refs (
		id INTEGER PRIMARY KEY,
		upstream_account_id INTEGER NOT NULL,
		FOREIGN KEY (upstream_account_id) REFERENCES upstream_accounts(id)
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := legacy.Exec("INSERT INTO legacy_account_refs (id, upstream_account_id) VALUES (1, ?)", account.ID).Error; err != nil {
		t.Fatal(err)
	}
	closeTestDB(t, legacy)

	db, err = Open(cfg)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	defer closeTestDB(t, db)

	var violations []struct {
		Table string
	}
	if err := db.Raw("PRAGMA foreign_key_check").Scan(&violations).Error; err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("foreign key violations remain: %#v", violations)
	}
	if db.Migrator().HasConstraint(&model.UpstreamAccount{}, "Proxy") {
		t.Fatal("legacy proxy constraint still exists")
	}
	var refCount int64
	if err := db.Table("legacy_account_refs").Where("upstream_account_id = ?", account.ID).Count(&refCount).Error; err != nil {
		t.Fatal(err)
	}
	if refCount != 1 {
		t.Fatalf("referencing rows were not preserved: %d", refCount)
	}
}

func TestOpenBackfillsLegacyGroupBindings(t *testing.T) {
	cfg := config.Default()
	cfg.Database.Path = filepath.Join(t.TempDir(), "legacy-key.db")
	db, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	user := model.User{Email: "legacy-key@example.test", PasswordHash: "x", Role: model.RoleUser, Status: model.StatusActive, RateMultiplier: 1}
	group := model.Group{Name: "legacy", Platform: model.PlatformOpenAI, Status: model.StatusActive, IsPublic: true, RateMultiplier: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	key := model.APIKey{UserID: user.ID, GroupID: group.ID, KeyHash: "legacy-hash", KeyPreview: "dd-legacy", Name: "legacy", Status: model.StatusActive}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	account := model.UpstreamAccount{GroupID: group.ID, Name: "legacy-upstream", Platform: model.PlatformOpenAI, AuthType: model.AuthAPIKey, Status: model.StatusActive}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}

	var before int64
	if err := db.Model(&model.APIKeyGroup{}).Where("api_key_id = ?", key.ID).Count(&before).Error; err != nil || before != 0 {
		t.Fatalf("unexpected pre-migration bindings=%d err=%v", before, err)
	}
	if err := db.Model(&model.UpstreamAccountGroup{}).Where("upstream_account_id = ?", account.ID).Count(&before).Error; err != nil || before != 0 {
		t.Fatalf("unexpected pre-migration account bindings=%d err=%v", before, err)
	}
	closeTestDB(t, db)

	db, err = Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestDB(t, db)
	var bindings []model.APIKeyGroup
	if err := db.Where("api_key_id = ?", key.ID).Find(&bindings).Error; err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 || bindings[0].GroupID != group.ID {
		t.Fatalf("legacy binding not restored: %#v", bindings)
	}
	var accountBindings []model.UpstreamAccountGroup
	if err := db.Where("upstream_account_id = ?", account.ID).Find(&accountBindings).Error; err != nil {
		t.Fatal(err)
	}
	if len(accountBindings) != 1 || accountBindings[0].GroupID != group.ID {
		t.Fatalf("legacy account binding not restored: %#v", accountBindings)
	}
}

func TestBackfillPaymentLedgerIsCompleteAndIdempotent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.PaymentOrder{}, &model.PaymentLedgerEntry{}, &model.Setting{}); err != nil {
		t.Fatal(err)
	}
	completedAt := time.Now().UTC().Add(-2 * time.Hour)
	refundedAt := time.Now().UTC().Add(-time.Hour)
	orders := []model.PaymentOrder{
		{OutTradeNo: "backfill-income", UserID: 1, ProviderID: 1, ProviderKey: "wxpay", PaymentMethod: "wxpay", Status: model.PaymentStatusCompleted, Currency: "CNY", AmountMinor: 1000, CreditMicro: 10_000_000, ExpiresAt: completedAt.Add(time.Hour), CompletedAt: &completedAt},
		{OutTradeNo: "backfill-refund", UserID: 2, ProviderID: 1, ProviderKey: "stripe", PaymentMethod: "card", Status: model.PaymentStatusRefunded, Currency: "USD", AmountMinor: 2000, CreditMicro: 20_000_000, RefundedMicro: 20_000_000, ExpiresAt: completedAt.Add(time.Hour), RefundedAt: &refundedAt},
	}
	if err := db.Create(&orders).Error; err != nil {
		t.Fatal(err)
	}
	if err := backfillPaymentLedger(db); err != nil {
		t.Fatal(err)
	}
	if err := backfillPaymentLedger(db); err != nil {
		t.Fatal(err)
	}
	var entries []model.PaymentLedgerEntry
	if err := db.Order("event_key ASC").Find(&entries).Error; err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("ledger entries=%d, want 3: %+v", len(entries), entries)
	}
	var income, expense int
	for _, entry := range entries {
		if entry.Kind == model.PaymentLedgerIncome {
			income++
		}
		if entry.Kind == model.PaymentLedgerExpense {
			expense++
			if !entry.OccurredAt.Equal(refundedAt) {
				t.Fatalf("refund occurred_at=%s, want %s", entry.OccurredAt, refundedAt)
			}
		}
	}
	if income != 2 || expense != 1 {
		t.Fatalf("income=%d expense=%d, want 2/1", income, expense)
	}
}

func closeTestDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSeedDefaultModelConfigsBackfillsMissingLimits(t *testing.T) {
	db := openModelConfigTestDB(t)
	if err := db.Create(&model.ModelConfig{
		Name:            "gpt-5.6",
		Platform:        model.PlatformOpenAI,
		Kind:            "chat",
		ContextWindow:   0,
		MaxOutputTokens: 9_999,
	}).Error; err != nil {
		t.Fatal(err)
	}

	if err := seedDefaultModelConfigs(db); err != nil {
		t.Fatal(err)
	}

	var got model.ModelConfig
	if err := db.Where("name = ?", "gpt-5.6").First(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.ContextWindow != 1_050_000 {
		t.Fatalf("context window = %d, want 1050000", got.ContextWindow)
	}
	if got.MaxOutputTokens != 9_999 {
		t.Fatalf("custom max output was overwritten: got %d", got.MaxOutputTokens)
	}
}

func TestDefaultModelConfigsHaveCompletePublishedLimits(t *testing.T) {
	expected := map[string][2]int64{
		"gpt-6-astra":                  {1_050_000, 128_000},
		"gpt-6-sol":                    {1_050_000, 128_000},
		"gpt-6-luna":                   {1_050_000, 128_000},
		"gpt-5.6":                      {1_050_000, 128_000},
		"gpt-5.6-sol":                  {1_050_000, 128_000},
		"gpt-5.6-terra":                {1_050_000, 128_000},
		"gpt-5.6-luna":                 {1_050_000, 128_000},
		"gpt-5.5":                      {1_050_000, 128_000},
		"gpt-5.5-pro":                  {1_050_000, 128_000},
		"gpt-image-2.5-sunburst":       {0, 0},
		"gpt-image-2.5-flare":          {0, 0},
		"gpt-image-2":                  {0, 0},
		"claude-fable-5-1":             {1_000_000, 128_000},
		"claude-opus-5-5":              {1_000_000, 128_000},
		"claude-fable-5":               {1_000_000, 128_000},
		"claude-opus-5":                {1_000_000, 128_000},
		"claude-opus-4-8":              {1_000_000, 128_000},
		"claude-opus-4-7":              {1_000_000, 128_000},
		"claude-opus-4-6":              {1_000_000, 128_000},
		"claude-opus-4-5-20251101":     {200_000, 64_000},
		"claude-sonnet-5":              {1_000_000, 128_000},
		"claude-sonnet-4-6":            {1_000_000, 64_000},
		"claude-sonnet-4-5-20250929":   {200_000, 64_000},
		"claude-haiku-4-5-20251001":    {200_000, 64_000},
		"claude-mythos-5-1":            {1_000_000, 128_000},
		"claude-mythos-5":              {1_000_000, 128_000},
		"claude-mythos-preview":        {1_000_000, 128_000},
		"gemini-3.8-flash":             {1_048_576, 65_536},
		"gemini-3.7-flash":             {1_048_576, 65_536},
		"gemini-3.6-flash":             {1_048_576, 65_536},
		"gemini-3.5-flash":             {1_048_576, 65_536},
		"gemini-3.5-flash-lite":        {1_048_576, 65_536},
		"gemini-3.1-flash-lite":        {1_048_576, 65_536},
		"gemini-2.5-flash-image":       {65_536, 32_768},
		"gemini-3.1-flash-image":       {65_536, 32_768},
		"gemini-3.1-flash-lite-image":  {65_536, 32_768},
		"gemini-3-pro-image":           {65_536, 32_768},
		"grok-4.7":                     {500_000, 0},
		"grok-4.5":                     {500_000, 0},
		"grok-4.3":                     {1_000_000, 0},
		"grok-composer-2.5-fast":       {256_000, 0},
		"grok-imagine-image":           {1_024, 0},
		"kimi-k3":                      {1_048_576, 1_048_576},
		"kimi-k2.7-code":               {262_144, 262_144},
		"kimi-k2.7-code-highspeed":     {262_144, 262_144},
		"kimi-k2.6":                    {262_144, 262_144},
		"glm-5.3":                      {1_000_000, 128_000},
		"glm-5.2":                      {1_000_000, 128_000},
		"glm-5-turbo":                  {200_000, 128_000},
		"glm-4.7":                      {200_000, 128_000},
		"glm-4.7-flashx":               {200_000, 128_000},
		"glm-4.7-flash":                {200_000, 128_000},
		"glm-5v-turbo":                 {200_000, 128_000},
		"glm-image":                    {0, 0},
		"deepseek-flash":               {1_000_000, 384_000},
		"deepseek-v4-flash":            {1_000_000, 384_000},
		"deepseek-v4-pro":              {1_000_000, 384_000},
		"deepseek-v4-flash-vision-exp": {1_000_000, 384_000},
	}

	configs := defaultModelConfigs()
	if len(configs) != len(expected) {
		t.Fatalf("default config count = %d, want %d", len(configs), len(expected))
	}
	for _, cfg := range configs {
		want, ok := expected[cfg.Name]
		if !ok {
			t.Errorf("unexpected default model %q", cfg.Name)
			continue
		}
		if cfg.ContextWindow != want[0] || cfg.MaxOutputTokens != want[1] {
			t.Errorf("%s limits = %d/%d, want %d/%d", cfg.Name, cfg.ContextWindow, cfg.MaxOutputTokens, want[0], want[1])
		}
		if strings.HasPrefix(cfg.Name, "gpt-6-") && (!cfg.SupportsVision || !cfg.SupportsTools || !cfg.SupportsReasoning) {
			t.Errorf("%s capability flags = vision:%t tools:%t reasoning:%t, want all enabled", cfg.Name, cfg.SupportsVision, cfg.SupportsTools, cfg.SupportsReasoning)
		}
	}
}

func TestDefaultDomesticModelPricesAreCompleteAndUnique(t *testing.T) {
	prices := defaultDomesticModelPrices()
	wantPlatforms := map[string]int{
		model.PlatformKimi: 4, model.PlatformZhipu: 8, model.PlatformDeepSeek: 4,
	}
	seen := make(map[string]bool, len(prices))
	for _, price := range prices {
		if price.Match == "" {
			t.Fatal("domestic model price has an empty match")
		}
		if seen[price.Match] {
			t.Fatalf("duplicate domestic model price %q", price.Match)
		}
		seen[price.Match] = true
		wantPlatforms[price.Platform]--
	}
	for platform, remaining := range wantPlatforms {
		if remaining != 0 {
			t.Errorf("platform %s model price count differs by %d", platform, remaining)
		}
	}
	if got := findDefaultPrice(prices, "kimi-k3"); got == nil || got.InputPrice != 3 || got.OutputPrice != 15 || got.CacheReadPrice != .3 {
		t.Fatalf("unexpected Kimi K3 price: %#v", got)
	}
	if got := findDefaultPrice(prices, "glm-image"); got == nil || got.ImagePricePerImage != .015 {
		t.Fatalf("unexpected GLM-Image price: %#v", got)
	}
	if got := findDefaultPrice(prices, "deepseek-v4-pro"); got == nil || got.InputPrice != 1.32 || got.OutputPrice != 3.96 || got.CacheReadPrice != .044 {
		t.Fatalf("unexpected DeepSeek V4 Pro price: %#v", got)
	}
	if got := findDefaultPrice(prices, "deepseek-flash"); got == nil || got.InputPrice != .3 || got.OutputPrice != 1.2 || got.CacheReadPrice != .006 {
		t.Fatalf("unexpected DeepSeek Flash price: %#v", got)
	}
}

func TestSeedAddsLatestOfficialModelPrices(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.ModelPrice{}, &model.ModelConfig{}, &model.Setting{}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Admin.Password = "seed-test-password"
	if err := Seed(db, cfg); err != nil {
		t.Fatal(err)
	}

	checks := map[string][6]float64{
		"gpt-6-astra":      {10, 50, 1, 12.5, 0, 0},
		"gpt-6-sol":        {2, 10, .2, 2.5, 0, 0},
		"gpt-6-luna":       {.1, .5, .01, .125, 0, 0},
		"gpt-5.6-sol":      {4, 20, .4, 5, 0, 0},
		"claude-fable-5-1": {10, 50, .25, 12.5, 12.5, 20},
		"claude-opus-5-5":  {4, 20, .2, 5, 5, 8},
		"gemini-3.8-flash": {.75, 3.75, .075, 0, 0, 0},
		"grok-4.7":         {2, 6, .5, 0, 0, 0},
		"deepseek-flash":   {.3, 1.2, .006, 0, 0, 0},
	}
	for match, want := range checks {
		var got model.ModelPrice
		if err := db.Where("match = ?", match).First(&got).Error; err != nil {
			t.Fatalf("load %s: %v", match, err)
		}
		actual := [6]float64{got.InputPrice, got.OutputPrice, got.CacheReadPrice, got.CacheWritePrice, got.CacheWrite5mPrice, got.CacheWrite1hPrice}
		if actual != want {
			t.Errorf("%s price = %v, want %v", match, actual, want)
		}
	}

	for _, match := range []string{"gpt-image-2.5-sunburst", "gpt-image-2.5-flare"} {
		var got model.ModelPrice
		if err := db.Where("match = ?", match).First(&got).Error; err != nil {
			t.Fatalf("load %s: %v", match, err)
		}
		if got.InputPrice != 5 || got.CacheReadPrice != 1.25 || got.ImageInputPrice != 8 || got.ImageCacheReadPrice != 2 || got.ImageOutputPrice != 30 {
			t.Errorf("%s image price is incomplete: %#v", match, got)
		}
	}
}

func TestMigrateOfficialModelPricesPreservesOperatorEdits(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.ModelPrice{}, &model.Setting{}); err != nil {
		t.Fatal(err)
	}
	rows := []model.ModelPrice{
		{Match: "gpt-5.6", Platform: model.PlatformOpenAI, InputPrice: 5, OutputPrice: 30, CacheReadPrice: .5, CacheWritePrice: 6.25},
		{Match: "gpt-5.6-terra", Platform: model.PlatformOpenAI, InputPrice: 9, OutputPrice: 15, CacheReadPrice: .25, CacheWritePrice: 3.125},
		{Match: "deepseek-v4-flash", Platform: model.PlatformDeepSeek, InputPrice: .44, OutputPrice: 1.32, CacheReadPrice: .014},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateOfficialModelPrices20260925(db); err != nil {
		t.Fatal(err)
	}
	if err := migrateOfficialModelPrices20260925(db); err != nil {
		t.Fatalf("idempotent second migration failed: %v", err)
	}

	var flagship, customized, deepseek model.ModelPrice
	if err := db.Where("match = ?", "gpt-5.6").First(&flagship).Error; err != nil {
		t.Fatal(err)
	}
	if flagship.InputPrice != 4 || flagship.OutputPrice != 20 || flagship.CacheReadPrice != .4 || flagship.CacheWritePrice != 5 {
		t.Fatalf("official price was not migrated: %#v", flagship)
	}
	if err := db.Where("match = ?", "gpt-5.6-terra").First(&customized).Error; err != nil {
		t.Fatal(err)
	}
	if customized.InputPrice != 9 || customized.OutputPrice != 15 || customized.CacheReadPrice != .25 || customized.CacheWritePrice != 3.125 {
		t.Fatalf("operator price was overwritten: %#v", customized)
	}
	if err := db.Where("match = ?", "deepseek-v4-flash").First(&deepseek).Error; err != nil {
		t.Fatal(err)
	}
	if deepseek.InputPrice != .3 || deepseek.OutputPrice != 1.2 || deepseek.CacheReadPrice != .006 {
		t.Fatalf("DeepSeek alias price was not migrated: %#v", deepseek)
	}
	var marker model.Setting
	if err := db.Where("key = ?", officialModelPrices20260925MigrationKey).First(&marker).Error; err != nil {
		t.Fatal(err)
	}
}

func findDefaultPrice(prices []model.ModelPrice, match string) *model.ModelPrice {
	for i := range prices {
		if prices[i].Match == match {
			return &prices[i]
		}
	}
	return nil
}

func TestBackfillTierPricingColumnsRepairsLegacyValuesOnce(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:tier-pricing-backfill?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Group{}, &model.UsageLog{}, &model.Setting{}); err != nil {
		t.Fatal(err)
	}
	group := model.Group{Name: "legacy-pricing", Platform: model.PlatformOpenAI, Status: model.StatusActive}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Group{}).Where("id = ?", group.ID).Updates(map[string]any{
		"fast_rate_multiplier": 0, "flex_rate_multiplier": 0,
		"long_context_threshold": -1, "long_context_input_multiplier": 0,
		"long_context_output_multiplier": 0, "long_context_cache_multiplier": 0,
	}).Error; err != nil {
		t.Fatal(err)
	}
	usage := model.UsageLog{RequestID: "legacy-tier", StatusCode: 200}
	if err := db.Create(&usage).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.UsageLog{}).Where("id = ?", usage.ID).Updates(map[string]any{
		"service_tier_multiplier": 0, "long_context_tokens": -1, "long_context_threshold": -1,
	}).Error; err != nil {
		t.Fatal(err)
	}

	if err := backfillTierPricingColumns(db); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&group, group.ID).Error; err != nil {
		t.Fatal(err)
	}
	if group.FastRateMultiplier != 2 || group.FlexRateMultiplier != .5 || group.LongContextThreshold != 0 ||
		group.LongContextInputMultiplier != 1 || group.LongContextOutputMultiplier != 1 || group.LongContextCacheMultiplier != 1 {
		t.Fatalf("group pricing not repaired: %#v", group)
	}
	if err := db.First(&usage, usage.ID).Error; err != nil {
		t.Fatal(err)
	}
	if usage.ServiceTierMultiplier != 1 || usage.LongContextTokens != 0 || usage.LongContextThreshold != 0 {
		t.Fatalf("usage pricing snapshot not repaired: %#v", usage)
	}
	var marker model.Setting
	if err := db.Where("key = ?", tierPricingMigrationKey).First(&marker).Error; err != nil {
		t.Fatal(err)
	}
	if err := backfillTierPricingColumns(db); err != nil {
		t.Fatalf("idempotent second run failed: %v", err)
	}
}

func openModelConfigTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.ModelConfig{}); err != nil {
		t.Fatal(err)
	}
	return db
}
