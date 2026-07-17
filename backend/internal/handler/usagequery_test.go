package handler

import (
	"testing"
	"time"

	"dengdeng/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestQueryUsageFiltersByRequestID(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.UsageLog{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	logs := []model.UsageLog{
		{RequestID: "ddr_target", Model: "gpt-test", StatusCode: 503, CreatedAt: now},
		{RequestID: "ddr_other", Model: "gpt-test", StatusCode: 200, CreatedAt: now},
	}
	if err := db.Create(&logs).Error; err != nil {
		t.Fatal(err)
	}

	items, total, err := queryUsage(db, usageQuery{Page: 1, Size: 20, Sort: "created_at", Order: "desc", RequestID: "ddr_target"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 || items[0].RequestID != "ddr_target" {
		t.Fatalf("unexpected request-id result: total=%d items=%#v", total, items)
	}
}
