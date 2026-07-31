package service

import (
	"encoding/json"
	"strings"
	"testing"

	"dengdeng/internal/config"
	fieldcrypto "dengdeng/internal/crypto"
	"dengdeng/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestSystemSettingsAgreementRevisionChangesWithDocument(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:system-settings-test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Setting{}); err != nil {
		t.Fatal(err)
	}
	svc := NewSystemSettingsService(db, &config.Config{Site: config.SiteConfig{Name: "DengDeng", AllowRegister: true}})
	settings, err := svc.Get()
	if err != nil {
		t.Fatal(err)
	}
	if !settings.LoginAgreement.Enabled || len(settings.LoginAgreement.Documents) < 6 {
		t.Fatalf("expected enabled default agreement documents, got %#v", settings.LoginAgreement)
	}
	if settings.LoginAgreement.UpdatedAt != defaultAgreementUpdatedAt {
		t.Fatalf("unexpected default agreement date: %q", settings.LoginAgreement.UpdatedAt)
	}
	for _, document := range settings.LoginAgreement.Documents {
		if strings.Contains(document.ContentMD, "#") {
			t.Fatalf("default legal document %q contains raw markdown heading markers", document.ID)
		}
		if strings.TrimSpace(document.ContentMD) == "" {
			t.Fatalf("default legal document %q is empty", document.ID)
		}
	}
	before := settings.LoginAgreement.Revision()
	settings.LoginAgreement.Documents[0].ContentMD += "\n\n补充说明。"
	if _, err := svc.Update(settings); err != nil {
		t.Fatal(err)
	}
	after, err := svc.Get()
	if err != nil {
		t.Fatal(err)
	}
	if after.LoginAgreement.Revision() == before {
		t.Fatal("agreement revision did not change after document update")
	}
}

func TestSystemSettingsExtendedDefaultsAndPersistence(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:system-settings-extended-test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Setting{}); err != nil {
		t.Fatal(err)
	}
	svc := NewSystemSettingsService(db, &config.Config{Site: config.SiteConfig{Name: "DengDeng", AllowRegister: true}})
	settings, err := svc.Get()
	if err != nil {
		t.Fatal(err)
	}
	if !settings.Features.ChannelMonitorEnabled || !settings.Security.EmailVerificationEnabled || settings.SiteCustomization.TableDefaultPageSize != 20 {
		t.Fatalf("unexpected extended defaults: %#v", settings)
	}
	settings.SiteCustomization.ContactInfo = "QQ群 1072353908"
	settings.Features.RiskControlEnabled = true
	settings.UserDefaults.Concurrency = 12
	settings.UserDefaults.PlatformQuotas["openai"] = PlatformQuotaSetting{DailyMicro: 5_000_000}
	settings.Notifications.AccountQuotaEmails = []string{"OPS@EXAMPLE.COM", "ops@example.com"}
	updated, err := svc.Update(settings)
	if err != nil {
		t.Fatal(err)
	}
	if updated.UserDefaults.Concurrency != 12 || updated.UserDefaults.PlatformQuotas["openai"].DailyMicro != 5_000_000 {
		t.Fatalf("extended settings were not persisted: %#v", updated.UserDefaults)
	}
	if len(updated.Notifications.AccountQuotaEmails) != 1 || updated.Notifications.AccountQuotaEmails[0] != "ops@example.com" {
		t.Fatalf("notification emails were not normalized: %#v", updated.Notifications.AccountQuotaEmails)
	}
}

func TestSystemSettingsMigratesLegacyInitialBalanceIntoUserDefaults(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:system-settings-balance-migration-test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Setting{}); err != nil {
		t.Fatal(err)
	}
	svc := NewSystemSettingsService(db, &config.Config{Site: config.SiteConfig{Name: "DengDeng", AllowRegister: true}})
	settings := svc.defaults()
	settings.InitBalanceMicro = 2_000_000
	settings.UserDefaults.BalanceMicro = 0
	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Setting{Key: systemSettingsKey, Value: string(raw)}).Error; err != nil {
		t.Fatal(err)
	}

	loaded, err := svc.Get()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.InitBalanceMicro != 2_000_000 || loaded.UserDefaults.BalanceMicro != 2_000_000 {
		t.Fatalf("legacy initial balance was not migrated: legacy=%d defaults=%d", loaded.InitBalanceMicro, loaded.UserDefaults.BalanceMicro)
	}
}

func TestSystemSettingsSecretsAreEncryptedAndClearable(t *testing.T) {
	if err := fieldcrypto.Init("", "system-settings-secret-test"); err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(sqlite.Open("file:system-settings-secret-test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Setting{}); err != nil {
		t.Fatal(err)
	}
	svc := NewSystemSettingsService(db, &config.Config{})
	if err := svc.UpdateSecrets(SystemSecretUpdate{Values: map[string]string{SecretSMTPPassword: "mail-secret"}}); err != nil {
		t.Fatal(err)
	}
	var stored model.Setting
	if err := db.First(&stored, "key = ?", systemSecretPrefix+SecretSMTPPassword).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Value == "mail-secret" || !strings.HasPrefix(stored.Value, "enc:v1:") {
		t.Fatalf("secret was not encrypted at rest: %q", stored.Value)
	}
	plain, err := svc.Secret(SecretSMTPPassword)
	if err != nil || plain != "mail-secret" {
		t.Fatalf("secret decrypt failed: value=%q err=%v", plain, err)
	}
	if !svc.SecretConfigured()[SecretSMTPPassword] {
		t.Fatal("configured secret flag was false")
	}
	if err := svc.UpdateSecrets(SystemSecretUpdate{Clear: []string{SecretSMTPPassword}}); err != nil {
		t.Fatal(err)
	}
	if svc.SecretConfigured()[SecretSMTPPassword] {
		t.Fatal("cleared secret still reported configured")
	}
}

func TestSystemSettingsRegistrationEmailSuffixes(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:system-settings-suffix-test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Setting{}); err != nil {
		t.Fatal(err)
	}
	svc := NewSystemSettingsService(db, &config.Config{Site: config.SiteConfig{Name: "DengDeng", AllowRegister: true}})
	settings, err := svc.Get()
	if err != nil {
		t.Fatal(err)
	}
	settings.RegistrationEmailSuffixes = []string{"Example.COM", "team.example.cn"}
	settings.RegistrationEmailBlockedSuffixes = []string{"blocked.example.com", "Disposable.Example"}
	updated, err := svc.Update(settings)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.AllowsRegistrationEmail("member@sub.example.com") || !updated.AllowsRegistrationEmail("staff@team.example.cn") {
		t.Fatalf("expected permitted suffixes: %#v", updated.RegistrationEmailSuffixes)
	}
	if updated.AllowsRegistrationEmail("person@other.example") {
		t.Fatal("unexpected domain allowance")
	}
	if updated.AllowsRegistrationEmail("person@blocked.example.com") || updated.AllowsRegistrationEmail("person@sub.disposable.example") {
		t.Fatal("blocked domain should not be allowed")
	}
}
