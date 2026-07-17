// Package crypto provides at-rest encryption for sensitive fields (upstream
// API keys / tokens). It uses AES-256-GCM with a process-wide key derived
// from configuration, exposed as an EncryptedString type that transparently
// encrypts on write and decrypts on read via database/sql.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql/driver"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
)

const cipherPrefix = "enc:v1:"

var (
	mu    sync.RWMutex
	aead  cipher.AEAD
	ready bool
)

// Init sets the master key. Prefer a dedicated 32-byte hex key (ENCRYPTION_KEY);
// otherwise a key is derived from the given fallback (JWT secret) so the
// feature works with zero extra configuration. Returns an error only on a
// malformed explicit hex key.
func Init(explicitHexKey, fallback string) error {
	var key []byte
	if explicitHexKey != "" {
		k, err := hex.DecodeString(explicitHexKey)
		if err != nil || len(k) != 32 {
			return errors.New("ENCRYPTION_KEY must be 64 hex chars (32 bytes)")
		}
		key = k
	} else {
		sum := sha256.Sum256([]byte("dengdeng-field-encryption/v1|" + fallback))
		key = sum[:]
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	mu.Lock()
	aead = gcm
	ready = true
	mu.Unlock()
	return nil
}

// Encrypt returns a prefixed base64 ciphertext. Empty input stays empty.
func Encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	mu.RLock()
	gcm, ok := aead, ready
	mu.RUnlock()
	if !ok {
		return "", errors.New("crypto not initialized")
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return cipherPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt reverses Encrypt. Values without the prefix are treated as legacy
// plaintext and returned unchanged, so existing rows keep working and get
// upgraded to ciphertext on their next write.
func Decrypt(stored string) (string, error) {
	if stored == "" {
		return "", nil
	}
	if len(stored) < len(cipherPrefix) || stored[:len(cipherPrefix)] != cipherPrefix {
		return stored, nil
	}
	mu.RLock()
	gcm, ok := aead, ready
	mu.RUnlock()
	if !ok {
		return "", errors.New("crypto not initialized")
	}
	raw, err := base64.StdEncoding.DecodeString(stored[len(cipherPrefix):])
	if err != nil {
		return "", fmt.Errorf("decode ciphertext: %w", err)
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	nonce, body := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, body, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt failed (wrong key?): %w", err)
	}
	return string(plain), nil
}

// EncryptedString is stored encrypted at rest but used as plaintext in Go.
// It implements database/sql Scanner and Valuer so GORM handles it
// transparently. JSON marshaling is intentionally not customized; sensitive
// fields should carry json:"-".
type EncryptedString string

func (s EncryptedString) Value() (driver.Value, error) {
	return Encrypt(string(s))
}

func (s *EncryptedString) Scan(src any) error {
	if src == nil {
		*s = ""
		return nil
	}
	var stored string
	switch v := src.(type) {
	case string:
		stored = v
	case []byte:
		stored = string(v)
	default:
		return fmt.Errorf("EncryptedString: unsupported scan type %T", src)
	}
	plain, err := Decrypt(stored)
	if err != nil {
		return err
	}
	*s = EncryptedString(plain)
	return nil
}
