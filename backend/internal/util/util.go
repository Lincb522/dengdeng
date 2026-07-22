package util

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	return string(b), err
}

func CheckPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

const tokenAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// RandomToken returns a crypto-random string of n characters.
func RandomToken(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	for i, b := range buf {
		buf[i] = tokenAlphabet[int(b)%len(tokenAlphabet)]
	}
	return string(buf)
}

// NewAPIKey returns (plaintext, sha256hex, preview). Only the hash is stored.
func NewAPIKey() (string, string, string) {
	plain := "dd-" + RandomToken(48)
	return plain, HashAPIKey(plain), plain[:10] + "..." + plain[len(plain)-4:]
}

func HashAPIKey(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

type Claims struct {
	UserID      int64  `json:"uid"`
	Role        string `json:"role"`
	Ver         int    `json:"ver"`
	Fingerprint string `json:"fp,omitempty"`
	MFA         bool   `json:"mfa,omitempty"`
	jwt.RegisteredClaims
}

func SignJWT(secret string, userID int64, role string, ver int, ttl time.Duration) (string, error) {
	return SignJWTBound(secret, userID, role, ver, ttl, "", false)
}

func SignJWTBound(secret string, userID int64, role string, ver int, ttl time.Duration, fingerprint string, mfa bool) (string, error) {
	claims := Claims{
		UserID:      userID,
		Role:        role,
		Ver:         ver,
		Fingerprint: fingerprint,
		MFA:         mfa,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}

func SessionFingerprint(secret, userAgent string) string {
	sum := sha256.Sum256([]byte("dengdeng-session/v1|" + secret + "|" + strings.TrimSpace(userAgent)))
	return hex.EncodeToString(sum[:16])
}

func ParseJWT(secret, tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		return []byte(secret), nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return nil, err
	}
	return token.Claims.(*Claims), nil
}
