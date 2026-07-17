package util

import (
	"fmt"
	"net/netip"
	"strings"
)

// NormalizeIPRules accepts comma, semicolon or whitespace separated addresses
// and CIDRs. Rules are canonicalized before persistence so an operator can
// inspect exactly what the gateway will apply.
func NormalizeIPRules(raw string) (string, error) {
	items := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	if len(items) == 0 {
		return "", nil
	}
	if len(items) > 128 {
		return "", fmt.Errorf("at most 128 IP rules are allowed")
	}
	seen := make(map[string]struct{}, len(items))
	result := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		canonical := ""
		if strings.Contains(item, "/") {
			prefix, err := netip.ParsePrefix(item)
			if err != nil {
				return "", fmt.Errorf("invalid IP range %q", item)
			}
			canonical = prefix.Masked().String()
		} else {
			address, err := netip.ParseAddr(item)
			if err != nil {
				return "", fmt.Errorf("invalid IP address %q", item)
			}
			canonical = address.String()
		}
		if _, ok := seen[canonical]; ok {
			continue
		}
		seen[canonical] = struct{}{}
		result = append(result, canonical)
	}
	return strings.Join(result, ","), nil
}

// MatchIPRules returns whether the source IP appears in at least one rule.
// Invalid persisted data fails closed for an allow-list and fails safely for a
// deny-list by returning an error to the caller.
func MatchIPRules(ip, rules string) (bool, error) {
	if strings.TrimSpace(rules) == "" {
		return false, nil
	}
	address, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return false, fmt.Errorf("invalid client IP")
	}
	for _, item := range strings.Split(rules, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if strings.Contains(item, "/") {
			prefix, parseErr := netip.ParsePrefix(item)
			if parseErr != nil {
				return false, fmt.Errorf("invalid stored IP range")
			}
			if prefix.Contains(address) {
				return true, nil
			}
			continue
		}
		candidate, parseErr := netip.ParseAddr(item)
		if parseErr != nil {
			return false, fmt.Errorf("invalid stored IP address")
		}
		if candidate == address {
			return true, nil
		}
	}
	return false, nil
}
