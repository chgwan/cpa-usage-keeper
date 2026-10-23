package repository

import (
	"cpa-usage-keeper/internal/entities"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/plugin/dbresolver"
)

// UpsertAPIKeyQuotaAdjustments 按 (key, window) 覆盖写入调整额：再次为同一周期设置只会替换，不会累加。
func UpsertAPIKeyQuotaAdjustments(db *gorm.DB, rows []entities.APIKeyQuotaAdjustment) error {
	if len(rows) == 0 {
		return nil
	}
	return db.Clauses(dbresolver.Write).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "cpa_api_key_id"}, {Name: "window"}},
		DoUpdates: clause.AssignmentColumns([]string{"window_key", "delta_usd", "updated_at"}),
	}).Create(&rows).Error
}

// ListActiveAPIKeyQuotaAdjustments 返回给定窗口当前周期内生效的调整额；windowKeys 以窗口名映射到当前周期键。
func ListActiveAPIKeyQuotaAdjustments(db *gorm.DB, windowKeys map[string]string) ([]entities.APIKeyQuotaAdjustment, error) {
	if len(windowKeys) == 0 {
		return nil, nil
	}
	query := db.Clauses(dbresolver.Read).Model(&entities.APIKeyQuotaAdjustment{})
	conditions := db.Where("1 = 0")
	for window, key := range windowKeys {
		conditions = conditions.Or("window = ? AND window_key = ?", window, key)
	}
	var rows []entities.APIKeyQuotaAdjustment
	if err := query.Where(conditions).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// DeleteAPIKeyQuotaAdjustments 按 key id × window 清除调整额；用于提前手动清除当前生效的临时调整。
func DeleteAPIKeyQuotaAdjustments(db *gorm.DB, ids []int64, windows []string) error {
	if len(ids) == 0 || len(windows) == 0 {
		return nil
	}
	return db.Clauses(dbresolver.Write).
		Where("cpa_api_key_id IN ?", ids).
		Where("window IN ?", windows).
		Delete(&entities.APIKeyQuotaAdjustment{}).Error
}
