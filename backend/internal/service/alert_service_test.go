package service

import (
	"testing"
	"time"

	"dengdeng/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestAlertServiceOpensRefreshesAndResolvesIncident(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Group{}, &model.UpstreamAccount{}, &model.AlertRule{}, &model.AlertEvent{}); err != nil {
		t.Fatal(err)
	}
	group := model.Group{Name: "alerts-openai", Platform: model.PlatformOpenAI, Status: model.StatusActive, RateMultiplier: 1}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	account := model.UpstreamAccount{GroupID: group.ID, Name: "account-a", Platform: model.PlatformOpenAI, Status: model.StatusActive}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.AlertRule{Name: "down only", Enabled: true, Condition: "down"}).Error; err != nil {
		t.Fatal(err)
	}
	svc := NewAlertService(db, nil, "")
	now := time.Now().UTC()
	svc.EvaluateProbe(model.AccountProbe{AccountID: account.ID, State: "down", CheckedAt: now, ErrorMessage: "unreachable"})
	svc.EvaluateProbe(model.AccountProbe{AccountID: account.ID, State: "down", CheckedAt: now.Add(time.Minute), ErrorMessage: "still unreachable"})
	var rule model.AlertRule
	if err := db.Where("name = ?", "down only").First(&rule).Error; err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&model.AlertEvent{}).Where("state = ? AND rule_id = ?", "open", rule.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("open incidents=%d err=%v", count, err)
	}
	svc.EvaluateProbe(model.AccountProbe{AccountID: account.ID, State: "healthy", CheckedAt: now.Add(2 * time.Minute)})
	var event model.AlertEvent
	if err := db.Where("rule_id = ?", rule.ID).First(&event).Error; err != nil {
		t.Fatal(err)
	}
	if event.State != "resolved" || event.ResolvedAt == nil {
		t.Fatalf("event not resolved: %#v", event)
	}
}
