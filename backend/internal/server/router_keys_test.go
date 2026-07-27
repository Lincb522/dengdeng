package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"dengdeng/internal/config"
	"dengdeng/internal/crypto"
	"dengdeng/internal/model"
	"dengdeng/internal/service"
	"dengdeng/internal/store"
	"dengdeng/internal/util"
)

func TestAPIKeySupportsMultipleGroups(t *testing.T) {
	cfg := config.Default()
	cfg.JWT.Secret = "router-multi-group-key-secret"
	cfg.Database.Path = filepath.Join(t.TempDir(), "test.db")
	if err := crypto.Init("", cfg.JWT.Secret); err != nil {
		t.Fatalf("crypto.Init: %v", err)
	}
	db, err := store.Open(cfg)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	hash, err := util.HashPassword("admin12345")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	admin := model.User{Email: "admin@example.test", PasswordHash: hash, Role: model.RoleAdmin, Status: model.StatusActive, RateMultiplier: 1}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatalf("create admin: %v", err)
	}
	groups := []model.Group{
		{Name: "openai-primary", Platform: model.PlatformOpenAI, IsPublic: true, Status: model.StatusActive, RateMultiplier: 1},
		{Name: "openai-fallback", Platform: model.PlatformOpenAI, IsPublic: true, Status: model.StatusActive, RateMultiplier: 0.8},
		{Name: "claude", Platform: model.PlatformAnthropic, IsPublic: true, Status: model.StatusActive, RateMultiplier: 1},
	}
	if err := db.Create(&groups).Error; err != nil {
		t.Fatalf("create groups: %v", err)
	}
	router := NewRouter(cfg, db)
	settings, err := service.NewSystemSettingsService(db, cfg).Get()
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	login := callJSON(t, router, http.MethodPost, "/api/auth/login", map[string]any{
		"email": admin.Email, "password": "admin12345", "terms_revision": settings.LoginAgreement.Revision(),
	}, "")
	var loginBody struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if login.Code != http.StatusOK || json.Unmarshal(login.Body.Bytes(), &loginBody) != nil || loginBody.Data.Token == "" {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body.String())
	}

	create := callJSON(t, router, http.MethodPost, "/api/user/keys", map[string]any{
		"name": "multi", "group_ids": []int64{groups[0].ID, groups[1].ID, groups[2].ID},
	}, loginBody.Data.Token)
	var created struct {
		Data struct {
			Key   model.APIKey `json:"key"`
			Plain string       `json:"plain"`
		} `json:"data"`
	}
	if create.Code != http.StatusOK || json.Unmarshal(create.Body.Bytes(), &created) != nil {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	if created.Data.Key.GroupID != groups[0].ID || len(created.Data.Key.GroupIDs) != 3 || len(created.Data.Key.Groups) != 3 {
		t.Fatalf("unexpected created key groups: %#v", created.Data.Key)
	}
	if created.Data.Plain == "" || !created.Data.Key.SecretAvailable {
		t.Fatalf("created key did not report a recoverable secret: %#v", created.Data.Key)
	}
	var storedSecret string
	if err := db.Raw("SELECT key_secret FROM api_keys WHERE id = ?", created.Data.Key.ID).Scan(&storedSecret).Error; err != nil {
		t.Fatalf("read encrypted secret: %v", err)
	}
	if storedSecret == "" || storedSecret == created.Data.Plain {
		t.Fatalf("secret is not encrypted at rest: %q", storedSecret)
	}
	reveal := callJSON(t, router, http.MethodGet, "/api/user/keys/"+jsonNumber(created.Data.Key.ID)+"/secret", nil, loginBody.Data.Token)
	var revealed struct {
		Data struct {
			Plain string `json:"plain"`
		} `json:"data"`
	}
	if reveal.Code != http.StatusOK || json.Unmarshal(reveal.Body.Bytes(), &revealed) != nil || revealed.Data.Plain != created.Data.Plain {
		t.Fatalf("reveal status=%d body=%s", reveal.Code, reveal.Body.String())
	}
	if reveal.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("reveal response is cacheable: %q", reveal.Header().Get("Cache-Control"))
	}
	list := callJSON(t, router, http.MethodGet, "/api/user/keys", nil, loginBody.Data.Token)
	var listed struct {
		Data []map[string]any `json:"data"`
	}
	if list.Code != http.StatusOK || json.Unmarshal(list.Body.Bytes(), &listed) != nil || len(listed.Data) == 0 {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}
	if _, exists := listed.Data[0]["key_secret"]; exists {
		t.Fatalf("list response leaked key_secret: %s", list.Body.String())
	}

	// Simulate a pre-encryption row and verify the browser migration endpoint.
	if err := db.Model(&model.APIKey{}).Where("id = ?", created.Data.Key.ID).Update("key_secret", "").Error; err != nil {
		t.Fatalf("clear legacy secret: %v", err)
	}
	legacyReveal := callJSON(t, router, http.MethodGet, "/api/user/keys/"+jsonNumber(created.Data.Key.ID)+"/secret", nil, loginBody.Data.Token)
	if legacyReveal.Code != http.StatusConflict {
		t.Fatalf("legacy reveal status=%d body=%s", legacyReveal.Code, legacyReveal.Body.String())
	}
	wrongRecovery := callJSON(t, router, http.MethodPut, "/api/user/keys/"+jsonNumber(created.Data.Key.ID)+"/secret", map[string]any{"plain": "dd-not-the-key"}, loginBody.Data.Token)
	if wrongRecovery.Code != http.StatusBadRequest {
		t.Fatalf("wrong recovery status=%d body=%s", wrongRecovery.Code, wrongRecovery.Body.String())
	}
	recovery := callJSON(t, router, http.MethodPut, "/api/user/keys/"+jsonNumber(created.Data.Key.ID)+"/secret", map[string]any{"plain": created.Data.Plain}, loginBody.Data.Token)
	if recovery.Code != http.StatusOK {
		t.Fatalf("recovery status=%d body=%s", recovery.Code, recovery.Body.String())
	}
	var bindingCount int64
	if err := db.Model(&model.APIKeyGroup{}).Where("api_key_id = ?", created.Data.Key.ID).Count(&bindingCount).Error; err != nil || bindingCount != 3 {
		t.Fatalf("binding count=%d err=%v", bindingCount, err)
	}

	update := callJSON(t, router, http.MethodPut, "/api/user/keys/"+jsonNumber(created.Data.Key.ID), map[string]any{
		"group_ids": []int64{groups[2].ID, groups[1].ID},
	}, loginBody.Data.Token)
	var updated struct {
		Data model.APIKey `json:"data"`
	}
	if update.Code != http.StatusOK || json.Unmarshal(update.Body.Bytes(), &updated) != nil {
		t.Fatalf("update status=%d body=%s", update.Code, update.Body.String())
	}
	if updated.Data.GroupID != groups[2].ID || len(updated.Data.GroupIDs) != 2 || updated.Data.GroupIDs[0] != groups[2].ID {
		t.Fatalf("unexpected updated groups: %#v", updated.Data)
	}
	if err := db.Model(&model.APIKeyGroup{}).Where("api_key_id = ?", created.Data.Key.ID).Count(&bindingCount).Error; err != nil || bindingCount != 2 {
		t.Fatalf("updated binding count=%d err=%v", bindingCount, err)
	}

	settings.KeyMultiGroupEnabled = false
	if _, err := service.NewSystemSettingsService(db, cfg).Update(settings); err != nil {
		t.Fatalf("disable multi-group keys: %v", err)
	}
	if err := db.Model(&model.APIKeyGroup{}).Where("api_key_id = ?", created.Data.Key.ID).Count(&bindingCount).Error; err != nil || bindingCount != 1 {
		t.Fatalf("collapsed binding count=%d err=%v", bindingCount, err)
	}
	var primaryBinding model.APIKeyGroup
	if err := db.Where("api_key_id = ?", created.Data.Key.ID).First(&primaryBinding).Error; err != nil || primaryBinding.GroupID != groups[2].ID {
		t.Fatalf("unexpected primary binding=%#v err=%v", primaryBinding, err)
	}

	rejectedCreate := callJSON(t, router, http.MethodPost, "/api/user/keys", map[string]any{
		"name": "rejected-multi", "group_ids": []int64{groups[0].ID, groups[1].ID},
	}, loginBody.Data.Token)
	if rejectedCreate.Code != http.StatusBadRequest {
		t.Fatalf("disabled multi-group create status=%d body=%s", rejectedCreate.Code, rejectedCreate.Body.String())
	}
	rejectedUpdate := callJSON(t, router, http.MethodPut, "/api/user/keys/"+jsonNumber(created.Data.Key.ID), map[string]any{
		"group_ids": []int64{groups[0].ID, groups[1].ID},
	}, loginBody.Data.Token)
	if rejectedUpdate.Code != http.StatusBadRequest {
		t.Fatalf("disabled multi-group update status=%d body=%s", rejectedUpdate.Code, rejectedUpdate.Body.String())
	}
	acceptedSingle := callJSON(t, router, http.MethodPost, "/api/user/keys", map[string]any{
		"name": "single", "group_ids": []int64{groups[0].ID},
	}, loginBody.Data.Token)
	if acceptedSingle.Code != http.StatusOK {
		t.Fatalf("single-group create status=%d body=%s", acceptedSingle.Code, acceptedSingle.Body.String())
	}
}

