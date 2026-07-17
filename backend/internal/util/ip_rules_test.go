package util

import "testing"

func TestNormalizeAndMatchIPRules(t *testing.T) {
	rules, err := NormalizeIPRules("203.0.113.7, 2001:0db8::/32\n203.0.113.7")
	if err != nil {
		t.Fatal(err)
	}
	if rules != "203.0.113.7,2001:db8::/32" {
		t.Fatalf("rules=%q", rules)
	}
	matched, err := MatchIPRules("2001:db8:1::9", rules)
	if err != nil || !matched {
		t.Fatalf("expected CIDR match, matched=%v err=%v", matched, err)
	}
	matched, err = MatchIPRules("198.51.100.10", rules)
	if err != nil || matched {
		t.Fatalf("unexpected match, matched=%v err=%v", matched, err)
	}
}

func TestNormalizeIPRulesRejectsInvalidInput(t *testing.T) {
	if _, err := NormalizeIPRules("not-an-ip"); err == nil {
		t.Fatal("expected invalid address error")
	}
}
