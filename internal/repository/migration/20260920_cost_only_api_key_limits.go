package migration

import (
	"encoding/json"
	"fmt"

	"cpa-usage-keeper/internal/entities"
	"gorm.io/gorm"
)

// legacyAPIKeyLimit 是策略表 Limits JSON 的本地快照结构：migration 语义必须冻结，
// 不依赖 keypolicy 包的当前定义（后者随功能演进，且经 repository 存在环依赖风险）。
type legacyAPIKeyLimit struct {
	Type   string  `json:"type"`
	Window string  `json:"window"`
	Value  float64 `json:"value"`
}

// costOnlyAPIKeyLimitsMigration 从既有策略里剥离已下线的 tokens / requests 维度，
// 只保留 cost 限额。损坏的 JSON 保持原样，由保存入口的校验兜底。
func costOnlyAPIKeyLimitsMigration(tx *gorm.DB) error {
	if !tx.Migrator().HasTable(&entities.CPAAPIKeyPolicy{}) {
		return nil
	}
	var rows []entities.CPAAPIKeyPolicy
	if err := tx.Find(&rows).Error; err != nil {
		return fmt.Errorf("load api key policies: %w", err)
	}
	for _, row := range rows {
		var limits []legacyAPIKeyLimit
		if err := json.Unmarshal([]byte(row.Limits), &limits); err != nil {
			continue
		}
		kept := make([]legacyAPIKeyLimit, 0, len(limits))
		for _, limit := range limits {
			if limit.Type == "cost" {
				kept = append(kept, limit)
			}
		}
		if len(kept) == len(limits) {
			continue
		}
		encoded, err := json.Marshal(kept)
		if err != nil {
			return fmt.Errorf("encode cost-only limits for key %d: %w", row.CPAAPIKeyID, err)
		}
		if err := tx.Model(&entities.CPAAPIKeyPolicy{}).Where("id = ?", row.ID).
			Update("limits", string(encoded)).Error; err != nil {
			return fmt.Errorf("update limits for key %d: %w", row.CPAAPIKeyID, err)
		}
	}
	return nil
}