func TestAPIKeyRouteVisibilityAndUsageProjection(t *testing.T) {
	cfg := config.Default()
	cfg.JWT.Secret = "router-key-route-visibility-secret"
	cfg.Database.Path = filepath.Join(t.TempDir(), "test.db")
	if err := crypto.Init("", cfg.JWT.Secret); err != nil {
		t.Fatalf("crypto.Init: %v", err)
	}
	db, err := store.Open(cfg)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	passwordHash, err := util.HashPassword("password12345")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	users := []model.User{
		{Email: "user@example.test", PasswordHash: passwordHash, Role: model.RoleUser, Status: model.StatusActive, RateMultiplier: 1},
		{Email: "admin@example.test", PasswordHash: passwordHash, Role: model.RoleAdmin, Status: model.StatusActive, RateMultiplier: 1},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatalf("create users: %v", err)
	}
	groups := []model.Group{
		{Name: "public-route", Platform: model.PlatformOpenAI, IsPublic: true, Status: model.StatusActive, RateMultiplier: 1},
		{Name: "private-route", Platform: model.PlatformOpenAI, IsPublic: false, Status: model.StatusActive, RateMultiplier: 1},
		{Name: "disabled-route", Platform: model.PlatformOpenAI, IsPublic: true, Status: model.StatusDisabled, RateMultiplier: 1},
	}
	if err := db.Create(&groups).Error; err != nil {
		t.Fatalf("create groups: %v", err)
	}
	if err := db.Model(&model.Group{}).Where("id = ?", groups[1].ID).UpdateColumn("is_public", false).Error; err != nil {
		t.Fatalf("make group private: %v", err)
	}

	router := NewRouter(cfg, db)
	settings, err := service.NewSystemSettingsService(db, cfg).Get()
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	login := func(email string) string {
		response := callJSON(t, router, http.MethodPost, "/api/auth/login", map[string]any{
			"email": email, "password": "password12345", "terms_revision": settings.LoginAgreement.Revision(),
		}, "")
		var body struct {
			Data struct {
				Token string `json:"token"`
			} `json:"data"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Data.Token == "" {
			t.Fatalf("login %s status=%d body=%s", email, response.Code, response.Body.String())
		}
		return body.Data.Token
	}
	userToken := login(users[0].Email)
	adminToken := login(users[1].Email)

	visible := callJSON(t, router, http.MethodGet, "/api/user/groups", nil, userToken)
	var visibleBody struct {
		Data []model.Group `json:"data"`
	}
	if visible.Code != http.StatusOK || json.Unmarshal(visible.Body.Bytes(), &visibleBody) != nil || len(visibleBody.Data) != 1 || visibleBody.Data[0].ID != groups[0].ID {
		t.Fatalf("ordinary user groups status=%d body=%s", visible.Code, visible.Body.String())
	}

	created := callJSON(t, router, http.MethodPost, "/api/user/keys", map[string]any{
		"name": "routed", "group_ids": []int64{groups[0].ID},
	}, userToken)
	var createdBody struct {
		Data struct {
			Key model.APIKey `json:"key"`
		} `json:"data"`
	}
	if created.Code != http.StatusOK || json.Unmarshal(created.Body.Bytes(), &createdBody) != nil || createdBody.Data.Key.ID == 0 {
		t.Fatalf("create key status=%d body=%s", created.Code, created.Body.String())
	}
	keyPath := "/api/user/keys/" + jsonNumber(createdBody.Data.Key.ID)
	privateRoute := callJSON(t, router, http.MethodPut, keyPath, map[string]any{"group_ids": []int64{groups[1].ID}}, userToken)
	if privateRoute.Code != http.StatusForbidden {
		t.Fatalf("ordinary user private route status=%d body=%s", privateRoute.Code, privateRoute.Body.String())
	}
	disabledRoute := callJSON(t, router, http.MethodPut, keyPath, map[string]any{"group_ids": []int64{groups[2].ID}}, userToken)
	if disabledRoute.Code != http.StatusBadRequest {
		t.Fatalf("ordinary user disabled route status=%d body=%s", disabledRoute.Code, disabledRoute.Body.String())
	}
	adminPrivateKey := callJSON(t, router, http.MethodPost, "/api/user/keys", map[string]any{
		"name": "private-admin", "group_ids": []int64{groups[1].ID},
	}, adminToken)
	if adminPrivateKey.Code != http.StatusOK {
		t.Fatalf("admin private route status=%d body=%s", adminPrivateKey.Code, adminPrivateKey.Body.String())
	}

	now := time.Now()
	logs := []model.UsageLog{
		{UserID: users[0].ID, APIKeyID: createdBody.Data.Key.ID, GroupID: groups[0].ID, CostMicro: 100, CreatedAt: now},
		{UserID: users[0].ID, APIKeyID: createdBody.Data.Key.ID, GroupID: groups[0].ID, CostMicro: 200, CreatedAt: now.AddDate(0, 0, -10)},
		{UserID: users[0].ID, APIKeyID: createdBody.Data.Key.ID, GroupID: groups[0].ID, CostMicro: 400, CreatedAt: now.AddDate(0, 0, -40)},
	}
	if err := db.Create(&logs).Error; err != nil {
		t.Fatalf("create usage logs: %v", err)
	}
	listed := callJSON(t, router, http.MethodGet, "/api/user/keys", nil, userToken)
	var listedBody struct {
		Data []model.APIKey `json:"data"`
	}
	if listed.Code != http.StatusOK || json.Unmarshal(listed.Body.Bytes(), &listedBody) != nil || len(listedBody.Data) != 1 {
		t.Fatalf("list keys status=%d body=%s", listed.Code, listed.Body.String())
	}
	if listedBody.Data[0].UsageTodayMicro != 100 || listedBody.Data[0].Usage30dMicro != 300 {
		t.Fatalf("unexpected projected usage today=%d month=%d", listedBody.Data[0].UsageTodayMicro, listedBody.Data[0].Usage30dMicro)
	}
}

func jsonNumber(value int64) string {
	return strconv.FormatInt(value, 10)
}
