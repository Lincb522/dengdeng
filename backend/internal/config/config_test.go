package config

import (
	"reflect"
	"testing"
)

func TestDefaultTrustsOnlyLoopbackReverseProxy(t *testing.T) {
	cfg := Default()
	want := []string{"127.0.0.1", "::1"}
	if !reflect.DeepEqual(cfg.Server.TrustedProxies, want) {
		t.Fatalf("trusted proxies = %#v, want %#v", cfg.Server.TrustedProxies, want)
	}
	if !reflect.DeepEqual(cfg.Server.ForwardedClientIPHeaders, []string{"X-Forwarded-For", "X-Real-IP"}) {
		t.Fatalf("forwarded headers = %#v", cfg.Server.ForwardedClientIPHeaders)
	}
}
