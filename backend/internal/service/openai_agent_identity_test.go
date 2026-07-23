package service

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appcrypto "dengdeng/internal/crypto"
	"dengdeng/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestAgentIdentityAssertionMatchesSignedEnvelope(t *testing.T) {
	privateKey, encodedPrivateKey := testAgentIdentityPrivateKey(t)
	record := AgentIdentityRecord{
		AgentRuntimeID: "runtime-1", AgentPrivateKey: encodedPrivateKey,
		TaskID: "task-1", AccountID: "acct-1", ChatGPTUserID: "user-1",
	}
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	header, err := OpenAIAgentIdentityAuthorization(record, now)
	if err != nil {
		t.Fatal(err)
	}
	encoded := strings.TrimPrefix(header, "AgentAssertion ")
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]string
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope["agent_runtime_id"] != record.AgentRuntimeID || envelope["task_id"] != record.TaskID || envelope["timestamp"] != now.Format(time.RFC3339) {
		t.Fatalf("unexpected assertion: %#v", envelope)
	}
	signature, err := base64.StdEncoding.DecodeString(envelope["signature"])
	if err != nil {
		t.Fatal(err)
	}
	parsedPrivateKey, err := parseAgentIdentityPrivateKey(record.AgentPrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	message := []byte(record.AgentRuntimeID + ":" + record.TaskID + ":" + envelope["timestamp"])
	if !ed25519.Verify(parsedPrivateKey.Public().(ed25519.PublicKey), message, signature) {
		t.Fatal("assertion signature is invalid")
	}
	if !privateKey.Public().(ed25519.PublicKey).Equal(parsedPrivateKey.Public()) {
		t.Fatal("parsed private key does not match generated key")
	}
}

func TestIsChatGPTAccountFedRAMPUsesImportedIdentityFlag(t *testing.T) {
	extra, err := model.EncodeExtra(map[string]any{"chatgpt_account_is_fedramp": true})
	if err != nil {
		t.Fatal(err)
	}
	account := &model.UpstreamAccount{
		Platform: model.PlatformOpenAI,
		AuthType: model.AuthAgentIdentity,
		Extra:    extra,
	}
	if !IsChatGPTAccountFedRAMP(account) {
		t.Fatal("expected imported FedRAMP identity flag to be applied")
	}
	account.Platform = model.PlatformAnthropic
	if IsChatGPTAccountFedRAMP(account) {
		t.Fatal("non-OpenAI account must not use the ChatGPT FedRAMP header")
	}
}

