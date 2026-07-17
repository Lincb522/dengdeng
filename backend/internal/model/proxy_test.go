package model

import (
	"strings"
	"testing"

	"dengdeng/internal/crypto"
)

func TestProxyURLBuildsEscapedCredentialURL(t *testing.T) {
	p := Proxy{Protocol: "socks5", Host: "proxy.example.test", Port: 1080, Username: crypto.EncryptedString("user@name"), Password: crypto.EncryptedString("p@ss word")}
	got, err := p.URL()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "socks5://user%40name:p%40ss%20word@proxy.example.test:1080") {
		t.Fatalf("unexpected proxy URL: %s", got)
	}
}
