package handler

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dengdeng/internal/model"
	"dengdeng/internal/service"

	"github.com/glebarez/sqlite"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

func TestValidateOIDCIDTokenChecksSignatureAudienceIssuerAndNonce(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	encodeInt := func(value *big.Int) string { return base64.RawURLEncoding.EncodeToString(value.Bytes()) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{
			"kid": "test-key", "kty": "RSA", "alg": "RS256", "use": "sig",
			"n": encodeInt(privateKey.PublicKey.N), "e": encodeInt(big.NewInt(int64(privateKey.PublicKey.E))),
		}}})
	}))
	defer server.Close()

	provider := service.OAuthProviderSettings{ClientID: "client-1", IssuerURL: "https://issuer.example", JWKSURL: server.URL, AllowedSigningAlgs: "RS256", ClockSkewSeconds: 30}
	claims := jwt.MapClaims{"iss": provider.IssuerURL, "aud": provider.ClientID, "sub": "user-1", "nonce": "nonce-1", "exp": time.Now().Add(time.Minute).Unix(), "iat": time.Now().Unix()}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "test-key"
	raw, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	validated, err := validateOIDCIDToken(provider, raw, "nonce-1")
	if err != nil || validated["sub"] != "user-1" {
		t.Fatalf("expected valid ID token, claims=%v err=%v", validated, err)
	}
	if _, err := validateOIDCIDToken(provider, raw, "wrong-nonce"); err == nil {
		t.Fatal("expected nonce mismatch")
	}
}

func TestOAuthFlowIsSharedAndSingleUse(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.UserOAuthFlow{}); err != nil {
		t.Fatal(err)
	}
	first := &AuthHandler{db: db}
	second := &AuthHandler{db: db}
	if err := first.storeOAuthFlow("raw-state", model.UserOAuthFlow{Kind: oauthFlowState, Provider: "oidc", ExpiresAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	flow, err := second.consumeOAuthFlow(oauthFlowState, "raw-state")
	if err != nil || flow.Provider != "oidc" {
		t.Fatalf("expected another instance to consume flow, flow=%+v err=%v", flow, err)
	}
	if _, err := first.consumeOAuthFlow(oauthFlowState, "raw-state"); err == nil {
		t.Fatal("expected OAuth state to be single-use")
	}
}
