package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"dengdeng/internal/config"
	"dengdeng/internal/model"
	"dengdeng/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type fakeRegistrationMailer struct {
	to   string
	code string
}

func (m *fakeRegistrationMailer) Configured() bool { return true }
func (m *fakeRegistrationMailer) SendRegistrationCode(to, code string) error {
	m.to, m.code = to, code
	return nil
}

func TestEmailVerifiedRegistration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:auth-email-test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.EmailVerification{}, &model.Setting{}, &model.ReferralCode{}, &model.ReferralBinding{}); err != nil {
		t.Fatal(err)
	}
	mailer := &fakeRegistrationMailer{}
	cfg := &config.Config{JWT: config.JWTConfig{Secret: "test-secret"}, Site: config.SiteConfig{AllowRegister: true}}
	h := NewAuthHandlerWithMailer(db, cfg, mailer)
	promoter := model.User{Email: "promoter@example.test", PasswordHash: "x", Role: model.RoleUser, Status: model.StatusActive}
	if err := db.Create(&promoter).Error; err != nil {
		t.Fatal(err)
	}
	referral := model.ReferralCode{Code: "DD-REGISTER", OwnerUserID: promoter.ID, CommissionBps: 500, Status: model.StatusActive}
	if err := db.Create(&referral).Error; err != nil {
		t.Fatal(err)
	}

	request := func(path, body string, handle gin.HandlerFunc) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
		c.Request.Header.Set("Content-Type", "application/json")
		handle(c)
		return w
	}

	if w := request("/api/auth/register/code", `{"email":"new@example.test"}`, h.SendRegistrationCode); w.Code != http.StatusOK {
		t.Fatalf("send code status = %d, body = %s", w.Code, w.Body.String())
	}
	if mailer.to != "new@example.test" || len(mailer.code) != 6 {
		t.Fatalf("mailer received to=%q code=%q", mailer.to, mailer.code)
	}

	settings, err := service.NewSystemSettingsService(db, cfg).Get()
	if err != nil {
		t.Fatal(err)
	}
	body := `{"email":"new@example.test","password":"password123","code":"` + mailer.code + `","referral_code":"DD-REGISTER","terms_revision":"` + settings.LoginAgreement.Revision() + `"}`
	if w := request("/api/auth/register", body, h.Register); w.Code != http.StatusOK {
		t.Fatalf("register status = %d, body = %s", w.Code, w.Body.String())
	}
	var user model.User
	if err := db.Where("email = ?", "new@example.test").First(&user).Error; err != nil {
		t.Fatal(err)
	}
	if !user.EmailVerified {
		t.Fatal("registered user should be email verified")
	}
	var binding model.ReferralBinding
	if err := db.Where("referred_user_id = ?", user.ID).First(&binding).Error; err != nil {
		t.Fatal("registration should bind the referral code:", err)
	}
	if binding.ReferrerUserID != promoter.ID || binding.ReferralCodeID != referral.ID {
		t.Fatalf("unexpected referral binding: %#v", binding)
	}
	if w := request("/api/auth/register", body, h.Register); w.Code != http.StatusBadRequest {
		t.Fatalf("reused code status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}
