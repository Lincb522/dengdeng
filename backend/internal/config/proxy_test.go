package config

import (
	"net/http"
	"testing"
)

func TestNewProxyHTTPClientTunesConnectionPool(t *testing.T) {
	client, err := NewProxyHTTPClient("", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T", client.Transport)
	}
	if transport.MaxIdleConns != upstreamMaxIdleConns ||
		transport.MaxIdleConnsPerHost != upstreamMaxIdleConnsPerHost ||
		transport.MaxConnsPerHost != upstreamMaxConnsPerHost ||
		transport.IdleConnTimeout != upstreamIdleConnTimeout ||
		!transport.ForceAttemptHTTP2 {
		t.Fatalf("transport was not tuned: %#v", transport)
	}
}
