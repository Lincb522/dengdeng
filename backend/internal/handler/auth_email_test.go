package handler

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	if err := db.AutoMigrate(&model.User{}, &model.EmailVerification{}, &model.RegistrationRiskEvent{}, &model.RegistrationBlock{}, &model.Setting{}, &model.ReferralCode{}, &model.ReferralBinding{}); err != nil {
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
	var riskEvent model.RegistrationRiskEvent
	if err := db.Where("action = ? AND email_domain = ?", registrationActionCreate, "example.test").First(&riskEvent).Error; err != nil {
		t.Fatal("successful registration should persist a risk event:", err)
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

func TestRegistrationRiskLimitSurvivesHandlerRestart(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:auth-registration-risk-test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.EmailVerification{}, &model.RegistrationRiskEvent{}, &model.RegistrationBlock{}, &model.Setting{}, &model.AuditLog{}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{JWT: config.JWTConfig{Secret: "test-secret"}, Site: config.SiteConfig{AllowRegister: true}}
	mailer := &fakeRegistrationMailer{}
	requestCode := func(h *AuthHandler, email string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/auth/register/code", bytes.NewBufferString(`{"email":"`+email+`"}`))
		c.Request.RemoteAddr = "198.51.100.25:32100"
		c.Request.Header.Set("Content-Type", "application/json")
		h.SendRegistrationCode(c)
		return w
	}
	first := NewAuthHandlerWithMailer(db, cfg, mailer)
	for i := 0; i < 3; i++ {
		if w := requestCode(first, fmt.Sprintf("risk-%d@example.test", i)); w.Code != http.StatusOK {
			t.Fatalf("code request %d status = %d, body = %s", i+1, w.Code, w.Body.String())
		}
	}
	restarted := NewAuthHandlerWithMailer(db, cfg, mailer)
	if w := requestCode(restarted, "risk-blocked@example.test"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("persisted limit status = %d, body = %s", w.Code, w.Body.String())
	}
	var block model.RegistrationBlock
	if err := db.Where("kind = ? AND value = ?", "ip", "198.51.100.25").First(&block).Error; err != nil {
		t.Fatal("threshold should create a registration block:", err)
	}
	if block.StrikeCount != 1 || !block.ExpiresAt.After(time.Now()) {
		t.Fatalf("unexpected registration block: %#v", block)
	}
	if err := db.Where("source_ip = ?", "198.51.100.25").Delete(&model.RegistrationRiskEvent{}).Error; err != nil {
		t.Fatal(err)
	}
	restartedAgain := NewAuthHandlerWithMailer(db, cfg, mailer)
	if w := requestCode(restartedAgain, "risk-still-blocked@example.test"); w.Code != http.StatusTooManyRequests || !strings.Contains(w.Body.String(), "auth.registration_temporarily_blocked") {
		t.Fatalf("active block after restart status = %d, body = %s", w.Code, w.Body.String())
	}
}
