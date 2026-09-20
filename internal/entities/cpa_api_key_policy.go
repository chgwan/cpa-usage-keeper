package entities

import "time"

// CPAAPIKeyPolicy 保存单个 CPA API Key 的用量限额策略与执行状态，与 cpa_api_keys 一对一。
type CPAAPIKeyPolicy struct {
	ID int64 `gorm:"primaryKey"`
	// CPAAPIKeyID 指向 cpa_api_keys.id，重新生成 key 只改 key 字符串不改此关联。
	CPAAPIKeyID int64 `gorm:"uniqueIndex:uniq_cpa_api_key_policies_key;not null"`
	// Limits 是 keypolicy.Limits 的 JSON 文本，空策略存 "[]"。
	Limits string `gorm:"type:text;not null;default:'[]'"`
	// Enabled 关闭后 runner 完全忽略该策略（包括自动恢复）。
	Enabled bool `gorm:"not null;default:true"`
	// AdminDisabled 为 true 时自动恢复被禁止，只能手动恢复。
	AdminDisabled bool `gorm:"not null;default:false"`
	// EnforcementState 取 active / disabled_by_quota / disabled_manual。
	EnforcementState string `gorm:"type:text;not null;default:'active'"`
	// DisabledWindowKey 记录禁用时的周期键，恢复时判断是窗口翻转还是策略调整。
	DisabledWindowKey string     `gorm:"type:text;not null;default:''"`
	LastEvaluatedAt   *time.Time `gorm:"serializer:storageTime"`
	CreatedAt         time.Time  `gorm:"serializer:storageTime;not null"`
	UpdatedAt         time.Time  `gorm:"serializer:storageTime;not null"`
}

func (CPAAPIKeyPolicy) TableName() string { return "cpa_api_key_policies" }
