package gateway

import (
	"testing"

	"dengdeng/internal/service"
)

func TestEffortRatesDoNotChangeImageRate(t *testing.T) {
	policy := service.NewRuntimePolicyService(nil)
	gateway := &Gateway{policy: policy}
	rates := service.RatePlan{Base: 2, CacheRead: 3, CacheWrite5m: 4, CacheWrite1h: 5, Image: 6}

	got := gateway.effortRates(rates, "high")
	if got.Base != 2.5 || got.CacheRead != 3.75 || got.CacheWrite5m != 5 || got.CacheWrite1h != 6.25 {
		t.Fatalf("unexpected high-effort rates: %#v", got)
	}
	if got.Image != 6 {
		t.Fatalf("image rate changed to %v, want 6", got.Image)
	}
}
