package gateway

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"dengdeng/internal/model"
	"dengdeng/internal/service"
)

var numericVersionPattern = regexp.MustCompile(`(?i)(?:claude-cli|claude-code|codex_cli_rs|codex-cli|codex)/([0-9]+(?:\.[0-9]+){1,3})`)

func requestClientVersion(headers http.Header) string {
	if version := strings.TrimPrefix(strings.TrimSpace(headers.Get("Version")), "v"); version != "" {
		valid := true
		for _, part := range strings.Split(version, ".") {
			if _, err := strconv.Atoi(part); err != nil {
				valid = false
				break
			}
		}
		if valid {
			return version
		}
	}
	match := numericVersionPattern.FindStringSubmatch(headers.Get("User-Agent"))
	if len(match) == 2 {
		return match[1]
	}
	return ""
}

func compareNumericVersions(left, right string) int {
	lp, rp := strings.Split(strings.TrimPrefix(left, "v"), "."), strings.Split(strings.TrimPrefix(right, "v"), ".")
	count := len(lp)
	if len(rp) > count {
		count = len(rp)
	}
	for i := 0; i < count; i++ {
		var l, r int
		if i < len(lp) {
			l, _ = strconv.Atoi(lp[i])
		}
		if i < len(rp) {
			r, _ = strconv.Atoi(rp[i])
		}
		if l < r {
			return -1
		}
		if l > r {
			return 1
		}
	}
	return 0
}

func enforceClientPolicy(headers http.Header, platform string, policy service.GatewayRuntimePolicy) error {
	version := requestClientVersion(headers)
	if platform == model.PlatformAnthropic && policy.ClaudeClientGateEnabled {
		if version == "" {
			return fmt.Errorf("Claude Code client version is required")
		}
		if policy.MinClaudeCodeVersion != "" && compareNumericVersions(version, policy.MinClaudeCodeVersion) < 0 {
			return fmt.Errorf("Claude Code %s or newer is required", policy.MinClaudeCodeVersion)
		}
		if policy.MaxClaudeCodeVersion != "" && compareNumericVersions(version, policy.MaxClaudeCodeVersion) > 0 {
			return fmt.Errorf("Claude Code version is newer than the allowed maximum %s", policy.MaxClaudeCodeVersion)
		}
	}
	if platform != model.PlatformOpenAI || !policy.CodexClientGateEnabled {
		return nil
	}
	fingerprint := strings.ToLower(strings.Join([]string{headers.Get("User-Agent"), headers.Get("Originator"), headers.Get("x-stainless-package-version")}, " "))
	if !policy.CodexAllowAppServerClients && (strings.Contains(fingerprint, "app-server") || strings.Contains(fingerprint, "app_server")) {
		return fmt.Errorf("Codex app-server clients are disabled")
	}
	for _, blocked := range policy.CodexClientBlacklist {
		if strings.Contains(fingerprint, blocked) {
			return fmt.Errorf("Codex client fingerprint is blocked")
		}
	}
	if len(policy.CodexClientWhitelist) > 0 {
		matched := false
		for _, allowed := range policy.CodexClientWhitelist {
			if strings.Contains(fingerprint, allowed) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("Codex client fingerprint is not allowed")
		}
	}
	if version == "" {
		return fmt.Errorf("Codex client version is required")
	}
	if policy.MinCodexVersion != "" && compareNumericVersions(version, policy.MinCodexVersion) < 0 {
		return fmt.Errorf("Codex %s or newer is required", policy.MinCodexVersion)
	}
	if policy.MaxCodexVersion != "" && compareNumericVersions(version, policy.MaxCodexVersion) > 0 {
		return fmt.Errorf("Codex version is newer than the allowed maximum %s", policy.MaxCodexVersion)
	}
	return nil
}
