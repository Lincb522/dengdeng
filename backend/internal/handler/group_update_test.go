package handler

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"dengdeng/internal/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestUpdateGroupCanMakePublicGroupPrivateWithPartialRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:group-update-test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.Group{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	group := model.Group{Name: "public-group", Platform: model.PlatformOpenAI, IsPublic: true, Status: model.StatusActive}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/admin/groups/"+strconv.FormatInt(group.ID, 10), strings.NewReader(`{"is_public":false}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(group.ID, 10)}}

	(&AdminHandler{db: db}).UpdateGroup(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var updated model.Group
	if err := db.First(&updated, group.ID).Error; err != nil {
		t.Fatalf("reload group: %v", err)
	}
	if updated.IsPublic {
		t.Fatal("is_public remained true after explicit false update")
	}
}

func TestUpdateGroupSavesCacheMultipliers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:group-cache-update-test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.Group{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	group := model.Group{Name: "cache-group", Platform: model.PlatformOpenAI, Status: model.StatusActive}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/admin/groups/"+strconv.FormatInt(group.ID, 10), strings.NewReader(`{"cache_read_multiplier":0.2,"cache_write_5m_multiplier":0.4,"cache_write_1h_multiplier":0.6}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(group.ID, 10)}}

	(&AdminHandler{db: db}).UpdateGroup(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var updated model.Group
	if err := db.First(&updated, group.ID).Error; err != nil {
		t.Fatalf("reload group: %v", err)
	}
	if updated.CacheReadMultiplier != 0.2 || updated.CacheWrite5mMultiplier != 0.4 || updated.CacheWrite1hMultiplier != 0.6 {
		t.Fatalf("cache multipliers = %#v", updated)
	}
}
