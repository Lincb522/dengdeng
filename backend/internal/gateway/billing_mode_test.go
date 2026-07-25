package gateway

import (
	"testing"

	"dengdeng/internal/model"
)

func TestUsageBillingMode(t *testing.T) {
	tests := []struct {
		name string
		key  *authedKey
		want string
	}{
		{name: "missing", key: nil, want: "none"},
		{name: "balance", key: &authedKey{User: model.User{Role: model.RoleUser}}, want: "usage"},
		{name: "request", key: &authedKey{User: model.User{Role: model.RoleUser}, RequestReserved: true}, want: "request"},
		{name: "day", key: &authedKey{User: model.User{Role: model.RoleUser}, AccessActive: true}, want: "day"},
		{name: "admin", key: &authedKey{User: model.User{Role: model.RoleAdmin}, AccessActive: true, RequestReserved: true}, want: "admin"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := usageBillingMode(test.key); got != test.want {
				t.Fatalf("usageBillingMode() = %q, want %q", got, test.want)
			}
		})
	}
}
