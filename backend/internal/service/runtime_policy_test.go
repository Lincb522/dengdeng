package service

import "testing"

func TestDefaultReasoningEffortMultipliers(t *testing.T) {
	policy := DefaultGatewayRuntimePolicy()
	want := map[string]float64{
		"none": 0.8, "low": 0.9, "medium": 1,
		"high": 1.25, "xhigh": 1.5, "max": 2,
	}
	for effort, multiplier := range want {
		if got := policy.EffortMultiplier(effort); got != multiplier {
			t.Fatalf("%s multiplier = %v, want %v", effort, got, multiplier)
		}
	}
}

func TestNormalizeGatewayRuntimePolicyEffortMultipliers(t *testing.T) {
	policy := DefaultGatewayRuntimePolicy()
	policy.ReasoningEffortMultipliers = map[string]float64{
		"high": 1.4,
		"max":  2.5,
		"fake": 9,
	}
	normalized, err := normalizeGatewayRuntimePolicy(policy)
	if err != nil {
		t.Fatal(err)
	}
	if got := normalized.EffortMultiplier("high"); got != 1.4 {
		t.Fatalf("high multiplier = %v, want 1.4", got)
	}
	if got := normalized.EffortMultiplier("max"); got != 2.5 {
		t.Fatalf("max multiplier = %v, want 2.5", got)
	}
	if got := normalized.EffortMultiplier("low"); got != 0.9 {
		t.Fatalf("missing low multiplier = %v, want default 0.9", got)
	}
	if _, exists := normalized.ReasoningEffortMultipliers["fake"]; exists {
		t.Fatal("unknown effort must not be stored")
	}
}

func TestNormalizeGatewayRuntimePolicyEffortMultiplierBounds(t *testing.T) {
	for _, value := range []float64{0.01, 11} {
		policy := DefaultGatewayRuntimePolicy()
		policy.ReasoningEffortMultipliers = map[string]float64{"high": value}
		if _, err := normalizeGatewayRuntimePolicy(policy); err == nil {
			t.Fatalf("multiplier %v must be rejected", value)
		}
	}
}
