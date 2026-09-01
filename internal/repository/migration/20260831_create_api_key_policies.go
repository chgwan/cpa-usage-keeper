package migration

import (
	"fmt"

	"cpa-usage-keeper/internal/entities"
	"gorm.io/gorm"
)

func createAPIKeyPoliciesMigration(tx *gorm.DB) error {
	// 两张新表对旧库是纯增量，缺哪张补哪张。
	if !tx.Migrator().HasTable(&entities.CPAAPIKeyPolicy{}) {
		if err := tx.Migrator().CreateTable(&entities.CPAAPIKeyPolicy{}); err != nil {
			return fmt.Errorf("create cpa_api_key_policies table: %w", err)
		}
	}
	if !tx.Migrator().HasTable(&entities.APIKeyEnforcementLog{}) {
		if err := tx.Migrator().CreateTable(&entities.APIKeyEnforcementLog{}); err != nil {
			return fmt.Errorf("create api_key_enforcement_logs table: %w", err)
		}
	}
	return nil
}
