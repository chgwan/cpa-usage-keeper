package migration

import (
	"fmt"

	"cpa-usage-keeper/internal/entities"
	"gorm.io/gorm"
)

// createAPIKeyQuotaResetsMigration 为旧库补建限额临时清零点表；已存在时跳过。
func createAPIKeyQuotaResetsMigration(tx *gorm.DB) error {
	if tx.Migrator().HasTable(&entities.APIKeyQuotaReset{}) {
		return nil
	}
	if err := tx.Migrator().CreateTable(&entities.APIKeyQuotaReset{}); err != nil {
		return fmt.Errorf("create api_key_quota_resets table: %w", err)
	}
	return nil
}
