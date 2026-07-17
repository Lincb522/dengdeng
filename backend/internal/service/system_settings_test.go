package service

import (
	"testing"

	"dengdeng/internal/config"
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
	if !settings.LoginAgreement.Enabled || len(settings.LoginAgreement.Documents) < 5 {
		t.Fatalf("expected enabled default agreement documents, got %#v", settings.LoginAgreement)
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
}
