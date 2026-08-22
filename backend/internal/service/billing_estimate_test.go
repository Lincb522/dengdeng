package service

import (
	"testing"
	"time"

	"dengdeng/internal/model"
)

func TestEstimateMaximumUsesByteToTokenApproximation(t *testing.T) {
	pricing := &PricingService{
		cache: []model.ModelPrice{{Match: "test-model", InputPrice: 3, OutputPrice: 12}},
		until: time.Now().Add(time.Hour),
	}
	billing := NewBillingService(nil, pricing)

	// 300 KB JSON is approximately 100K input tokens, plus the 4096-token
	// default output allowance and the 20% safety margin. The old byte=token
	// estimate was about three times larger and denied valid low-balance users.
	got := billing.EstimateMaximum("test-model", 300_000, 0, 0, RatePlan{Base: 1})
	want := int64(418_982)
	if got != want {
		t.Fatalf("EstimateMaximum() = %d, want %d", got, want)
	}
}

func TestEstimateMaximumUsesSameTierAndLongContextRulesAsSettlement(t *testing.T) {
	pricing := &PricingService{
		cache: []model.ModelPrice{{Match: "tier-model", InputPrice: 1}},
		until: time.Now().Add(time.Hour),
	}
	billing := NewBillingService(nil, pricing)
	rates := RatePlan{Base: 1, Fast: 2, LongContextThreshold: 100, LongContextInput: 1.5}

	// 600 bytes ~= 200 input tokens. 200 * 1.5 long-context * 2 fast,
	// followed by the normal 20% reservation safety margin.
	got := billing.EstimateMaximum("tier-model", 600, 1, 0, rates, "fast")
	if got != 720 {
		t.Fatalf("tiered EstimateMaximum() = %d, want 720", got)
	}
}
