package keypolicy

import (
	"context"
	"fmt"
	"time"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/repository"
	"cpa-usage-keeper/internal/timeutil"

	"gorm.io/gorm"
	"gorm.io/plugin/dbresolver"
)

// Store 负责 keypolicy 需要的 per-key 费用聚合查询。
type Store struct {
	db *gorm.DB
}

// NewStore 绑定数据库；费用直接汇总入库时按事件计好的 cost_usd。
func NewStore(db *gorm.DB) *Store {
	return &Store{db: db}
}

// usageCostRow 是按 key 分组的一行费用聚合结果。
type usageCostRow struct {
	APIKeyID int64   `gorm:"column:api_key_id"`
	CostUSD  float64 `gorm:"column:cost_usd"`
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

// queryWindowRows 汇总单个窗口内每个 key 的已存事件费用，与看板费用口径一致；
// 尚未回填费用（cost_usd 为 NULL）或缺价的事件按 0 计。
func (s *Store) queryWindowRows(ctx context.Context, w Window) ([]usageCostRow, error) {
	predicate, args := windowPredicate(w.Start, w.End)
	query := fmt.Sprintf(`
SELECT
	keys.id AS api_key_id,
	COALESCE(SUM(events.cost_usd), 0) AS cost_usd
FROM usage_events AS events
JOIN cpa_api_keys AS keys ON keys.api_key = TRIM(events.api_group_key)
WHERE %s
GROUP BY keys.id
ORDER BY keys.id ASC`, predicate)
	var rows []usageCostRow
	if err := s.db.Clauses(dbresolver.Read).WithContext(ctx).Raw(query, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("aggregate keypolicy usage window %s: %w", WindowKey(w), err)
	}
	return rows, nil
}

// windowResets 是单个窗口当前周期内的清零点：全局一条加若干 per-key。
type windowResets struct {
	global time.Time
	perKey map[int64]time.Time
}

// loadResets 读取三个窗口当前周期的清零点；过期周期的旧行不参与统计。
func (s *Store) loadResets(windows []Window) (map[LimitWindow]windowResets, error) {
	windowKeys := make(map[string]string, len(windows))
	for _, w := range windows {
		windowKeys[string(w.Kind)] = WindowKey(w)
	}
	rows, err := repository.ListAPIKeyQuotaResets(s.db, windowKeys)
	if err != nil {
		return nil, fmt.Errorf("load api key quota resets: %w", err)
	}
	result := make(map[LimitWindow]windowResets, len(windows))
	for _, row := range rows {
		resets := result[LimitWindow(row.Window)]
		if row.CPAAPIKeyID == entities.APIKeyQuotaResetGlobalID {
			resets.global = row.ResetAt
		} else {
			if resets.perKey == nil {
				resets.perKey = map[int64]time.Time{}
			}
			resets.perKey[row.CPAAPIKeyID] = row.ResetAt
		}
		result[LimitWindow(row.Window)] = resets
	}
	return result, nil
}

// laterOf 返回较晚的时间；零值视为不存在。
func laterOf(base, candidate time.Time) time.Time {
	if candidate.After(base) {
		return candidate
	}
	return base
}

// PerKeyUsage 返回每个 key 在三个窗口内的费用，供 runner 一次评估全部策略。
// 清零点只把窗口起点后移：每个 key 只统计 max(窗口起点, 全局重置, 自身重置) 之后的用量，
// usage_events 本身不被修改，排行等其他统计不受影响。
func (s *Store) PerKeyUsage(ctx context.Context, daily, weekly, monthly Window) (map[int64]UsageByWindow, error) {
	result := make(map[int64]UsageByWindow)
	windows := []Window{daily, weekly, monthly}
	resets, err := s.loadResets(windows)
	if err != nil {
		return nil, err
	}
	accumulate := func(kind LimitWindow, rows []usageCostRow, include func(int64) bool) {
		for _, row := range rows {
			if !include(row.APIKeyID) {
				continue
			}
			usage := result[row.APIKeyID]
			if usage == nil {
				usage = UsageByWindow{}
				result[row.APIKeyID] = usage
			}
			windowUsage := usage[kind]
			windowUsage.CostUSD += row.CostUSD
			usage[kind] = windowUsage
		}
	}
	for _, w := range windows {
		windowReset := resets[w.Kind]
		defaultStart := laterOf(w.Start, windowReset.global)
		// 只有晚于默认起点的 per-key 清零点才需要单独起点；按起点分组复用查询。
		overrides := make(map[int64]time.Time)
		starts := make(map[int64]time.Time)
		for keyID, resetAt := range windowReset.perKey {
			if resetAt.After(defaultStart) {
				overrides[keyID] = resetAt
				starts[resetAt.UnixNano()] = resetAt
			}
		}
		rows, err := s.queryWindowRows(ctx, Window{Kind: w.Kind, Start: defaultStart, End: w.End})
		if err != nil {
			return nil, err
		}
		accumulate(w.Kind, rows, func(keyID int64) bool {
			_, overridden := overrides[keyID]
			return !overridden
		})
		for _, start := range starts {
			rows, err := s.queryWindowRows(ctx, Window{Kind: w.Kind, Start: start, End: w.End})
			if err != nil {
				return nil, err
			}
			accumulate(w.Kind, rows, func(keyID int64) bool {
				return overrides[keyID].Equal(start)
			})
		}
	}
	return result, nil
}

// ActiveAdjustments 一次性加载三个窗口当前周期生效的调整额，按 key 分组返回；
// 没有调整的 key 不出现在返回的 map 里（nil Adjustments 对 ApplyAdjustments 是安全的）。
func (s *Store) ActiveAdjustments(daily, weekly, monthly Window) (map[int64]Adjustments, error) {
	windows := []Window{daily, weekly, monthly}
	windowKeys := make(map[string]string, len(windows))
	for _, w := range windows {
		windowKeys[string(w.Kind)] = WindowKey(w)
	}
	rows, err := repository.ListActiveAPIKeyQuotaAdjustments(s.db, windowKeys)
	if err != nil {
		return nil, fmt.Errorf("load active api key quota adjustments: %w", err)
	}
	result := make(map[int64]Adjustments, len(rows))
	for _, row := range rows {
		adjustments := result[row.CPAAPIKeyID]
		if adjustments == nil {
			adjustments = Adjustments{}
			result[row.CPAAPIKeyID] = adjustments
		}
		adjustments[LimitWindow(row.Window)] = row.DeltaUSD
	}
	return result, nil
}

// SingleKeyUsage 只评估单个 key，供策略查询接口使用。
func (s *Store) SingleKeyUsage(ctx context.Context, cpaAPIKeyID int64, daily, weekly, monthly Window) (UsageByWindow, error) {
	all, err := s.PerKeyUsage(ctx, daily, weekly, monthly)
	if err != nil {
		return nil, err
	}
	if usage, ok := all[cpaAPIKeyID]; ok {
		return usage, nil
	}
	return UsageByWindow{}, nil
}
