package service

import (
	"math"
	"testing"
)

func TestHostCPUPercentUsesIdleDelta(t *testing.T) {
	got := hostCPUPercent(400, 1000, 460, 1200)
	if math.Abs(got-70) > 0.001 {
		t.Fatalf("expected 70%% host CPU usage, got %.3f", got)
	}
}

func TestHostCPUPercentRejectsInvalidCounters(t *testing.T) {
	for _, test := range []struct {
		name                        string
		previousIdle, previousTotal uint64
		idle, total                 uint64
	}{
		{name: "unchanged", previousIdle: 40, previousTotal: 100, idle: 40, total: 100},
		{name: "reset", previousIdle: 40, previousTotal: 100, idle: 10, total: 20},
		{name: "idle exceeds delta", previousIdle: 10, previousTotal: 100, idle: 60, total: 120},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := hostCPUPercent(test.previousIdle, test.previousTotal, test.idle, test.total); got != 0 {
				t.Fatalf("expected invalid counters to return zero, got %.3f", got)
			}
		})
	}
}

func TestPercentOf(t *testing.T) {
	if got := percentOf(24, 100); math.Abs(got-24) > 0.001 {
		t.Fatalf("expected 24%%, got %.3f", got)
	}
	if got := percentOf(1, 0); got != 0 {
		t.Fatalf("expected zero total to return zero, got %.3f", got)
	}
}
