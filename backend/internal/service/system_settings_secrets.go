package service

import (
	"errors"
	"fmt"

	fieldcrypto "dengdeng/internal/crypto"
	"dengdeng/internal/model"

	"gorm.io/gorm"
)

const systemSecretPrefix = "system.secret.v1."

const (
	SecretSMTPPassword  = "smtp_password"
	SecretTurnstile     = "turnstile_secret"
	SecretLinuxDOOAuth  = "oauth_linuxdo_secret"
	SecretDingTalkOAuth = "oauth_dingtalk_secret"
	SecretWeChatOAuth   = "oauth_wechat_secret"
	SecretOIDCOAuth     = "oauth_oidc_secret"
	SecretGitHubOAuth   = "oauth_github_secret"
	SecretGoogleOAuth   = "oauth_google_secret"
)

var allowedSystemSecrets = map[string]struct{}{
	SecretSMTPPassword:  {},
	SecretTurnstile:     {},
	SecretLinuxDOOAuth:  {},
	SecretDingTalkOAuth: {},
	SecretWeChatOAuth:   {},
	SecretOIDCOAuth:     {},
	SecretGitHubOAuth:   {},
	SecretGoogleOAuth:   {},
}

type SystemSecretUpdate struct {
	Values map[string]string `json:"values"`
	Clear  []string          `json:"clear"`
}

func validateSystemSecretName(name string) error {
	if _, ok := allowedSystemSecrets[name]; !ok {
		return fmt.Errorf("unsupported system secret %q", name)
	}
	return nil
}

func (s *SystemSettingsService) UpdateSecrets(update SystemSecretUpdate) error {
	if len(update.Values) > len(allowedSystemSecrets) || len(update.Clear) > len(allowedSystemSecrets) {
		return errors.New("too many system secrets")
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		return s.updateSecretsTx(tx, update)
	})
}

func (s *SystemSettingsService) updateSecretsTx(tx *gorm.DB, update SystemSecretUpdate) error {
	if len(update.Values) > len(allowedSystemSecrets) || len(update.Clear) > len(allowedSystemSecrets) {
		return errors.New("too many system secrets")
	}
	for name, plain := range update.Values {
		if err := validateSystemSecretName(name); err != nil {
			return err
		}
		if plain == "" {
			continue // Empty form fields preserve the configured value.
		}
		if len(plain) > 16_384 {
			return fmt.Errorf("system secret %q is too large", name)
		}
		ciphertext, err := fieldcrypto.Encrypt(plain)
		if err != nil {
			return err
		}
		if err := tx.Save(&model.Setting{Key: systemSecretPrefix + name, Value: ciphertext}).Error; err != nil {
			return err
		}
	}
	for _, name := range update.Clear {
		if err := validateSystemSecretName(name); err != nil {
			return err
		}
		if err := tx.Where("key = ?", systemSecretPrefix+name).Delete(&model.Setting{}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *SystemSettingsService) Secret(name string) (string, error) {
	if err := validateSystemSecretName(name); err != nil {
		return "", err
	}
	var record model.Setting
	err := s.db.First(&record, "key = ?", systemSecretPrefix+name).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return s.environmentSecret(name), nil
	}
	if err != nil {
		return "", err
	}
	return fieldcrypto.Decrypt(record.Value)
}

func (s *SystemSettingsService) SecretConfigured() map[string]bool {
	configured := make(map[string]bool, len(allowedSystemSecrets))
	var records []model.Setting
	_ = s.db.Where("key LIKE ?", systemSecretPrefix+"%").Find(&records).Error
	for _, record := range records {
		name := record.Key[len(systemSecretPrefix):]
		if _, ok := allowedSystemSecrets[name]; ok && record.Value != "" {
			configured[name] = true
		}
	}
	for name := range allowedSystemSecrets {
		if !configured[name] && s.environmentSecret(name) != "" {
			configured[name] = true
		}
	}
	return configured
}

func (s *SystemSettingsService) environmentSecret(name string) string {
	if s == nil || s.cfg == nil {
		return ""
	}
	switch name {
	case SecretSMTPPassword:
		return s.cfg.SMTP.Pass
	default:
		return ""
	}
}
