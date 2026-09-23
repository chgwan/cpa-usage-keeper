package ranking

import (
	"context"
	"fmt"

	"cpa-usage-keeper/internal/helper"
	"cpa-usage-keeper/internal/pricing"
	"gorm.io/plugin/dbresolver"
)

// localRankingCostRow 是费用维度按 (key, model) 分组的一行聚合结果，字段与 keypolicy 的聚合口径一致。
type localRankingCostRow struct {
	APIKeyID            int64  `gorm:"column:api_key_id"`
	APIGroupKey         string `gorm:"column:api_group_key"`
	Model               string `gorm:"column:model"`
	InputTokens         int64  `gorm:"column:input_tokens"`
	OutputTokens        int64  `gorm:"column:output_tokens"`
	CacheReadTokens     int64  `gorm:"column:cache_read_tokens"`
	CacheCreationTokens int64  `gorm:"column:cache_creation_tokens"`
}

// perKeyCostUSD 聚合窗口内 (key, model) 用量并按当前价格快照计价，返回每个 API Key 的周期 USD 费用。
// 价格目录为 nil 时费用恒为 0，也不再扫描 usage_events。
func (s *LocalRankingService) perKeyCostUSD(ctx context.Context, window localRankingPeriodWindow) (map[int64]float64, error) {
	if s.catalog == nil {
		return map[int64]float64{}, nil
	}
	if !window.End.After(window.Start) {
		return map[int64]float64{}, nil
	}
	predicate, arguments := rankingTimeRangePredicate(window.Start, window.End)
	query := fmt.Sprintf(`
SELECT
	keys.id AS api_key_id,
	TRIM(events.api_group_key) AS api_group_key,
	events.model AS model,
	SUM(events.input_tokens) AS input_tokens,
	SUM(events.output_tokens) AS output_tokens,
	SUM(events.cache_read_tokens) AS cache_read_tokens,
	SUM(events.cache_creation_tokens) AS cache_creation_tokens
FROM usage_events AS events
JOIN cpa_api_keys AS keys ON keys.api_key = TRIM(events.api_group_key)
WHERE %s
GROUP BY keys.id, events.model
ORDER BY keys.id ASC`, predicate)
	var rows []localRankingCostRow
	if err := s.db.Clauses(dbresolver.Read).WithContext(ctx).Raw(query, arguments...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("aggregate local ranking cost window %s: %w", window.Key, err)
	}
	// 一次榜单读取固定一个 Resolver 快照，避免中途换价。
	resolver := s.catalog.NewResolver()
	result := make(map[int64]float64, len(rows))
	for _, row := range rows {
		subject := pricing.NewCostSubject(pricing.UsageDimensions{
			APIGroupKey: row.APIGroupKey,
			Model:       row.Model,
		}, helper.UsageTokenCostInput{
			InputTokens:         row.InputTokens,
			OutputTokens:        row.OutputTokens,
			CacheReadTokens:     row.CacheReadTokens,
			CacheCreationTokens: row.CacheCreationTokens,
		})
		result[row.APIKeyID] += resolver.Calculate(subject).Cost.TotalCostUSD
	}
	return result, nil
}

// attachLocalRankingCost 把窗口费用写回榜单人口行；缺失的 key 保持 0。
func (s *LocalRankingService) attachLocalRankingCost(ctx context.Context, window localRankingPeriodWindow, rows []localRankingPopulationRow) error {
	costs, err := s.perKeyCostUSD(ctx, window)
	if err != nil {
		return err
	}
	for index := range rows {
		rows[index].CostUSD = costs[rows[index].APIKeyID]
	}
	return nil
}
