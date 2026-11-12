package ranking

import (
	"context"
	"fmt"

	"gorm.io/plugin/dbresolver"
)

// localRankingCostRow 是费用维度按 key 分组的一行聚合结果，口径与 keypolicy 限额一致。
type localRankingCostRow struct {
	APIKeyID int64   `gorm:"column:api_key_id"`
	CostUSD  float64 `gorm:"column:cost_usd"`
}

// perKeyCostUSD 汇总窗口内每个 API Key 的已存事件费用；未回填或缺价的事件按 0 计。
func (s *LocalRankingService) perKeyCostUSD(ctx context.Context, window localRankingPeriodWindow) (map[int64]float64, error) {
	if !window.End.After(window.Start) {
		return map[int64]float64{}, nil
	}
	predicate, arguments := rankingTimeRangePredicate(window.Start, window.End)
	query := fmt.Sprintf(`
SELECT
	keys.id AS api_key_id,
	COALESCE(SUM(events.cost_usd), 0) AS cost_usd
FROM usage_events AS events
JOIN cpa_api_keys AS keys ON keys.api_key = TRIM(events.api_group_key)
WHERE %s
GROUP BY keys.id
ORDER BY keys.id ASC`, predicate)
	var rows []localRankingCostRow
	if err := s.db.Clauses(dbresolver.Read).WithContext(ctx).Raw(query, arguments...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("aggregate local ranking cost window %s: %w", window.Key, err)
	}
	result := make(map[int64]float64, len(rows))
	for _, row := range rows {
		result[row.APIKeyID] = row.CostUSD
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
