package service

import (
	"strings"
	"testing"
)

func TestRegistrationEmailUsesProductPalette(t *testing.T) {
	message := registrationEmail("DengDeng AI · 蹬蹬ai", "https://dengdeng.example.test/", "no-reply@example.test", "user@example.test", "123456")
	for _, part := range []string{"#fffaf1", "#30261e", "#c98a20", "123456", "确认你的邮箱", "https://dengdeng.example.test/brand/dengdeng-avatar.png", "width=\"42\""} {
		if !strings.Contains(message, part) {
			t.Fatalf("registration email is missing %q", part)
		}
	}
}
