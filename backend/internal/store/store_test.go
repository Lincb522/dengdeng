package store

import (
	"testing"

	"dengdeng/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

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
		"gpt-5.6":                    {1_050_000, 128_000},
		"gpt-5.6-sol":                {1_050_000, 128_000},
		"gpt-5.6-terra":              {1_050_000, 128_000},
		"gpt-5.6-luna":               {1_050_000, 128_000},
		"gpt-5.5":                    {1_050_000, 128_000},
		"gpt-5.5-pro":                {1_050_000, 128_000},
		"gpt-image-2":                {0, 0},
		"claude-fable-5":             {1_000_000, 128_000},
		"claude-opus-4-8":            {1_000_000, 128_000},
		"claude-opus-4-7":            {1_000_000, 128_000},
		"claude-opus-4-6":            {1_000_000, 128_000},
		"claude-opus-4-5-20251101":   {200_000, 64_000},
		"claude-sonnet-5":            {1_000_000, 128_000},
		"claude-sonnet-4-6":          {1_000_000, 64_000},
		"claude-sonnet-4-5-20250929": {200_000, 64_000},
		"claude-haiku-4-5-20251001":  {200_000, 64_000},
		"claude-mythos-5":            {1_000_000, 128_000},
		"claude-mythos-preview":      {1_000_000, 128_000},
		"gemini-2.5-flash-image":     {65_536, 32_768},
		"gemini-3-pro-image":         {65_536, 32_768},
		"grok-4.5":                   {500_000, 0},
		"grok-4.3":                   {1_000_000, 0},
		"grok-composer-2.5-fast":     {256_000, 0},
		"grok-imagine-image":         {1_024, 0},
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
