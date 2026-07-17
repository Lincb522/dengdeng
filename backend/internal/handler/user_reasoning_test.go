package handler

import "testing"

func TestNormalizeReasoningEffort(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{"", "auto"}, {"AUTO", "auto"},
		{"fast", "low"}, {"minimal", "low"},
		{"none", "none"}, {"low", "low"}, {"medium", "medium"},
		{"high", "high"}, {"xhigh", "xhigh"}, {"max", "max"},
	} {
		got, err := normalizeReasoningEffort(test.input)
		if err != nil || got != test.want {
			t.Fatalf("normalize(%q) = %q, %v; want %q", test.input, got, err, test.want)
		}
	}
	if _, err := normalizeReasoningEffort("turbo"); err == nil {
		t.Fatal("invalid effort should fail")
	}
}
