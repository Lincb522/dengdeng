package handler

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"dengdeng/internal/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newGroupDeleteTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "-")+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&model.User{}, &model.Group{}, &model.APIKey{}, &model.APIKeyGroup{},
		&model.Proxy{}, &model.UpstreamAccount{}, &model.UpstreamAccountGroup{},
		&model.UserGroupSubscription{}, &model.UserGroupRate{}, &model.ModelConfig{}, &model.AlertRule{},
	); err != nil {
		t.Fatal(err)
	}
	return db
}

func deleteGroupRequest(groupID int64, query string) (*httptest.ResponseRecorder, *gin.Context) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodDelete, "/api/admin/groups/"+strconv.FormatInt(groupID, 10)+query, nil)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(groupID, 10)}}
	return recorder, ctx
}

func TestDeleteGroupRequiresUnbindConfirmation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newGroupDeleteTestDB(t)
	group := model.Group{Name: "protected", Platform: model.PlatformOpenAI, Status: model.StatusActive}
	user := model.User{Email: "protected@example.test", PasswordHash: "hash", Role: model.RoleUser, Status: model.StatusActive}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	key := model.APIKey{UserID: user.ID, GroupID: group.ID, KeyHash: "protected-hash", KeyPreview: "dd-protected", Name: "protected", Status: model.StatusActive}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.APIKeyGroup{APIKeyID: key.ID, GroupID: group.ID}).Error; err != nil {
		t.Fatal(err)
	}

	recorder, ctx := deleteGroupRequest(group.ID, "")
	(&AdminHandler{db: db}).DeleteGroup(ctx)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if err := db.First(&group, group.ID).Error; err != nil {
		t.Fatalf("group was deleted without confirmation: %v", err)
	}
}

func TestDeleteGroupCanMigrateExclusiveResourcesAndUnbindSharedResources(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newGroupDeleteTestDB(t)
	groups := []model.Group{
		{Name: "source", Platform: model.PlatformOpenAI, Status: model.StatusActive},
		{Name: "target", Platform: model.PlatformOpenAI, Status: model.StatusActive},
		{Name: "other", Platform: model.PlatformOpenAI, Status: model.StatusActive},
	}
	if err := db.Create(&groups).Error; err != nil {
		t.Fatal(err)
	}
	user := model.User{Email: "migration@example.test", PasswordHash: "hash", Role: model.RoleUser, Status: model.StatusActive}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}

	exclusiveKey := model.APIKey{UserID: user.ID, GroupID: groups[0].ID, KeyHash: "exclusive-hash", KeyPreview: "dd-exclusive", Name: "exclusive", Status: model.StatusActive}
	sharedKey := model.APIKey{UserID: user.ID, GroupID: groups[0].ID, KeyHash: "shared-hash", KeyPreview: "dd-shared", Name: "shared", Status: model.StatusActive}
	if err := db.Create(&exclusiveKey).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&sharedKey).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create([]model.APIKeyGroup{
		{APIKeyID: exclusiveKey.ID, GroupID: groups[0].ID},
		{APIKeyID: sharedKey.ID, GroupID: groups[0].ID},
		{APIKeyID: sharedKey.ID, GroupID: groups[2].ID},
	}).Error; err != nil {
		t.Fatal(err)
	}

	exclusiveAccount := model.UpstreamAccount{GroupID: groups[0].ID, Name: "exclusive-account", Platform: model.PlatformOpenAI, AuthType: model.AuthAPIKey, Status: model.StatusActive}
	sharedAccount := model.UpstreamAccount{GroupID: groups[0].ID, Name: "shared-account", Platform: model.PlatformOpenAI, AuthType: model.AuthAPIKey, Status: model.StatusActive}
	if err := db.Create(&exclusiveAccount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&sharedAccount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create([]model.UpstreamAccountGroup{
		{UpstreamAccountID: exclusiveAccount.ID, GroupID: groups[0].ID},
		{UpstreamAccountID: sharedAccount.ID, GroupID: groups[0].ID},
		{UpstreamAccountID: sharedAccount.ID, GroupID: groups[2].ID},
	}).Error; err != nil {
		t.Fatal(err)
	}

	subscription := model.UserGroupSubscription{UserID: user.ID, GroupID: groups[0].ID, ExpiresAt: time.Now().Add(24 * time.Hour)}
	rate := model.UserGroupRate{UserID: user.ID, GroupID: groups[0].ID, RateMultiplier: .8}
	imageModel := model.ModelConfig{Name: "image-route", Platform: model.PlatformOpenAI, Kind: "image", ImageGroupID: groups[0].ID, Status: model.StatusActive}
	rule := model.AlertRule{Name: "source-alert", Enabled: true, Condition: "down", GroupID: groups[0].ID}
	for _, value := range []any{&subscription, &rate, &imageModel, &rule} {
		if err := db.Create(value).Error; err != nil {
			t.Fatal(err)
		}
	}

	dependencies := httptest.NewRecorder()
	dependenciesCtx, _ := gin.CreateTestContext(dependencies)
	dependenciesCtx.Request = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/admin/groups/%d/dependencies", groups[0].ID), nil)
	dependenciesCtx.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(groups[0].ID, 10)}}
	(&AdminHandler{db: db}).GroupDependencies(dependenciesCtx)
	if dependencies.Code != http.StatusOK || !strings.Contains(dependencies.Body.String(), `"exclusive_api_keys":1`) || !strings.Contains(dependencies.Body.String(), `"shared_accounts":1`) {
		t.Fatalf("dependencies status=%d body=%s", dependencies.Code, dependencies.Body.String())
	}

	recorder, ctx := deleteGroupRequest(groups[0].ID, fmt.Sprintf("?force=true&target_group_id=%d", groups[1].ID))
	(&AdminHandler{db: db}).DeleteGroup(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if err := db.First(&model.Group{}, groups[0].ID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("source group still exists: %v", err)
	}

	for _, check := range []struct {
		name      string
		id        int64
		wantGroup int64
		model     any
	}{
		{name: "exclusive key", id: exclusiveKey.ID, wantGroup: groups[1].ID, model: &model.APIKey{}},
		{name: "shared key", id: sharedKey.ID, wantGroup: groups[2].ID, model: &model.APIKey{}},
		{name: "exclusive account", id: exclusiveAccount.ID, wantGroup: groups[1].ID, model: &model.UpstreamAccount{}},
		{name: "shared account", id: sharedAccount.ID, wantGroup: groups[2].ID, model: &model.UpstreamAccount{}},
	} {
		if err := db.First(check.model, check.id).Error; err != nil {
			t.Fatalf("reload %s: %v", check.name, err)
		}
		var groupID int64
		switch row := check.model.(type) {
		case *model.APIKey:
			groupID = row.GroupID
		case *model.UpstreamAccount:
			groupID = row.GroupID
		}
		if groupID != check.wantGroup {
			t.Fatalf("%s group=%d want=%d", check.name, groupID, check.wantGroup)
		}
	}

	for name, query := range map[string]any{
		"subscription": &model.UserGroupSubscription{},
		"rate":         &model.UserGroupRate{},
		"alert":        &model.AlertRule{},
	} {
		var count int64
		if err := db.Model(query).Where("group_id = ?", groups[1].ID).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("migrated %s count=%d err=%v", name, count, err)
		}
	}
	var migratedModel model.ModelConfig
	if err := db.First(&migratedModel, imageModel.ID).Error; err != nil || migratedModel.ImageGroupID != groups[1].ID {
		t.Fatalf("image model=%#v err=%v", migratedModel, err)
	}
}

