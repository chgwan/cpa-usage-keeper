package keypolicy

import (
	"context"
	"fmt"
	"time"

	"cpa-usage-keeper/internal/helper"
	"cpa-usage-keeper/internal/pricing"
	"cpa-usage-keeper/internal/timeutil"

	"gorm.io/gorm"
	"gorm.io/plugin/dbresolver"
)

// Store 负责 keypolicy 需要的 per-key 用量聚合查询与计价。
type Store struct {
	db      *gorm.DB
	catalog *pricing.Catalog
}

// NewStore 绑定数据库与价格目录；目录为 nil 时费用维度恒为 0。
func NewStore(db *gorm.DB, catalog *pricing.Catalog) *Store {
	return &Store{db: db, catalog: catalog}
}

// usageModelRow 是按 (key, model) 分组的一行聚合结果。
type usageModelRow struct {
	APIKeyID            int64  `gorm:"column:api_key_id"`
	APIGroupKey         string `gorm:"column:api_group_key"`
	Model               string `gorm:"column:model"`
	RequestCount        int64  `gorm:"column:request_count"`
	InputTokens         int64  `gorm:"column:input_tokens"`
	OutputTokens        int64  `gorm:"column:output_tokens"`
	CacheReadTokens     int64  `gorm:"column:cache_read_tokens"`
	CacheCreationTokens int64  `gorm:"column:cache_creation_tokens"`
	TotalTokens         int64  `gorm:"column:total_tokens"`
}

// windowPredicate 复刻 ranking.rankingTimeRangePredicate 的 DST 安全双条件：
// 外层用 storageTime 文本范围走索引，内层用 epoch 秒数复核真实时刻。
func windowPredicate(start, end time.Time) (string, []any) {
	coarseStart := timeutil.FormatStorageTime(start.Add(-24 * time.Hour))
	coarseEnd := timeutil.FormatStorageTime(end.Add(24 * time.Hour))
	epochExpression := "CAST(strftime('%s', timestamp) AS INTEGER)"
	return "timestamp >= ? AND timestamp < ? AND " + epochExpression + " >= ? AND " + epochExpression + " < ?", []any{
		coarseStart, coarseEnd, start.Unix(), end.Unix(),
	}
}

// queryWindowRows 聚合单个窗口内按 (key, model) 分组的用量。
func (s *Store) queryWindowRows(ctx context.Context, w Window) ([]usageModelRow, error) {
	predicate, args := windowPredicate(w.Start, w.End)
	query := fmt.Sprintf(`
SELECT
	keys.id AS api_key_id,
	TRIM(events.api_group_key) AS api_group_key,
	events.model AS model,
	COUNT(*) AS request_count,
	SUM(events.input_tokens) AS input_tokens,
	SUM(events.output_tokens) AS output_tokens,
	SUM(events.cache_read_tokens) AS cache_read_tokens,
	SUM(events.cache_creation_tokens) AS cache_creation_tokens,
	SUM(events.total_tokens) AS total_tokens
FROM usage_events AS events
JOIN cpa_api_keys AS keys ON keys.api_key = TRIM(events.api_group_key)
WHERE %s
GROUP BY keys.id, events.model
ORDER BY keys.id ASC`, predicate)
	var rows []usageModelRow
	if err := s.db.Clauses(dbresolver.Read).WithContext(ctx).Raw(query, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("aggregate keypolicy usage window %s: %w", WindowKey(w), err)
	}
	return rows, nil
}

// priceRow 用固定的 Resolver 快照给一行 (key, model) 聚合计价。
func (s *Store) priceRow(resolver pricing.Resolver, row usageModelRow) float64 {
	subject := pricing.NewCostSubject(pricing.UsageDimensions{
		APIGroupKey: row.APIGroupKey,
		Model:       row.Model,
	}, helper.UsageTokenCostInput{
		InputTokens:         row.InputTokens,
		OutputTokens:        row.OutputTokens,
		CacheReadTokens:     row.CacheReadTokens,
		CacheCreationTokens: row.CacheCreationTokens,
	})
	return resolver.Calculate(subject).Cost.TotalCostUSD
}

// PerKeyUsage 返回每个 key 在两个窗口内的用量，供 runner 一次评估全部策略。
func (s *Store) PerKeyUsage(ctx context.Context, daily, monthly Window) (map[int64]UsageByWindow, error) {
	result := make(map[int64]UsageByWindow)
	var resolver pricing.Resolver
	if s.catalog != nil {
		// 一次评估固定一个快照，避免中途换价。
		resolver = s.catalog.NewResolver()
	}
	for _, w := range []Window{daily, monthly} {
		rows, err := s.queryWindowRows(ctx, w)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			usage := result[row.APIKeyID]
			if usage == nil {
				usage = UsageByWindow{}
				result[row.APIKeyID] = usage
			}
			windowUsage := usage[w.Kind]
			windowUsage.Requests += row.RequestCount
			windowUsage.Tokens += row.TotalTokens
			windowUsage.CostUSD += s.priceRow(resolver, row)
			usage[w.Kind] = windowUsage
		}
	}
	return result, nil
}

// SingleKeyUsage 只评估单个 key，供策略查询接口使用。
func (s *Store) SingleKeyUsage(ctx context.Context, cpaAPIKeyID int64, daily, monthly Window) (UsageByWindow, error) {
	all, err := s.PerKeyUsage(ctx, daily, monthly)
	if err != nil {
		return nil, err
	}
	if usage, ok := all[cpaAPIKeyID]; ok {
		return usage, nil
	}
	return UsageByWindow{}, nil
}
