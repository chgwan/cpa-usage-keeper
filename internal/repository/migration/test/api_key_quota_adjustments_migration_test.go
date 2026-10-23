package test

import (
	"testing"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/repository/migration"
)

const (
	apiKeyQuotaAdjustmentsMigrationVersion       = "20261023_create_api_key_quota_adjustments"
	forceAPIKeyPolicyEnforcementMigrationVersion = "20261023_force_api_key_policy_enforcement"
)

func TestAPIKeyQuotaAdjustmentsTableCreatedAndEnforcementForcedOnMigration(t *testing.T) {
	db := newMigrationTestDB(t)
	// 只让这两个新版本真正执行：历史版本标记完成后再把它们设为待执行。
	if err := migration.MarkAllAsApplied(db); err != nil {
		t.Fatalf("mark historical migrations applied: %v", err)
	}
	// cpa_api_key_policies 从未被真正创建过（历史 migration 只是标记完成），
	// 这里手动建表并插入一行 enabled=0，验证强制开启迁移能命中真实数据。
	if err := db.Migrator().CreateTable(&entities.CPAAPIKeyPolicy{}); err != nil {
		t.Fatalf("create cpa_api_key_policies table: %v", err)
	}
	policy := entities.CPAAPIKeyPolicy{CPAAPIKeyID: 1, Limits: "[]", Enabled: false, EnforcementState: "active"}
	if err := db.Create(&policy).Error; err != nil {
		t.Fatalf("seed disabled-enforcement policy: %v", err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		for _, version := range []string{apiKeyQuotaAdjustmentsMigrationVersion, forceAPIKeyPolicyEnforcementMigrationVersion} {
			if err := db.Table("schema_migrations").Where("version = ?", version).Delete(nil).Error; err != nil {
				t.Fatalf("make %s migration pending: %v", version, err)
			}
		}
		if err := migration.Run(db); err != nil {
			t.Fatalf("run migrations (attempt %d): %v", attempt+1, err)
		}
	}

	if !db.Migrator().HasTable("api_key_quota_adjustments") {
		t.Fatalf("expected api_key_quota_adjustments table to exist")
	}
	if !db.Migrator().HasIndex("api_key_quota_adjustments", "uniq_api_key_quota_adjustments_key_window") {
		t.Fatalf("expected unique (key, window) index on api_key_quota_adjustments")
	}

	var reloaded entities.CPAAPIKeyPolicy
	if err := db.Where("id = ?", policy.ID).First(&reloaded).Error; err != nil {
		t.Fatalf("reload policy: %v", err)
	}
	if !reloaded.Enabled {
		t.Fatalf("expected enforcement to be forced on, got enabled=%v", reloaded.Enabled)
	}
}

func TestForceAPIKeyPolicyEnforcementMigrationSkipsMissingTable(t *testing.T) {
	db := newMigrationTestDB(t)
	// 全新库里 cpa_api_key_policies 从未创建过：强制开启迁移必须安全跳过而不是报错。
	if err := migration.MarkAllAsApplied(db); err != nil {
		t.Fatalf("mark historical migrations applied: %v", err)
	}
	for _, version := range []string{apiKeyQuotaAdjustmentsMigrationVersion, forceAPIKeyPolicyEnforcementMigrationVersion} {
		if err := db.Table("schema_migrations").Where("version = ?", version).Delete(nil).Error; err != nil {
			t.Fatalf("make %s migration pending: %v", version, err)
		}
	}
	if err := migration.Run(db); err != nil {
		t.Fatalf("run migrations on fresh database without policies table: %v", err)
	}
}
