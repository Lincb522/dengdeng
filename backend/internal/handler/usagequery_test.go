package handler

import (
	"encoding/csv"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dengdeng/internal/model"

	"github.com/gin-gonic/gin"
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

func TestUsageCSVIncludesFirstTokenAndTotalDuration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.APIKey{}, &model.Group{}, &model.UpstreamAccount{}, &model.UsageLog{}); err != nil {
		t.Fatal(err)
	}
	entry := model.UsageLog{
		RequestID: "ddr_latency", Model: "gpt-test", FirstTokenMs: 123, DurationMs: 456,
		StatusCode: 200, CreatedAt: time.Now().UTC(),
	}
	if err := db.Create(&entry).Error; err != nil {
		t.Fatal(err)
	}

	for _, includeInternal := range []bool{false, true} {
		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		filter := usageQuery{Page: 1, Size: 20, Sort: "created_at", Order: "desc"}
		if err := writeUsageCSV(context, db, filter, nil, includeInternal); err != nil {
			t.Fatal(err)
		}
		records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(recorder.Body.String(), "\uFEFF"))).ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		if len(records) != 2 || len(records[0]) != len(records[1]) {
			t.Fatalf("invalid CSV shape: %#v", records)
		}
		index := map[string]int{}
		for column, label := range records[0] {
			index[label] = column
		}
		if records[1][index["首字耗时(ms)"]] != "123" || records[1][index["总耗时(ms)"]] != "456" {
			t.Fatalf("missing latency values in CSV: %#v", records)
		}
	}
}
