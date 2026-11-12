package migration

import (
	"fmt"

	"cpa-usage-keeper/internal/entities"
	"gorm.io/gorm"
)

// removeRankingMigration 保留上游版本号以维持迁移顺序；fork 继续提供本地排行，
// 因此只清退社区排行身份设置，本地排行表、头像列和周期状态全部保留。
func removeRankingMigration(tx *gorm.DB) error {
	if err := tx.Where("setting_key = ?", "ranking.identity").Delete(&entities.AppSetting{}).Error; err != nil {
		return fmt.Errorf("delete community ranking identity: %w", err)
	}
	return nil
}
