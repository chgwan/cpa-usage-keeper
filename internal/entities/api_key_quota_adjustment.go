package entities

import "time"

// APIKeyQuotaAdjustment 记录某个限额窗口的临时调整额：只在 WindowKey 对应的周期内生效。
// 周期翻转后旧行自然失效，不回滚任何限额，也不删除历史用量数据。
type APIKeyQuotaAdjustment struct {
	ID          int64     `gorm:"primaryKey"`
	CPAAPIKeyID int64     `gorm:"not null;uniqueIndex:uniq_api_key_quota_adjustments_key_window"`
	Window      string    `gorm:"type:text;not null;uniqueIndex:uniq_api_key_quota_adjustments_key_window"`
	WindowKey   string    `gorm:"type:text;not null"`
	DeltaUSD    float64   `gorm:"column:delta_usd;not null"`
	CreatedAt   time.Time `gorm:"serializer:storageTime;not null"`
	UpdatedAt   time.Time `gorm:"serializer:storageTime;not null"`
}

func (APIKeyQuotaAdjustment) TableName() string { return "api_key_quota_adjustments" }
