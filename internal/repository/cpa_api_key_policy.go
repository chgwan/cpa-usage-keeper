package repository

import (
	"errors"
	"time"

	"cpa-usage-keeper/internal/entities"

	"gorm.io/gorm"
	"gorm.io/plugin/dbresolver"
)

// CPAAPIKeyPolicyRow 是 runner 需要的策略+key 联合视图；策略字段直接内嵌，key 字段来自 join。
type CPAAPIKeyPolicyRow struct {
	entities.CPAAPIKeyPolicy
	APIKey   string
	KeyAlias string
}

// UpsertCPAAPIKeyPolicy 先 UPDATE 后 INSERT，避免 SQLite ON CONFLICT 方言。
func UpsertCPAAPIKeyPolicy(db *gorm.DB, policy *entities.CPAAPIKeyPolicy) error {
	return db.Clauses(dbresolver.Write).Transaction(func(tx *gorm.DB) error {
		var existing entities.CPAAPIKeyPolicy
		err := tx.Where("cpa_api_key_id = ?", policy.CPAAPIKeyID).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Create(policy).Error
		}
		if err != nil {
			return err
		}
		policy.ID = existing.ID
		policy.CreatedAt = existing.CreatedAt
		return tx.Model(&entities.CPAAPIKeyPolicy{}).Where("id = ?", existing.ID).Updates(map[string]any{
			"limits":              policy.Limits,
			"enabled":             policy.Enabled,
			"admin_disabled":      policy.AdminDisabled,
			"enforcement_state":   policy.EnforcementState,
			"disabled_window_key": policy.DisabledWindowKey,
			"last_evaluated_at":   policy.LastEvaluatedAt,
			"updated_at":          time.Now(),
		}).Error
	})
}

// FindCPAAPIKeyPolicy 按 key id 读取策略，不存在时返回 ErrRecordNotFound。
func FindCPAAPIKeyPolicy(db *gorm.DB, cpaAPIKeyID int64) (entities.CPAAPIKeyPolicy, error) {
	var policy entities.CPAAPIKeyPolicy
	err := db.Clauses(dbresolver.Read).Where("cpa_api_key_id = ?", cpaAPIKeyID).First(&policy).Error
	return policy, err
}

// ListEnabledCPAAPIKeyPolicies 只返回启用策略，并带上 runner 需要的 key 字符串。
func ListEnabledCPAAPIKeyPolicies(db *gorm.DB) ([]CPAAPIKeyPolicyRow, error) {
	var rows []CPAAPIKeyPolicyRow
	err := db.Clauses(dbresolver.Read).Raw(`
SELECT policies.id, policies.cpa_api_key_id, policies.limits, policies.enabled,
       policies.admin_disabled, policies.enforcement_state, policies.disabled_window_key,
       policies.last_evaluated_at, policies.created_at, policies.updated_at,
       keys.api_key AS api_key, keys.key_alias AS key_alias
FROM cpa_api_key_policies AS policies
JOIN cpa_api_keys AS keys ON keys.id = policies.cpa_api_key_id AND keys.is_deleted = 0
WHERE policies.enabled = 1
ORDER BY policies.cpa_api_key_id ASC`).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// UpdateCPAAPIKeyPolicyRuntime 只更新 runner 会碰的运行时字段。
func UpdateCPAAPIKeyPolicyRuntime(db *gorm.DB, cpaAPIKeyID int64, state string, disabledWindowKey string, lastEvaluatedAt time.Time) error {
	return db.Clauses(dbresolver.Write).Model(&entities.CPAAPIKeyPolicy{}).
		Where("cpa_api_key_id = ?", cpaAPIKeyID).
		Updates(map[string]any{
			"enforcement_state":   state,
			"disabled_window_key": disabledWindowKey,
			"last_evaluated_at":   lastEvaluatedAt,
			"updated_at":          time.Now(),
		}).Error
}

// UpdateCPAAPIKeyPolicyConfig 只更新管理员可编辑的配置列（limits/enabled）。
// 绝不触碰 runner 拥有的 enforcement_state / admin_disabled / disabled_window_key /
// last_evaluated_at：读-改-写整行回写会把读取时的旧运行时值复活，覆盖 runner 在
// 读与写之间落下的并发禁用（runner 的禁用路径不持有管理服务的互斥锁）。
func UpdateCPAAPIKeyPolicyConfig(db *gorm.DB, cpaAPIKeyID int64, limits string, enabled bool) error {
	return db.Clauses(dbresolver.Write).Model(&entities.CPAAPIKeyPolicy{}).
		Where("cpa_api_key_id = ?", cpaAPIKeyID).
		Updates(map[string]any{
			"limits":     limits,
			"enabled":    enabled,
			"updated_at": time.Now(),
		}).Error
}

// UpdateCPAAPIKeyPolicyLifecycle 写手动禁用/恢复的目标运行时状态：enforcement_state、
// admin_disabled、disabled_window_key、last_evaluated_at 一次落库，limits/enabled 永不触碰。
func UpdateCPAAPIKeyPolicyLifecycle(db *gorm.DB, cpaAPIKeyID int64, state string, disabledWindowKey string, adminDisabled bool, lastEvaluatedAt time.Time) error {
	return db.Clauses(dbresolver.Write).Model(&entities.CPAAPIKeyPolicy{}).
		Where("cpa_api_key_id = ?", cpaAPIKeyID).
		Updates(map[string]any{
			"enforcement_state":   state,
			"admin_disabled":      adminDisabled,
			"disabled_window_key": disabledWindowKey,
			"last_evaluated_at":   lastEvaluatedAt,
			"updated_at":          time.Now(),
		}).Error
}

// DeleteCPAAPIKeyPolicy 在 key 被删除时清掉策略行。
func DeleteCPAAPIKeyPolicy(db *gorm.DB, cpaAPIKeyID int64) error {
	return db.Clauses(dbresolver.Write).Where("cpa_api_key_id = ?", cpaAPIKeyID).
		Delete(&entities.CPAAPIKeyPolicy{}).Error
}

// InsertAPIKeyEnforcementLog 只插入，永不更新审计记录。
func InsertAPIKeyEnforcementLog(db *gorm.DB, log entities.APIKeyEnforcementLog) error {
	return db.Clauses(dbresolver.Write).Create(&log).Error
}

// ListAPIKeyEnforcementLogs 返回指定 key 的审计记录，新事件在前。
func ListAPIKeyEnforcementLogs(db *gorm.DB, cpaAPIKeyID int64, limit int) ([]entities.APIKeyEnforcementLog, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var logs []entities.APIKeyEnforcementLog
	err := db.Clauses(dbresolver.Read).
		Where("cpa_api_key_id = ?", cpaAPIKeyID).
		Order("created_at DESC, id DESC").
		Limit(limit).
		Find(&logs).Error
	return logs, err
}

// LatestAPIKeyEnforcementLogAction 取指定动作的最近一条审计，用于 skipped 去重。
func LatestAPIKeyEnforcementLogAction(db *gorm.DB, cpaAPIKeyID int64, action string) (entities.APIKeyEnforcementLog, error) {
	var log entities.APIKeyEnforcementLog
	err := db.Clauses(dbresolver.Read).
		Where("cpa_api_key_id = ? AND action = ?", cpaAPIKeyID, action).
		Order("created_at DESC, id DESC").
		First(&log).Error
	return log, err
}
