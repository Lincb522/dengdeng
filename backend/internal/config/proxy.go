package config

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

const (
	upstreamMaxIdleConns        = 256
	upstreamMaxIdleConnsPerHost = 64
	upstreamMaxConnsPerHost     = 128
	upstreamIdleConnTimeout     = 2 * time.Minute
)

// HTTPClient returns a client for outbound provider traffic. Proxy URL may be
// an http:// or https:// CONNECT proxy. NoProxy accepts comma-separated
// hostnames, domain suffixes, IPs, CIDRs, and *.
func (p ProxyConfig) HTTPClient(timeout time.Duration) (*http.Client, error) {
	return NewProxyHTTPClient(p.URL, p.NoProxy, timeout)
}

// NewProxyHTTPClient creates a client for either the deployment-wide route or
// an account-specific proxy. SOCKS5 is supported alongside HTTP CONNECT so
// the independently managed proxy inventory has the same useful protocols as
// mature gateway panels.
func NewProxyHTTPClient(proxyRaw, noProxy string, timeout time.Duration) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	tuneUpstreamTransport(transport)
	if strings.TrimSpace(proxyRaw) == "" {
		return &http.Client{Transport: transport, Timeout: timeout}, nil
	}
	proxyURL, err := url.Parse(proxyRaw)
	if err != nil || proxyURL.Scheme == "" || proxyURL.Host == "" {
		return nil, fmt.Errorf("invalid proxy.url: must be an http(s) proxy URL")
	}
	scheme := strings.ToLower(proxyURL.Scheme)
	if scheme != "http" && scheme != "https" && scheme != "socks5" {
		return nil, fmt.Errorf("unsupported proxy.url scheme %q", proxyURL.Scheme)
	}
	if scheme == "socks5" {
		var auth *proxy.Auth
		if proxyURL.User != nil {
			password, _ := proxyURL.User.Password()
			auth = &proxy.Auth{User: proxyURL.User.Username(), Password: password}
		}
		dialer, err := proxy.SOCKS5("tcp", proxyURL.Host, auth, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("create SOCKS5 proxy client: %w", err)
		}
		direct := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		transport.Proxy = nil
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			host, _, splitErr := net.SplitHostPort(address)
			if splitErr == nil && bypassProxy(host, noProxy) {
				return direct.DialContext(ctx, network, address)
			}
			return dialer.Dial(network, address)
		}
		return &http.Client{Transport: transport, Timeout: timeout}, nil
	}
	transport.Proxy = func(req *http.Request) (*url.URL, error) {
		if bypassProxy(req.URL.Hostname(), noProxy) {
			return nil, nil
		}
		return proxyURL, nil
	}
	return &http.Client{Transport: transport, Timeout: timeout}, nil
}

// tuneUpstreamTransport keeps streaming requests free of a global deadline
// while retaining enough warm HTTP/2 and keep-alive connections for bursty
// gateway traffic. Go's default per-host idle pool is only two connections,
// which otherwise causes avoidable TCP/TLS handshakes between requests.
func tuneUpstreamTransport(transport *http.Transport) {
	if transport == nil {
		return
	}
	transport.MaxIdleConns = upstreamMaxIdleConns
	transport.MaxIdleConnsPerHost = upstreamMaxIdleConnsPerHost
	transport.MaxConnsPerHost = upstreamMaxConnsPerHost
	transport.IdleConnTimeout = upstreamIdleConnTimeout
	transport.ForceAttemptHTTP2 = true
}

func bypassProxy(host, noProxy string) bool {
	host = strings.Trim(strings.ToLower(host), "[]")
	for _, raw := range strings.Split(noProxy, ",") {
		entry := strings.TrimSpace(strings.ToLower(raw))
		if entry == "" {
			continue
		}
		if entry == "*" {
			return true
		}
		if _, cidr, err := net.ParseCIDR(entry); err == nil {
			if ip := net.ParseIP(host); ip != nil && cidr.Contains(ip) {
				return true
			}
			continue
		}
		entry = strings.TrimPrefix(entry, ".")
		if host == entry || strings.HasSuffix(host, "."+entry) {
			return true
		}
	}
	return false
}
