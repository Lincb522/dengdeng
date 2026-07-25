package handler

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"dengdeng/internal/model"
	"dengdeng/internal/service"

	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	oauthFlowState  = "state"
	oauthFlowResult = "result"
)

func oauthTokenHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (h *AuthHandler) storeOAuthFlow(rawToken string, flow model.UserOAuthFlow) error {
	if h == nil || h.db == nil || strings.TrimSpace(rawToken) == "" {
		return errors.New("OAuth flow store is unavailable")
	}
	flow.TokenHash = oauthTokenHash(rawToken)
	if flow.ExpiresAt.IsZero() {
		flow.ExpiresAt = time.Now().UTC().Add(10 * time.Minute)
	}
	return h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("expires_at < ?", time.Now().UTC()).Delete(&model.UserOAuthFlow{}).Error; err != nil {
			return err
		}
		return tx.Create(&flow).Error
	})
}

func (h *AuthHandler) loadOAuthFlow(kind, rawToken string) (model.UserOAuthFlow, error) {
	var flow model.UserOAuthFlow
	if h == nil || h.db == nil || strings.TrimSpace(rawToken) == "" {
		return flow, gorm.ErrRecordNotFound
	}
	err := h.db.Where("token_hash = ? AND kind = ? AND expires_at > ?", oauthTokenHash(rawToken), kind, time.Now().UTC()).First(&flow).Error
	return flow, err
}

func (h *AuthHandler) consumeOAuthFlow(kind, rawToken string) (model.UserOAuthFlow, error) {
	var flow model.UserOAuthFlow
	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"token_hash = ? AND kind = ? AND expires_at > ?", oauthTokenHash(rawToken), kind, time.Now().UTC(),
		).First(&flow).Error; err != nil {
			return err
		}
		result := tx.Where("id = ?", flow.ID).Delete(&model.UserOAuthFlow{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
	return flow, err
}

func (h *AuthHandler) deleteOAuthFlow(id int64) bool {
	return h != nil && h.db != nil && h.db.Where("id = ?", id).Delete(&model.UserOAuthFlow{}).RowsAffected == 1
}

type oidcJWKSet struct {
	Keys []struct {
		KID string `json:"kid"`
		KTY string `json:"kty"`
		ALG string `json:"alg"`
		Use string `json:"use"`
		N   string `json:"n"`
		E   string `json:"e"`
		CRV string `json:"crv"`
		X   string `json:"x"`
		Y   string `json:"y"`
	} `json:"keys"`
}

func oidcAllowedAlgorithms(raw string) []string {
	allowed := make([]string, 0, 4)
	for _, value := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' }) {
		value = strings.TrimSpace(value)
		switch value {
		case "RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "ES256", "ES384", "ES512":
			allowed = append(allowed, value)
		}
	}
	if len(allowed) == 0 {
		return []string{"RS256", "ES256", "PS256"}
	}
	return allowed
}

func fetchOIDCJWKSet(url string) (oidcJWKSet, error) {
	var set oidcJWKSet
	if strings.TrimSpace(url) == "" {
		return set, errors.New("OIDC JWKS URL is required for ID Token validation")
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return set, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 12 * time.Second}).Do(req)
	if err != nil {
		return set, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return set, fmt.Errorf("OIDC JWKS endpoint returned %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 512<<10)).Decode(&set); err != nil || len(set.Keys) == 0 {
		return set, errors.New("OIDC JWKS response is invalid")
	}
	return set, nil
}

func decodeJWKInteger(value string) (*big.Int, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) == 0 {
		return nil, errors.New("invalid JWK integer")
	}
	return new(big.Int).SetBytes(raw), nil
}

func oidcVerificationKey(set oidcJWKSet, kid, alg string) (any, error) {
	for _, key := range set.Keys {
		if kid != "" && key.KID != kid {
			continue
		}
		if key.Use != "" && key.Use != "sig" {
			continue
		}
		if key.ALG != "" && key.ALG != alg {
			continue
		}
		switch key.KTY {
		case "RSA":
			n, err := decodeJWKInteger(key.N)
			if err != nil {
				continue
			}
			e, err := decodeJWKInteger(key.E)
			if err != nil || !e.IsInt64() || e.Int64() < 3 {
				continue
			}
			return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
		case "EC":
			var curve elliptic.Curve
			switch key.CRV {
			case "P-256":
				curve = elliptic.P256()
			case "P-384":
				curve = elliptic.P384()
			case "P-521":
				curve = elliptic.P521()
			}
			x, xErr := decodeJWKInteger(key.X)
			y, yErr := decodeJWKInteger(key.Y)
			if curve != nil && xErr == nil && yErr == nil && curve.IsOnCurve(x, y) {
				return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
			}
		}
	}
	return nil, errors.New("no matching OIDC signing key")
}

func validateOIDCIDToken(provider service.OAuthProviderSettings, rawToken, expectedNonce string) (map[string]any, error) {
	if strings.TrimSpace(rawToken) == "" {
		return nil, errors.New("OIDC token response has no ID Token")
	}
	set, err := fetchOIDCJWKSet(provider.JWKSURL)
	if err != nil {
		return nil, err
	}
	claims := jwt.MapClaims{}
	options := []jwt.ParserOption{
		jwt.WithValidMethods(oidcAllowedAlgorithms(provider.AllowedSigningAlgs)),
		jwt.WithAudience(provider.ClientID),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(time.Duration(provider.ClockSkewSeconds) * time.Second),
	}
	if issuer := strings.TrimRight(strings.TrimSpace(provider.IssuerURL), "/"); issuer != "" {
		options = append(options, jwt.WithIssuer(issuer))
	}
	token, err := jwt.ParseWithClaims(rawToken, claims, func(token *jwt.Token) (any, error) {
		kid, _ := token.Header["kid"].(string)
		return oidcVerificationKey(set, kid, token.Method.Alg())
	}, options...)
	if err != nil || token == nil || !token.Valid {
		return nil, fmt.Errorf("OIDC ID Token validation failed: %w", err)
	}
	if expectedNonce != "" {
		nonce, _ := claims["nonce"].(string)
		if nonce == "" || nonce != expectedNonce {
			return nil, errors.New("OIDC ID Token nonce mismatch")
		}
	}
	return map[string]any(claims), nil
}
