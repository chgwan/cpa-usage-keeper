package test

import (
	"path/filepath"
	"testing"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/repository/migration"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestCostOnlyAPIKeyLimitsMigrationStripsLegacyDimensions 验证升级时策略表里
// 已下线的 tokens / requests 限额被剥离，cost 限额原样保留，损坏 JSON 不阻塞迁移。
func TestCostOnlyAPIKeyLimitsMigrationStripsLegacyDimensions(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "legacy.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	closeMigrationTestDatabase(t, db)
	if err := db.Migrator().CreateTable(&entities.CPAAPIKeyPolicy{}); err != nil {
		t.Fatal(err)
	}
	seed := func(keyID int64, limits string) {
		t.Helper()
		if err := db.Create(&entities.CPAAPIKeyPolicy{CPAAPIKeyID: keyID, Limits: limits, Enabled: true, EnforcementState: "active"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	seed(1, `[{"type":"tokens","window":"daily","value":1000},{"type":"cost","window":"monthly","value":5.5}]`)
	seed(2, `[{"type":"requests","window":"daily","value":10}]`)
	seed(3, `[]`)
	seed(4, `{broken json`)
	if err := migration.MarkAllAsApplied(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DELETE FROM schema_migrations WHERE version = ?", "20260920_cost_only_api_key_limits").Error; err != nil {
		t.Fatal(err)
	}
	if err := migration.Run(db); err != nil {
		t.Fatal(err)
	}
	fetch := func(keyID int64) string {
		t.Helper()
		var row entities.CPAAPIKeyPolicy
		if err := db.Where("cpa_api_key_id = ?", keyID).First(&row).Error; err != nil {
			t.Fatal(err)
		}
		return row.Limits
	}
	if got := fetch(1); got != `[{"type":"cost","window":"monthly","value":5.5}]` {
		t.Fatalf("expected tokens limit stripped and cost kept, got %s", got)
	}
	if got := fetch(2); got != `[]` {
		t.Fatalf("expected requests-only limits emptied, got %s", got)
	}
	if got := fetch(3); got != `[]` {
		t.Fatalf("expected empty limits unchanged, got %s", got)
	}
	// 损坏 JSON 无法判定语义：保持原样并留给保存入口的校验兜底。
	if got := fetch(4); got != `{broken json` {
		t.Fatalf("expected malformed limits untouched, got %s", got)
	}
}