func TestDeleteGroupCanRemoveExclusiveResourcesAfterExplicitConfirmation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newGroupDeleteTestDB(t)
	group := model.Group{Name: "remove-exclusive", Platform: model.PlatformAnthropic, Status: model.StatusActive}
	user := model.User{Email: "remove-exclusive@example.test", PasswordHash: "hash", Role: model.RoleUser, Status: model.StatusActive}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	key := model.APIKey{UserID: user.ID, GroupID: group.ID, KeyHash: "remove-exclusive-hash", KeyPreview: "dd-remove", Name: "remove", Status: model.StatusActive}
	account := model.UpstreamAccount{GroupID: group.ID, Name: "remove-account", Platform: model.PlatformAnthropic, AuthType: model.AuthAPIKey, Status: model.StatusActive}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.APIKeyGroup{APIKeyID: key.ID, GroupID: group.ID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.UpstreamAccountGroup{UpstreamAccountID: account.ID, GroupID: group.ID}).Error; err != nil {
		t.Fatal(err)
	}

	recorder, ctx := deleteGroupRequest(group.ID, "?force=true")
	(&AdminHandler{db: db}).DeleteGroup(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	for name, row := range map[string]any{
		"group":   &model.Group{},
		"key":     &model.APIKey{},
		"account": &model.UpstreamAccount{},
	} {
		var id int64
		switch name {
		case "group":
			id = group.ID
		case "key":
			id = key.ID
		case "account":
			id = account.ID
		}
		if err := db.First(row, id).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("%s still exists: %v", name, err)
		}
	}
}

func TestDeleteGroupRejectsCrossPlatformMigration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newGroupDeleteTestDB(t)
	groups := []model.Group{
		{Name: "source-openai", Platform: model.PlatformOpenAI, Status: model.StatusActive},
		{Name: "target-anthropic", Platform: model.PlatformAnthropic, Status: model.StatusActive},
	}
	if err := db.Create(&groups).Error; err != nil {
		t.Fatal(err)
	}

	recorder, ctx := deleteGroupRequest(groups[0].ID, fmt.Sprintf("?force=true&target_group_id=%d", groups[1].ID))
	(&AdminHandler{db: db}).DeleteGroup(ctx)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if err := db.First(&model.Group{}, groups[0].ID).Error; err != nil {
		t.Fatalf("source group was deleted: %v", err)
	}
}
