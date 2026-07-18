package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"dengdeng/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestSchedulerEnforcesAccountConcurrencyAndWakesWaiter(t *testing.T) {
	db := newSchedulerTestDB(t)
	account := model.UpstreamAccount{GroupID: 1, Name: "one-slot", Platform: model.PlatformOpenAI, AuthType: model.AuthAPIKey, Priority: 10, Concurrency: 1, Status: model.StatusActive}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	scheduler := NewScheduler(db)
	first, err := scheduler.PickForSession(1, "gpt-test", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scheduler.PickForSession(1, "gpt-test", "", nil); !errors.Is(err, ErrAccountConcurrencyBusy) {
		t.Fatalf("second pick error = %v", err)
	}

	type result struct {
		account *model.UpstreamAccount
		err     error
	}
	done := make(chan result, 1)
	go func() {
		selected, _, err := scheduler.PickForSessionWait(context.Background(), 1, "gpt-test", "", nil, time.Second, 4)
		done <- result{account: selected, err: err}
	}()
	time.Sleep(20 * time.Millisecond)
	if got := scheduler.WaitingCount(); got != 1 {
		t.Fatalf("waiting = %d", got)
	}
	scheduler.Release(first.ID)
	select {
	case acquired := <-done:
		if acquired.err != nil || acquired.account == nil || acquired.account.ID != account.ID {
			t.Fatalf("waited pick = %#v, %v", acquired.account, acquired.err)
		}
		scheduler.Release(acquired.account.ID)
	case <-time.After(time.Second):
		t.Fatal("account waiter was not woken")
	}
}

func TestSchedulerAccountQueueIsBounded(t *testing.T) {
	db := newSchedulerTestDB(t)
	account := model.UpstreamAccount{GroupID: 1, Name: "one-slot", Platform: model.PlatformOpenAI, AuthType: model.AuthAPIKey, Priority: 10, Concurrency: 1, Status: model.StatusActive}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	scheduler := NewScheduler(db)
	first, err := scheduler.Pick(1, nil)
	if err != nil {
		t.Fatal(err)
	}
	waiting := make(chan error, 1)
	go func() {
		_, _, err := scheduler.PickForSessionWait(context.Background(), 1, "", "", nil, 60*time.Millisecond, 1)
		waiting <- err
	}()
	time.Sleep(15 * time.Millisecond)
	if _, _, err := scheduler.PickForSessionWait(context.Background(), 1, "", "", nil, time.Second, 1); !errors.Is(err, ErrAccountQueueFull) {
		t.Fatalf("queue full error = %v", err)
	}
	if err := <-waiting; !errors.Is(err, ErrAccountWaitTimeout) {
		t.Fatalf("wait timeout error = %v", err)
	}
	scheduler.Release(first.ID)
}

func newSchedulerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.UpstreamAccount{}, &model.Proxy{}, &model.AccountQuotaSnapshot{}, &model.CodexQuotaSnapshot{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestSchedulerPrefersQuotaHeadroomWithinPriority(t *testing.T) {
	db := newSchedulerTestDB(t)
	now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	accounts := []model.UpstreamAccount{
		{GroupID: 1, Name: "nearly-used", Platform: model.PlatformOpenAI, AuthType: model.AuthOAuth, Priority: 10, Status: model.StatusActive},
		{GroupID: 1, Name: "available", Platform: model.PlatformOpenAI, AuthType: model.AuthOAuth, Priority: 10, Status: model.StatusActive},
	}
	if err := db.Create(&accounts).Error; err != nil {
		t.Fatal(err)
	}
	quotas := []model.CodexQuotaSnapshot{
		{UpstreamAccountID: accounts[0].ID, Allowed: true, HasPrimaryWindow: true, PrimaryUsedPercent: 95, FetchedAt: now},
		{UpstreamAccountID: accounts[1].ID, Allowed: true, HasPrimaryWindow: true, PrimaryUsedPercent: 15, FetchedAt: now},
	}
	if err := db.Create(&quotas).Error; err != nil {
		t.Fatal(err)
	}
	scheduler := NewScheduler(db)
	scheduler.now = func() time.Time { return now }
	selected, err := scheduler.Pick(1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if selected.ID != accounts[1].ID {
		t.Fatalf("selected %q, want account with quota headroom", selected.Name)
	}
}

func TestSchedulerKeepsFairRotationForSmallScoreDifference(t *testing.T) {
	db := newSchedulerTestDB(t)
	now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	accounts := []model.UpstreamAccount{
		{GroupID: 1, Name: "a", Platform: model.PlatformOpenAI, AuthType: model.AuthOAuth, Priority: 10, Status: model.StatusActive},
		{GroupID: 1, Name: "b", Platform: model.PlatformOpenAI, AuthType: model.AuthOAuth, Priority: 10, Status: model.StatusActive},
	}
	if err := db.Create(&accounts).Error; err != nil {
		t.Fatal(err)
	}
	quotas := []model.CodexQuotaSnapshot{
		{UpstreamAccountID: accounts[0].ID, Allowed: true, HasPrimaryWindow: true, PrimaryUsedPercent: 40, FetchedAt: now},
		{UpstreamAccountID: accounts[1].ID, Allowed: true, HasPrimaryWindow: true, PrimaryUsedPercent: 45, FetchedAt: now},
	}
	if err := db.Create(&quotas).Error; err != nil {
		t.Fatal(err)
	}
	scheduler := NewScheduler(db)
	scheduler.now = func() time.Time { return now }
	first, err := scheduler.Pick(1, nil)
	if err != nil {
		t.Fatal(err)
	}
	scheduler.Release(first.ID)
	now = now.Add(time.Second)
	second, err := scheduler.Pick(1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID {
		t.Fatalf("small quota difference stopped fair rotation on account %d", first.ID)
	}
}

func TestSchedulerSkipsFreshExhaustedCodexAccount(t *testing.T) {
	db := newSchedulerTestDB(t)
	now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	account := model.UpstreamAccount{GroupID: 1, Name: "exhausted", Platform: model.PlatformOpenAI, AuthType: model.AuthOAuth, Priority: 10, Status: model.StatusActive}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	quota := model.CodexQuotaSnapshot{UpstreamAccountID: account.ID, Allowed: true, LimitReached: true, FetchedAt: now}
	if err := db.Create(&quota).Error; err != nil {
		t.Fatal(err)
	}
	scheduler := NewScheduler(db)
	scheduler.now = func() time.Time { return now }
	if _, err := scheduler.Pick(1, nil); !errors.Is(err, ErrNoAccount) {
		t.Fatalf("exhausted account pick error = %v", err)
	}
}

func TestSchedulerDoesNotTrustLimitFlagPastResetBoundary(t *testing.T) {
	db := newSchedulerTestDB(t)
	now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	reset := now.Add(-time.Minute)
	account := model.UpstreamAccount{GroupID: 1, Name: "reset", Platform: model.PlatformOpenAI, AuthType: model.AuthOAuth, Priority: 10, Status: model.StatusActive}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	quota := model.CodexQuotaSnapshot{UpstreamAccountID: account.ID, Allowed: true, LimitReached: true, HasPrimaryWindow: true, PrimaryResetAt: &reset, FetchedAt: now.Add(-2 * time.Minute)}
	if err := db.Create(&quota).Error; err != nil {
		t.Fatal(err)
	}
	scheduler := NewScheduler(db)
	scheduler.now = func() time.Time { return now }
	if selected, err := scheduler.Pick(1, nil); err != nil || selected.ID != account.ID {
		t.Fatalf("past-reset account pick = %#v, %v", selected, err)
	}
}

func TestSchedulerUsesObservedLatencyAndReleasesInFlight(t *testing.T) {
	db := newSchedulerTestDB(t)
	accounts := []model.UpstreamAccount{
		{GroupID: 1, Name: "slow", Platform: model.PlatformOpenAI, AuthType: model.AuthAPIKey, Priority: 10, Status: model.StatusActive},
		{GroupID: 1, Name: "fast", Platform: model.PlatformOpenAI, AuthType: model.AuthAPIKey, Priority: 10, Status: model.StatusActive},
	}
	if err := db.Create(&accounts).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	scheduler := NewScheduler(db)
	scheduler.now = func() time.Time { return now }
	if _, err := scheduler.snapshot(1, now); err != nil {
		t.Fatal(err)
	}
	scheduler.ReportSuccessForModelWithLatency(accounts[0].ID, "gpt-test", 2000)
	scheduler.ReportSuccessForModelWithLatency(accounts[1].ID, "gpt-test", 100)
	selected, err := scheduler.PickForSession(1, "gpt-test", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if selected.ID != accounts[1].ID {
		t.Fatalf("selected %q, want lower-latency account", selected.Name)
	}
	scheduler.Release(selected.ID)
	scheduler.mu.Lock()
	inFlight := scheduler.groups[1].accounts[selected.ID].inFlight
	scheduler.mu.Unlock()
	if inFlight != 0 {
		t.Fatalf("in-flight count = %d", inFlight)
	}
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