func TestEnsureAgentIdentityTaskRegistersOnceAcrossConcurrentServices(t *testing.T) {
	if err := appcrypto.Init("", "agent-identity-task-test"); err != nil {
		t.Fatal(err)
	}
	openAIAgentIdentityTaskLocks = sync.Map{}
	_, encodedPrivateKey := testAgentIdentityPrivateKey(t)

	var registrations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		registrations.Add(1)
		if r.URL.Path != "/v1/agent/runtime-shared/task/register" {
			t.Errorf("unexpected registration path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"task_id":"task-shared"}`))
	}))
	defer server.Close()
	oldBaseURL := openAIAgentIdentityAuthBaseURL
	openAIAgentIdentityAuthBaseURL = server.URL
	t.Cleanup(func() { openAIAgentIdentityAuthBaseURL = oldBaseURL })

	db, err := gorm.Open(sqlite.Open("file:agent-identity-task?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Group{}, &model.Proxy{}, &model.UpstreamAccount{}); err != nil {
		t.Fatal(err)
	}
	extra, err := model.EncodeExtra(AgentIdentityExtra(AgentIdentityRecord{
		AgentRuntimeID: "runtime-shared", AgentPrivateKey: encodedPrivateKey,
		AccountID: "account-shared", ChatGPTUserID: "user-shared",
	}))
	if err != nil {
		t.Fatal(err)
	}
	stored := model.UpstreamAccount{
		Name: "identity", Platform: model.PlatformOpenAI, AuthType: model.AuthAgentIdentity,
		AccountID: "account-shared", Extra: extra, Status: model.StatusActive,
	}
	if err := db.Create(&stored).Error; err != nil {
		t.Fatal(err)
	}

	var first, second model.UpstreamAccount
	if err := db.First(&first, stored.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&second, stored.ID).Error; err != nil {
		t.Fatal(err)
	}
	factory := func(*model.UpstreamAccount) (*http.Client, error) { return server.Client(), nil }
	results := make(chan AgentIdentityRecord, 2)
	errs := make(chan error, 2)
	var group sync.WaitGroup
	for _, account := range []*model.UpstreamAccount{&first, &second} {
		group.Add(1)
		go func(candidate *model.UpstreamAccount) {
			defer group.Done()
			record, ensureErr := EnsureOpenAIAgentIdentityTask(context.Background(), db, factory, candidate, "")
			results <- record
			errs <- ensureErr
		}(account)
	}
	group.Wait()
	close(results)
	close(errs)
	for ensureErr := range errs {
		if ensureErr != nil {
			t.Fatal(ensureErr)
		}
	}
	for record := range results {
		if record.TaskID != "task-shared" {
			t.Fatalf("task id = %q, want task-shared", record.TaskID)
		}
	}
	if registrations.Load() != 1 {
		t.Fatalf("registrations = %d, want 1", registrations.Load())
	}
}

