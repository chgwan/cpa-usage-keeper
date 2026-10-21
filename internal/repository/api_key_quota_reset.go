package repository

import (
	"cpa-usage-keeper/internal/entities"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/plugin/dbresolver"
)

// UpsertAPIKeyQuotaResets 按 (key, window) 覆盖写入清零点：同一窗口只保留最近一次重置。
func UpsertAPIKeyQuotaResets(db *gorm.DB, rows []entities.APIKeyQuotaReset) error {
	if len(rows) == 0 {
		return nil
	}
	return db.Clauses(dbresolver.Write).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "cpa_api_key_id"}, {Name: "window"}},
		DoUpdates: clause.AssignmentColumns([]string{"window_key", "reset_at", "updated_at"}),
	}).Create(&rows).Error
}

// ListAPIKeyQuotaResets 返回给定窗口当前周期内的清零点；windowKeys 以窗口名映射到当前周期键。
func ListAPIKeyQuotaResets(db *gorm.DB, windowKeys map[string]string) ([]entities.APIKeyQuotaReset, error) {
	if len(windowKeys) == 0 {
		return nil, nil
	}
	query := db.Clauses(dbresolver.Read).Model(&entities.APIKeyQuotaReset{})
	conditions := db.Where("1 = 0")
	for window, key := range windowKeys {
		conditions = conditions.Or("window = ? AND window_key = ?", window, key)
	}
	var rows []entities.APIKeyQuotaReset
	if err := query.Where(conditions).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// PruneStaleAPIKeyQuotaResets 删除不属于当前周期的清零点；它们已不参与统计，只是占位。
func PruneStaleAPIKeyQuotaResets(db *gorm.DB, windowKeys map[string]string) error {
	if len(windowKeys) == 0 {
		return nil
	}
	conditions := db.Where("1 = 0")
	for window, key := range windowKeys {
		conditions = conditions.Or("window = ? AND window_key <> ?", window, key)
	}
	return db.Clauses(dbresolver.Write).Where(conditions).Delete(&entities.APIKeyQuotaReset{}).Error
}
