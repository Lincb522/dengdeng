package service

import (
	"errors"
	"testing"
	"time"

	"dengdeng/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newSchedulerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.UpstreamAccount{}, &model.Proxy{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestSchedulerUsesSnapshotUntilInvalidated(t *testing.T) {
	db := newSchedulerTestDB(t)
	first := model.UpstreamAccount{GroupID: 1, Name: "first", Platform: model.PlatformOpenAI, AuthType: model.AuthAPIKey, Priority: 10, Status: model.StatusActive}
	if err := db.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	scheduler := NewScheduler(db)
	scheduler.lastPersistedInterval = time.Hour
	scheduler.lastPersisted[first.ID] = time.Now()
	if got, err := scheduler.Pick(1, nil); err != nil || got.ID != first.ID {
		t.Fatalf("first pick = %#v, %v", got, err)
	}

	second := model.UpstreamAccount{GroupID: 1, Name: "second", Platform: model.PlatformOpenAI, AuthType: model.AuthAPIKey, Priority: 100, Status: model.StatusActive}
	if err := db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	scheduler.lastPersisted[second.ID] = time.Now()
	if got, err := scheduler.Pick(1, nil); err != nil || got.ID != first.ID {
		t.Fatalf("cached pick = %#v, %v", got, err)
	}
	scheduler.InvalidateGroup(1)
	if got, err := scheduler.Pick(1, nil); err != nil || got.ID != second.ID {
		t.Fatalf("invalidated pick = %#v, %v", got, err)
	}
}

func TestSchedulerFailureUpdatesSnapshotImmediately(t *testing.T) {
	db := newSchedulerTestDB(t)
	accounts := []model.UpstreamAccount{
		{GroupID: 1, Name: "one", Platform: model.PlatformOpenAI, AuthType: model.AuthAPIKey, Priority: 10, Status: model.StatusActive},
		{GroupID: 1, Name: "two", Platform: model.PlatformOpenAI, AuthType: model.AuthAPIKey, Priority: 10, Status: model.StatusActive},
	}
	if err := db.Create(&accounts).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	scheduler := NewScheduler(db)
	scheduler.now = func() time.Time { return now }
	for i := range accounts {
		scheduler.lastPersisted[accounts[i].ID] = now
	}
	first, err := scheduler.Pick(1, nil)
	if err != nil {
		t.Fatal(err)
	}
	scheduler.ReportFailure(first.ID, 429, "rate limited")
	next, err := scheduler.Pick(1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if next.ID == first.ID {
		t.Fatalf("cooling account %d was selected again", first.ID)
	}
}

func TestSchedulerReturnsNoAccountForEmptySnapshot(t *testing.T) {
	scheduler := NewScheduler(newSchedulerTestDB(t))
	_, err := scheduler.Pick(99, nil)
	if !errors.Is(err, ErrNoAccount) {
		t.Fatalf("error = %v", err)
	}
}

func TestSchedulerSessionAffinityKeepsConversationOnAccount(t *testing.T) {
	db := newSchedulerTestDB(t)
	accounts := []model.UpstreamAccount{
		{GroupID: 1, Name: "one", Platform: model.PlatformOpenAI, AuthType: model.AuthAPIKey, Priority: 10, Status: model.StatusActive},
		{GroupID: 1, Name: "two", Platform: model.PlatformOpenAI, AuthType: model.AuthAPIKey, Priority: 10, Status: model.StatusActive},
	}
	if err := db.Create(&accounts).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	scheduler := NewScheduler(db)
	scheduler.now = func() time.Time { return now }
	for i := range accounts {
		scheduler.lastPersisted[accounts[i].ID] = now
	}
	first, err := scheduler.PickForSession(1, "gpt-5.6", "key-1:session-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if _, err := scheduler.PickForSession(1, "gpt-5.6", "key-1:session-b", nil); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	sticky, err := scheduler.PickForSession(1, "gpt-5.6", "key-1:session-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	if sticky.ID != first.ID {
		t.Fatalf("session moved from account %d to %d", first.ID, sticky.ID)
	}
}

func TestSchedulerModelFailureDoesNotBlockOtherModels(t *testing.T) {
	db := newSchedulerTestDB(t)
	account := model.UpstreamAccount{GroupID: 1, Name: "one", Platform: model.PlatformOpenAI, AuthType: model.AuthAPIKey, Priority: 10, Status: model.StatusActive}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	scheduler := NewScheduler(db)
	scheduler.now = func() time.Time { return now }
	scheduler.lastPersisted[account.ID] = now
	if _, err := scheduler.PickForSession(1, "gpt-a", "", nil); err != nil {
		t.Fatal(err)
	}
	scheduler.ReportFailureForModel(account.ID, "gpt-a", 500, "model transient")
	if _, err := scheduler.PickForSession(1, "gpt-a", "", nil); !errors.Is(err, ErrNoAccount) {
		t.Fatalf("failed model error = %v", err)
	}
	if got, err := scheduler.PickForSession(1, "gpt-b", "", nil); err != nil || got.ID != account.ID {
		t.Fatalf("other model pick = %#v, %v", got, err)
	}
}

func TestSchedulerAuthFailureBlocksAccountAcrossModels(t *testing.T) {
	db := newSchedulerTestDB(t)
	account := model.UpstreamAccount{GroupID: 1, Name: "one", Platform: model.PlatformOpenAI, AuthType: model.AuthAPIKey, Priority: 10, Status: model.StatusActive}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	scheduler := NewScheduler(db)
	scheduler.now = func() time.Time { return now }
	scheduler.lastPersisted[account.ID] = now
	if _, err := scheduler.PickForSession(1, "gpt-a", "", nil); err != nil {
		t.Fatal(err)
	}
	scheduler.ReportFailureForModel(account.ID, "gpt-a", 401, "invalid token")
	if _, err := scheduler.PickForSession(1, "gpt-b", "", nil); !errors.Is(err, ErrNoAccount) {
		t.Fatalf("other model error = %v", err)
	}
}
