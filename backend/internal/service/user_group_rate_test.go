package service

import (
	"testing"

	"dengdeng/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestUserGroupRateResolverCachesAndInvalidates(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.UserGroupRate{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	resolver := NewUserGroupRateResolver(db)

	if got := resolver.Resolve(7, 9, 1.3); got != 1.3 {
		t.Fatalf("missing override = %v, want group default", got)
	}
	if err := db.Create(&model.UserGroupRate{UserID: 7, GroupID: 9, RateMultiplier: 0.6}).Error; err != nil {
		t.Fatalf("create override: %v", err)
	}
	// The cached miss remains until an administrative write explicitly
	// invalidates it. This is the intended hot-path behaviour.
	if got := resolver.Resolve(7, 9, 1.3); got != 1.3 {
		t.Fatalf("cached miss = %v, want group default", got)
	}
	resolver.Invalidate(7, 0)
	if got := resolver.Resolve(7, 9, 1.3); got != 0.6 {
		t.Fatalf("override = %v, want 0.6", got)
	}
}
