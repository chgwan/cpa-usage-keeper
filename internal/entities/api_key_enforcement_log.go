package entities

import "time"

// APIKeyEnforcementLog 记录每条执行动作的审计快照，只插入不更新。
type APIKeyEnforcementLog struct {
	ID int64 `gorm:"primaryKey"`
	// CPAAPIKeyID 关联 cpa_api_keys.id，key 删除时策略清理但日志保留。
	CPAAPIKeyID int64     `gorm:"not null;index:idx_api_key_enforcement_logs_key"`
	Action      string    `gorm:"type:text;not null"`
	Reason      string    `gorm:"type:text;not null"`
	LimitType   *string   `gorm:"type:text"`
	Window      *string   `gorm:"type:text"`
	UsedValue   *float64  `gorm:"not null;default:0"`
	LimitValue  *float64  `gorm:"not null;default:0"`
	Detail      string    `gorm:"type:text;not null;default:''"`
	CreatedAt   time.Time `gorm:"serializer:storageTime;not null;index:idx_api_key_enforcement_logs_created"`
}

func (APIKeyEnforcementLog) TableName() string { return "api_key_enforcement_logs" }
