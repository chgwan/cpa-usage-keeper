package migration

import (
	"fmt"

	"cpa-usage-keeper/internal/entities"
	"gorm.io/gorm"
)

// createAPIKeyQuotaAdjustmentsMigration 为旧库补建限额临时调整额表；已存在时跳过。
func createAPIKeyQuotaAdjustmentsMigration(tx *gorm.DB) error {
	if tx.Migrator().HasTable(&entities.APIKeyQuotaAdjustment{}) {
		return nil
	}
	if err := tx.Migrator().CreateTable(&entities.APIKeyQuotaAdjustment{}); err != nil {
		return fmt.Errorf("create api_key_quota_adjustments table: %w", err)
	}
	return nil
}
