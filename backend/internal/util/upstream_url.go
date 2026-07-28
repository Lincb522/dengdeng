package util

import (
	"fmt"
	"net/url"
	"strings"
)

var upstreamEndpointSuffixes = []string{
	"/responses/input_tokens",
	"/messages/count_tokens",
	"/images/generations",
	"/chat/completions",
	"/responses/compact",
	"/responses",
	"/messages",
	"/models",
}

// NormalizeUpstreamBaseURL accepts both host-style bases and SDK-style bases.
// It removes only a trailing slash or a complete API endpoint; version prefixes
// such as /v1 and /v1beta are retained so the value remains familiar in the UI.
func NormalizeUpstreamBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid upstream base URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("upstream base URL must use http or https")
	}
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.RawFragment = ""

	path := strings.TrimRight(parsed.Path, "/")
	lowerPath := strings.ToLower(path)
	for _, suffix := range upstreamEndpointSuffixes {
		if strings.HasSuffix(lowerPath, suffix) {
			path = strings.TrimRight(path[:len(path)-len(suffix)], "/")
			lowerPath = strings.ToLower(path)
			break
		}
	}
	if marker := strings.Index(lowerPath, "/v1beta/models/"); marker >= 0 {
		path = path[:marker+len("/v1beta")]
	}
	if path == "/" {
		path = ""
	}
	parsed.Path = path
	parsed.RawPath = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

// JoinUpstreamURL appends a gateway request path without duplicating a version
// prefix already present in the configured base URL.
func JoinUpstreamURL(rawBase, rawTarget string) (string, error) {
	base, err := NormalizeUpstreamBaseURL(rawBase)
	if err != nil {
		return "", err
	}
	if base == "" {
		return "", fmt.Errorf("upstream base URL is required")
	}
	target := "/" + strings.TrimLeft(strings.TrimSpace(rawTarget), "/")
	if target == "/" {
		return base, nil
	}
	if parsed, parseErr := url.Parse(target); parseErr != nil || parsed.IsAbs() || parsed.Host != "" {
		return "", fmt.Errorf("invalid upstream request path")
	}

	lowerBase := strings.ToLower(base)
	lowerTarget := strings.ToLower(target)
	for _, version := range []string{"/v1beta", "/v1"} {
		if strings.HasSuffix(lowerBase, version) &&
			(lowerTarget == version || strings.HasPrefix(lowerTarget, version+"/") || strings.HasPrefix(lowerTarget, version+"?")) {
			return base + target[len(version):], nil
		}
	}
	return base + target, nil
}
