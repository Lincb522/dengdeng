package gateway

import (
	"net/http"
	"testing"

	"dengdeng/internal/model"
	"dengdeng/internal/service"
)

func TestEnforceClientPolicyVersionAndFingerprint(t *testing.T) {
	policy := service.DefaultGatewayRuntimePolicy()
	policy.CodexClientGateEnabled = true
	policy.MinCodexVersion = "0.110.0"
	policy.MaxCodexVersion = "0.120.0"
	policy.CodexClientWhitelist = []string{"codex_cli_rs"}

	headers := http.Header{"User-Agent": {"codex_cli_rs/0.114.0 (Mac OS; arm64)"}, "Originator": {"codex_cli_rs"}}
	if err := enforceClientPolicy(headers, model.PlatformOpenAI, policy); err != nil {
		t.Fatalf("expected allowed Codex client, got %v", err)
	}
	headers.Set("User-Agent", "codex_cli_rs/0.100.0")
	if err := enforceClientPolicy(headers, model.PlatformOpenAI, policy); err == nil {
		t.Fatal("expected an outdated Codex client to be rejected")
	}
	headers.Set("User-Agent", "codex_cli_rs/0.114.0 app-server")
	policy.CodexAllowAppServerClients = false
	if err := enforceClientPolicy(headers, model.PlatformOpenAI, policy); err == nil {
		t.Fatal("expected app-server to be rejected")
	}
}

func TestEnforceClaudeClientPolicy(t *testing.T) {
	policy := service.DefaultGatewayRuntimePolicy()
	policy.ClaudeClientGateEnabled = true
	policy.MinClaudeCodeVersion = "2.1.0"
	headers := http.Header{"User-Agent": {"claude-cli/2.1.7 (external, cli)"}}
	if err := enforceClientPolicy(headers, model.PlatformAnthropic, policy); err != nil {
		t.Fatalf("expected allowed Claude client, got %v", err)
	}
	headers.Set("User-Agent", "chatbox/1.0")
	if err := enforceClientPolicy(headers, model.PlatformAnthropic, policy); err == nil {
		t.Fatal("expected missing Claude version to be rejected")
	}
}
