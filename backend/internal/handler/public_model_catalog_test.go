package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dengdeng/internal/config"
	"dengdeng/internal/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestPublicModelCatalogueOnlyExposesPublicGroups(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:public-model-catalog?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Group{}, &model.UpstreamAccount{}, &model.ModelConfig{}, &model.ModelPrice{}); err != nil {
		t.Fatal(err)
	}

	publicGroup := model.Group{Name: "public-claude", Platform: model.PlatformAnthropic, RateMultiplier: .5, IsPublic: true, Status: model.StatusActive}
	privateGroup := model.Group{Name: "private-claude", Platform: model.PlatformAnthropic, RateMultiplier: .1, IsPublic: false, Status: model.StatusActive}
	if err := db.Create(&publicGroup).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&privateGroup).Error; err != nil {
		t.Fatal(err)
	}
	// GORM applies the model's default:true tag when a false zero value is
	// inserted, so persist the private state explicitly just like the admin
	// update endpoint does.
	if err := db.Model(&privateGroup).Update("is_public", false).Error; err != nil {
		t.Fatal(err)
	}
	accounts := []model.UpstreamAccount{
		{GroupID: publicGroup.ID, Name: "public-account", Platform: model.PlatformAnthropic, Status: model.StatusActive},
		{GroupID: privateGroup.ID, Name: "private-account", Platform: model.PlatformAnthropic, Status: model.StatusActive},
	}
	if err := db.Create(&accounts).Error; err != nil {
		t.Fatal(err)
	}
	configured := model.ModelConfig{
		Name: "claude-test", Platform: model.PlatformAnthropic, Kind: "chat", UpstreamModel: "secret-upstream-alias",
		ContextWindow: 200000, MaxOutputTokens: 32000, SupportsTools: true, Status: model.StatusActive,
	}
	if err := db.Create(&configured).Error; err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/models", nil)
	NewUserHandler(db, &config.Config{}).PublicModelCatalogue(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "private-claude") || strings.Contains(recorder.Body.String(), "secret-upstream-alias") {
		t.Fatalf("public response leaked private routing data: %s", recorder.Body.String())
	}
	var response struct {
		Data []struct {
			Name   string `json:"name"`
			Groups []struct {
				Name  string `json:"name"`
				Ready bool   `json:"ready"`
			} `json:"groups"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 1 || response.Data[0].Name != "claude-test" {
		t.Fatalf("unexpected models: %#v", response.Data)
	}
	if len(response.Data[0].Groups) != 1 || response.Data[0].Groups[0].Name != "public-claude" || !response.Data[0].Groups[0].Ready {
		t.Fatalf("unexpected groups: %#v", response.Data[0].Groups)
	}
}
