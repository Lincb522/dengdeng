package handler

import "testing"

func TestLoginLockoutIsScopedToAccountAndSource(t *testing.T) {
	h := &AuthHandler{attempts: make(map[string]*loginAttempt)}
	first := loginAttemptKey("Owner@Example.Test", "203.0.113.10")
	second := loginAttemptKey("owner@example.test", "203.0.113.11")

	for i := 0; i < maxLoginFailures; i++ {
		h.recordFailure(first)
	}
	if h.lockoutRemaining(first) <= 0 {
		t.Fatal("expected the failing source to be locked")
	}
	if h.lockoutRemaining(second) != 0 {
		t.Fatal("a different source must not lock the account owner out")
	}
}
