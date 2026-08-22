package service

import (
	"testing"
	"time"

	"dengdeng/internal/model"
)

func TestPricingCostSplitsCacheTTLAndRates(t *testing.T) {
	pricing := &PricingService{
		cache: []model.ModelPrice{{
			Match:               "claude-test",
			InputPrice:          2,
			OutputPrice:         8,
			CacheReadPrice:      0.2,
			CacheWritePrice:     2.5,
			CacheWrite5mPrice:   2.5,
			CacheWrite1hPrice:   4,
			ImageInputPrice:     6,
			ImageOutputPrice:    10,
			ImageCacheReadPrice: 1,
		}},
		until: time.Now().Add(time.Hour),
	}

	usage := Usage{
		InputTokens:          100,
		OutputTokens:         20,
		CacheReadTokens:      50,
		CacheWriteTokens:     35,
		CacheWrite5mTokens:   10,
		CacheWrite1hTokens:   20,
		ImageInputTokens:     5,
		ImageOutputTokens:    3,
		ImageCacheReadTokens: 2,
	}
	rates := RatePlan{Base: 2, CacheRead: 3, CacheWrite5m: 4, CacheWrite1h: 5, Image: 6}

	// 100*2*2 + 20*8*2 + 50*.2*3 + 10*2.5*4 + 20*4*5 + 5*2.5*4
	// + (5*6 + 3*10 + 2*1)*6 = 1672 micro-USD.
	if got, want := pricing.Cost("claude-test", usage, rates), int64(1672); got != want {
		t.Fatalf("Cost() = %d, want %d", got, want)
	}

	breakdown := pricing.Breakdown("claude-test", usage, rates)
	if breakdown.TotalMicro != 1672 || breakdown.RawMicro != 549 {
		t.Fatalf("breakdown totals = charged %d raw %d, want 1672 and 549", breakdown.TotalMicro, breakdown.RawMicro)
	}
	if got := breakdown.InputMicro + breakdown.OutputMicro + breakdown.CacheReadMicro + breakdown.CacheWriteMicro + breakdown.ImageMicro; got != breakdown.TotalMicro {
		t.Fatalf("component total = %d, want %d", got, breakdown.TotalMicro)
	}
	if breakdown.InputUnitPrice != 2 || breakdown.OutputUnitPrice != 8 ||
		breakdown.CacheWrite5mPrice != 2.5 || breakdown.CacheWrite1hPrice != 4 {
		t.Fatalf("unexpected unit-price snapshot: %#v", breakdown)
	}
	if breakdown.EffectiveMultiplier <= 3.04 || breakdown.EffectiveMultiplier >= 3.05 {
		t.Fatalf("effective multiplier = %f, want about 3.043", breakdown.EffectiveMultiplier)
	}
}

func TestPricingCostFallsBackForUndividedCacheWrite(t *testing.T) {
	pricing := &PricingService{
		cache: []model.ModelPrice{{Match: "claude-test", CacheWritePrice: 2.5}},
		until: time.Now().Add(time.Hour),
	}

	// A legacy provider only returns cache_creation_input_tokens. It must keep
	// its existing price and use the short-cache multiplier as the fallback.
	if got, want := pricing.Cost("claude-test", Usage{CacheWriteTokens: 10}, RatePlan{CacheWrite5m: 2}), int64(50); got != want {
		t.Fatalf("Cost() = %d, want %d", got, want)
	}
}

func TestPricingCostDoesNotDoubleBillOpenAICachedInput(t *testing.T) {
	pricing := &PricingService{
		cache: []model.ModelPrice{{Match: "openai-test", InputPrice: 5, CacheReadPrice: 0.5}},
		until: time.Now().Add(time.Hour),
	}

	// OpenAI's input_tokens includes the 80 cached tokens. They must be
	// removed from regular input billing and charged only at the cache rate.
	usage := Usage{InputTokens: 100, CacheReadTokens: 80, InputIncludesCacheRead: true}
	if got, want := pricing.Cost("openai-test", usage, RatePlan{Base: 1, CacheRead: 1}), int64(140); got != want {
		t.Fatalf("Cost() = %d, want %d", got, want)
	}
}

func TestPricingCostUsesPerImagePriceInsteadOfImageTokens(t *testing.T) {
	pricing := &PricingService{
		cache: []model.ModelPrice{{
			Match:               "image-test",
			ImagePricePerImage:  0.08,
			ImageInputPrice:     8,
			ImageOutputPrice:    30,
			ImageCacheReadPrice: 2,
		}},
		until: time.Now().Add(time.Hour),
	}

	usage := Usage{ImageCount: 3, ImageInputTokens: 1000, ImageOutputTokens: 2000, ImageCacheReadTokens: 500}
	if got, want := pricing.Cost("image-test", usage, RatePlan{Image: 1.5}), int64(360000); got != want {
		t.Fatalf("Cost() = %d, want %d", got, want)
	}
}

func TestPricingAppliesConfiguredServiceTierMultipliers(t *testing.T) {
	pricing := &PricingService{
		cache: []model.ModelPrice{{Match: "tier-test", InputPrice: 1, OutputPrice: 2}},
		until: time.Now().Add(time.Hour),
	}
	usage := Usage{InputTokens: 100, OutputTokens: 10}
	rates := RatePlan{Base: 1, Fast: 1.8, Flex: .4}

	fast := pricing.BreakdownForTier("tier-test", usage, rates, "fast")
	if fast.TotalMicro != 216 || fast.ServiceTierMultiplier != 1.8 {
		t.Fatalf("fast breakdown = %#v, want total 216 at 1.8x", fast)
	}
	priority := pricing.BreakdownForTier("tier-test", usage, rates, "priority")
	if priority.TotalMicro != fast.TotalMicro {
		t.Fatalf("priority total = %d, want fast total %d", priority.TotalMicro, fast.TotalMicro)
	}
	flex := pricing.BreakdownForTier("tier-test", usage, rates, "flex")
	if flex.TotalMicro != 48 || flex.ServiceTierMultiplier != .4 {
		t.Fatalf("flex breakdown = %#v, want total 48 at .4x", flex)
	}
}

func TestPricingAppliesLongContextComponentMultipliersAtThreshold(t *testing.T) {
	pricing := &PricingService{
		cache: []model.ModelPrice{{Match: "long-test", InputPrice: 1, OutputPrice: 2, CacheReadPrice: .1}},
		until: time.Now().Add(time.Hour),
	}
	rates := RatePlan{
		Base: 1, CacheRead: 1,
		LongContextThreshold: 120,
		LongContextInput:     2, LongContextOutput: 1.5, LongContextCache: 3,
	}
	breakdown := pricing.Breakdown("long-test", Usage{
		InputTokens: 100, OutputTokens: 10, CacheReadTokens: 20,
	}, rates)
	if !breakdown.LongContextApplied || breakdown.LongContextTokens != 120 || breakdown.LongContextThreshold != 120 {
		t.Fatalf("long-context audit = %#v", breakdown)
	}
	if breakdown.TotalMicro != 236 {
		t.Fatalf("long-context total = %d, want 236", breakdown.TotalMicro)
	}

	below := pricing.Breakdown("long-test", Usage{InputTokens: 99, CacheReadTokens: 20}, rates)
	if below.LongContextApplied || below.TotalMicro != 101 {
		t.Fatalf("below-threshold breakdown = %#v, want ordinary total 101", below)
	}
}