func TestEnsureAgentIdentityTaskOnlyRotatesObservedTask(t *testing.T) {
	_, encodedPrivateKey := testAgentIdentityPrivateKey(t)
	var registrations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		registrations.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"task_id":"task-new"}`))
	}))
	defer server.Close()
	oldBaseURL := openAIAgentIdentityAuthBaseURL
	openAIAgentIdentityAuthBaseURL = server.URL
	t.Cleanup(func() { openAIAgentIdentityAuthBaseURL = oldBaseURL })

	extra, err := model.EncodeExtra(AgentIdentityExtra(AgentIdentityRecord{
		AgentRuntimeID: "runtime-rotate", AgentPrivateKey: encodedPrivateKey, TaskID: "task-current",
		AccountID: "account-rotate", ChatGPTUserID: "user-rotate",
	}))
	if err != nil {
		t.Fatal(err)
	}
	account := &model.UpstreamAccount{
		ID: 9001, Name: "identity", Platform: model.PlatformOpenAI, AuthType: model.AuthAgentIdentity,
		AccountID: "account-rotate", Extra: extra,
	}
	factory := func(*model.UpstreamAccount) (*http.Client, error) { return server.Client(), nil }
	record, err := EnsureOpenAIAgentIdentityTask(context.Background(), nil, factory, account, "task-stale")
	if err != nil {
		t.Fatal(err)
	}
	if record.TaskID != "task-current" || registrations.Load() != 0 {
		t.Fatalf("stale recovery rotated current task: record=%q registrations=%d", record.TaskID, registrations.Load())
	}
	record, err = EnsureOpenAIAgentIdentityTask(context.Background(), nil, factory, account, "task-current")
	if err != nil {
		t.Fatal(err)
	}
	if record.TaskID != "task-new" || registrations.Load() != 1 {
		t.Fatalf("observed task was not rotated: record=%q registrations=%d", record.TaskID, registrations.Load())
	}
}

func TestRegisterAgentIdentityTaskDoesNotRetryAmbiguousFailure(t *testing.T) {
	_, encodedPrivateKey := testAgentIdentityPrivateKey(t)
	var registrations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		registrations.Add(1)
		http.Error(w, "temporary failure", http.StatusInternalServerError)
	}))
	defer server.Close()
	oldBaseURL := openAIAgentIdentityAuthBaseURL
	openAIAgentIdentityAuthBaseURL = server.URL
	t.Cleanup(func() { openAIAgentIdentityAuthBaseURL = oldBaseURL })

	_, err := RegisterOpenAIAgentTask(context.Background(), server.Client(), AgentIdentityRecord{
		AgentRuntimeID: "runtime-once", AgentPrivateKey: encodedPrivateKey,
		AccountID: "account-once", ChatGPTUserID: "user-once",
	})
	if err == nil {
		t.Fatal("expected registration failure")
	}
	if registrations.Load() != 1 {
		t.Fatalf("registrations = %d, want exactly 1", registrations.Load())
	}
	if strings.Contains(err.Error(), "temporary failure") {
		t.Fatal("registration error leaked upstream response body")
	}
}

func TestAgentIdentityErrorRedaction(t *testing.T) {
	_, encodedPrivateKey := testAgentIdentityPrivateKey(t)
	record := AgentIdentityRecord{
		AgentRuntimeID: "runtime-secret", AgentPrivateKey: encodedPrivateKey, TaskID: "task-secret",
		AccountID: "account-secret", ChatGPTUserID: "user-secret",
	}
	extra, err := model.EncodeExtra(AgentIdentityExtra(record))
	if err != nil {
		t.Fatal(err)
	}
	account := &model.UpstreamAccount{
		Platform: model.PlatformOpenAI, AuthType: model.AuthAgentIdentity, Extra: extra,
	}
	body := []byte(`{"message":"runtime-secret task-secret ` + encodedPrivateKey + ` AgentAssertion abc.def"}`)
	redacted := string(RedactOpenAIAgentIdentitySensitiveBody(account, body))
	for _, secret := range []string{"runtime-secret", "task-secret", encodedPrivateKey, "abc.def"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("redacted body still contains %q: %s", secret, redacted)
		}
	}
	if !strings.Contains(redacted, "AgentAssertion [redacted]") {
		t.Fatalf("assertion was not redacted: %s", redacted)
	}
}

func TestAgentIdentityQuotaErrorIsRedacted(t *testing.T) {
	_, encodedPrivateKey := testAgentIdentityPrivateKey(t)
	record := AgentIdentityRecord{
		AgentRuntimeID: "runtime-quota-secret", AgentPrivateKey: encodedPrivateKey, TaskID: "task-quota-secret",
		AccountID: "account-quota-secret", ChatGPTUserID: "user-quota-secret", ChatGPTAccountIsFedRAMP: true,
	}
	extra, err := model.EncodeExtra(AgentIdentityExtra(record))
	if err != nil {
		t.Fatal(err)
	}
	account := &model.UpstreamAccount{
		ID: 7001, Platform: model.PlatformOpenAI, AuthType: model.AuthAgentIdentity,
		AccountID: record.AccountID, Extra: extra,
	}
	var fedRAMPHeader string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fedRAMPHeader = r.Header.Get("x-openai-fedramp")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"runtime-quota-secret task-quota-secret ` + encodedPrivateKey + `"}`))
	}))
	defer upstream.Close()
	oldUsageURL := openAICodexUsageURL
	openAICodexUsageURL = upstream.URL + "/usage"
	t.Cleanup(func() { openAICodexUsageURL = oldUsageURL })

	service := NewAccountQuotaService(nil, nil, nil, upstream.Client())
	err = service.refreshOpenAIAgentIdentity(context.Background(), account, &model.AccountQuotaSnapshot{})
	if err == nil {
		t.Fatal("expected quota error")
	}
	if fedRAMPHeader != "true" {
		t.Fatalf("quota x-openai-fedramp = %q", fedRAMPHeader)
	}
	for _, secret := range []string{record.AgentRuntimeID, record.TaskID, record.AgentPrivateKey} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("quota error leaked %q: %s", secret, err)
		}
	}
}

func testAgentIdentityPrivateKey(t *testing.T) (ed25519.PrivateKey, string) {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return privateKey, base64.StdEncoding.EncodeToString(der)
}
