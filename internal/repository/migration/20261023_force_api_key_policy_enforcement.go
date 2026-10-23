package migration

import (
	"fmt"

	"cpa-usage-keeper/internal/entities"
	"gorm.io/gorm"
)

// forceAPIKeyPolicyEnforcementMigration 把所有策略行的 enabled 强制改为 1：限额执行从此始终开启。
// enabled 列本身保留（避免破坏性 schema 变更），只是从今以后不会再被写成 false。
// 全新库在这个版本之前 cpa_api_key_policies 还没创建，必须跳过而不是报错。
func forceAPIKeyPolicyEnforcementMigration(tx *gorm.DB) error {
	if !tx.Migrator().HasTable(&entities.CPAAPIKeyPolicy{}) {
		return nil
	}
	if err := tx.Exec("UPDATE cpa_api_key_policies SET enabled = 1 WHERE enabled <> 1").Error; err != nil {
		return fmt.Errorf("force api key policy enforcement: %w", err)
	}
	return nil
}
