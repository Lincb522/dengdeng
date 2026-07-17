package model

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// URL returns a transport-ready proxy URL without ever exposing credentials
// through the JSON API. HTTP(S) and SOCKS5 are the protocols supported by the
// gateway transport.
func (p Proxy) URL() (string, error) {
	protocol := strings.ToLower(strings.TrimSpace(p.Protocol))
	if protocol != "http" && protocol != "https" && protocol != "socks5" {
		return "", fmt.Errorf("unsupported proxy protocol %q", p.Protocol)
	}
	host := strings.TrimSpace(p.Host)
	if host == "" || strings.Contains(host, "://") || strings.ContainsAny(host, "/?#@") {
		return "", fmt.Errorf("invalid proxy host")
	}
	if p.Port < 1 || p.Port > 65535 {
		return "", fmt.Errorf("proxy port must be between 1 and 65535")
	}

	u := &url.URL{Scheme: protocol, Host: net.JoinHostPort(host, fmt.Sprintf("%d", p.Port))}
	if p.Username != "" {
		u.User = url.UserPassword(string(p.Username), string(p.Password))
	}
	return u.String(), nil
}
