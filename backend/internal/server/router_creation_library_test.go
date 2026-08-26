package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"dengdeng/internal/config"
	"dengdeng/internal/crypto"
	"dengdeng/internal/model"
	"dengdeng/internal/service"
	"dengdeng/internal/store"
	"dengdeng/internal/util"
)

func TestCreationLibraryAdminAndUserEndpoints(t *testing.T) {
	cfg := config.Default()
	cfg.JWT.Secret = "creation-library-test-secret"
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
		t.Fatal(err)
	}
	admin := model.User{Email: "admin@example.test", PasswordHash: hash, Role: model.RoleAdmin, Status: model.StatusActive, RateMultiplier: 1}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}

	settings, err := service.NewSystemSettingsService(db, cfg).Get()
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(cfg, db)
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

	get := callJSON(t, router, http.MethodGet, "/api/admin/creation-library", nil, loginBody.Data.Token)
	var initial struct {
		Data service.CreationLibrarySettings `json:"data"`
	}
	if get.Code != http.StatusOK || json.Unmarshal(get.Body.Bytes(), &initial) != nil || len(initial.Data.Prompts) == 0 {
		t.Fatalf("get status=%d body=%s", get.Code, get.Body.String())
	}

	next := service.CreationLibrarySettings{
		Enabled:        true,
		CatalogVersion: 7,
		Capabilities: service.CreationCapabilitySettings{
			Prompts: true, Rules: true, Skills: true, Chat: true, Image: true, Video: true, Audio: true,
		},
		Rules: []service.CreationLibraryEntry{{
			ID: "verified-only", Name: "仅使用已验证信息", Content: "区分事实和假设。", Scope: service.CreationScopeChat, Enabled: true, AutoApply: true,
		}},
		Skills: []service.CreationLibraryEntry{{
			ID: "review", Name: "审查", NameEN: "Review", Description: "检查问题", DescriptionEN: "Find issues", Content: "检查可复现问题。", ContentEN: "Find reproducible issues.", Scope: service.CreationScopeChat, Enabled: true, Version: "1.0.0", SourceURL: "https://github.com/example/review-skill", InstallCommand: "npx skills add example/review-skill",
		}},
	}
	update := callJSON(t, router, http.MethodPut, "/api/admin/creation-library", next, loginBody.Data.Token)
	if update.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", update.Code, update.Body.String())
	}

	userView := callJSON(t, router, http.MethodGet, "/api/user/creation-library", nil, loginBody.Data.Token)
	var public struct {
		Data service.CreationLibrarySettings `json:"data"`
	}
	if userView.Code != http.StatusOK || json.Unmarshal(userView.Body.Bytes(), &public) != nil {
		t.Fatalf("user view status=%d body=%s", userView.Code, userView.Body.String())
	}
	if len(public.Data.Rules) != 1 || public.Data.Rules[0].ID != "verified-only" || !public.Data.Rules[0].AutoApply {
		t.Fatalf("unexpected public library: %#v", public.Data)
	}
	if len(public.Data.Skills) != 1 || public.Data.Skills[0].NameEN != "Review" || public.Data.Skills[0].DescriptionEN != "Find issues" || public.Data.Skills[0].ContentEN != "Find reproducible issues." || public.Data.Skills[0].SourceURL != "https://github.com/example/review-skill" || public.Data.Skills[0].InstallCommand != "npx skills add example/review-skill" {
		t.Fatalf("bilingual skill fields were not preserved: %#v", public.Data.Skills)
	}

	selection := callJSON(t, router, http.MethodPut, "/api/user/creation-library/selection", map[string]any{
		"rule_ids": []string{}, "skill_ids": []string{"review"},
	}, loginBody.Data.Token)
	if selection.Code != http.StatusOK {
		t.Fatalf("selection status=%d body=%s", selection.Code, selection.Body.String())
	}
	selectedView := callJSON(t, router, http.MethodGet, "/api/user/creation-library", nil, loginBody.Data.Token)
	var selected struct {
		Data service.UserCreationLibrary `json:"data"`
	}
	if selectedView.Code != http.StatusOK || json.Unmarshal(selectedView.Body.Bytes(), &selected) != nil || len(selected.Data.SelectedSkillIDs) != 1 || selected.Data.SelectedSkillIDs[0] != "review" {
		t.Fatalf("selected view status=%d body=%s", selectedView.Code, selectedView.Body.String())
	}
}
