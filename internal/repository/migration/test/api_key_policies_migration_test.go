package test

import (
	"path/filepath"
	"testing"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/repository/migration"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const apiKeyPoliciesMigrationVersion = "20260831_create_api_key_policies"

// newMigrationTestDB 打开一个全新的空 SQLite 库，由调用方决定跑哪一段迁移。
func newMigrationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "api-key-policies.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open migration database: %v", err)
	}
	closeMigrationTestDatabase(t, db)
	return db
}

func TestAPIKeyPolicyTablesCreatedOnMigration(t *testing.T) {
	db := newMigrationTestDB(t) // 复用本包已有 helper：空库跑完整 Run()
	// 空库无法直接跑完整迁移链，先按本包惯例把历史版本标记完成，只让本次迁移真正执行。
	if err := migration.MarkAllAsApplied(db); err != nil {
		t.Fatalf("mark historical migrations applied: %v", err)
	}
	if err := db.Table("schema_migrations").Where("version = ?", apiKeyPoliciesMigrationVersion).Delete(nil).Error; err != nil {
		t.Fatalf("make API key policies migration pending: %v", err)
	}
	if err := migration.Run(db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	for _, table := range []string{"cpa_api_key_policies", "api_key_enforcement_logs"} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("expected table %s to exist", table)
		}
	}
	if !db.Migrator().HasColumn(&entities.CPAAPIKeyPolicy{}, "DisabledWindowKey") {
		t.Fatal("expected cpa_api_key_policies.disabled_window_key column")
	}
}
