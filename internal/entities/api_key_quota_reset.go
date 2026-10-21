package entities

import "time"

// APIKeyQuotaResetGlobalID 是全局重置行的 CPAAPIKeyID 占位：对所有 key 生效。
const APIKeyQuotaResetGlobalID int64 = 0

// APIKeyQuotaReset 记录某个限额窗口的临时清零点：该窗口只统计 ResetAt 之后的用量。
// WindowKey 绑定具体周期（如 2026-W43），周期翻转后旧行自然失效，不删除任何 usage 数据。
type APIKeyQuotaReset struct {
	ID int64 `gorm:"primaryKey"`
	// CPAAPIKeyID 为 0 表示全局重置；同一 key 同一窗口只保留最近一次。
	CPAAPIKeyID int64     `gorm:"not null;uniqueIndex:uniq_api_key_quota_resets_key_window"`
	Window      string    `gorm:"type:text;not null;uniqueIndex:uniq_api_key_quota_resets_key_window"`
	WindowKey   string    `gorm:"type:text;not null"`
	ResetAt     time.Time `gorm:"serializer:storageTime;not null"`
	CreatedAt   time.Time `gorm:"serializer:storageTime;not null"`
	UpdatedAt   time.Time `gorm:"serializer:storageTime;not null"`
}

func (APIKeyQuotaReset) TableName() string { return "api_key_quota_resets" }
