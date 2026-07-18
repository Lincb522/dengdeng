package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"

	"dengdeng/internal/config"
	"dengdeng/internal/model"
	"dengdeng/internal/service"
	"dengdeng/internal/store"
	"dengdeng/internal/util"
)

func TestAPIKeySupportsMultipleGroups(t *testing.T) {
	cfg := config.Default()
	cfg.JWT.Secret = "router-multi-group-key-secret"
	cfg.Database.Path = filepath.Join(t.TempDir(), "test.db")
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
			Key model.APIKey `json:"key"`
		} `json:"data"`
	}
	if create.Code != http.StatusOK || json.Unmarshal(create.Body.Bytes(), &created) != nil {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	if created.Data.Key.GroupID != groups[0].ID || len(created.Data.Key.GroupIDs) != 3 || len(created.Data.Key.Groups) != 3 {
		t.Fatalf("unexpected created key groups: %#v", created.Data.Key)
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
}

func jsonNumber(value int64) string {
	return strconv.FormatInt(value, 10)
}
