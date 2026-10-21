package test

import (
	"testing"

	"cpa-usage-keeper/internal/repository/migration"
)

const apiKeyQuotaResetsMigrationVersion = "20261021_create_api_key_quota_resets"

func TestAPIKeyQuotaResetsTableCreatedOnMigrationAndRerunIsSafe(t *testing.T) {
	db := newMigrationTestDB(t)
	// 只让本次迁移真正执行：历史版本标记完成后再把它设为待执行。
	if err := migration.MarkAllAsApplied(db); err != nil {
		t.Fatalf("mark historical migrations applied: %v", err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := db.Table("schema_migrations").Where("version = ?", apiKeyQuotaResetsMigrationVersion).Delete(nil).Error; err != nil {
			t.Fatalf("make quota resets migration pending: %v", err)
		}
		if err := migration.Run(db); err != nil {
			t.Fatalf("run migrations (attempt %d): %v", attempt+1, err)
		}
	}
	if !db.Migrator().HasTable("api_key_quota_resets") {
		t.Fatalf("expected api_key_quota_resets table to exist")
	}
	if !db.Migrator().HasIndex("api_key_quota_resets", "uniq_api_key_quota_resets_key_window") {
		t.Fatalf("expected unique (key, window) index on api_key_quota_resets")
	}
}
