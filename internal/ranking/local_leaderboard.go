package ranking

import (
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/helper"
)

func buildLocalLeaderboardEntries(rows []localRankingPopulationRow, metric LeaderboardMetric) []LeaderboardEntry {
	type candidate struct {
		apiKeyID int64
		entry    LeaderboardEntry
	}
	candidates := make([]candidate, 0, len(rows))
	if metric == MetricOverall {
		eligibleRows := make([]localRankingPopulationRow, 0, len(rows))
		for _, row := range rows {
			if localOverallEligible(row) {
				eligibleRows = append(eligibleRows, row)
			}
		}
		scores := scoreLocalOverallPopulation(eligibleRows)
		for index, row := range eligibleRows {
			entry := localLeaderboardEntry(row, scores[index])
			entry.Metrics = localLeaderboardMetricsMap(row)
			candidates = append(candidates, candidate{apiKeyID: row.APIKeyID, entry: entry})
		}
	} else {
		for _, row := range rows {
			value, numerator, denominator, eligible := localMetricValue(row, metric)
			if !eligible {
				continue
			}
			entry := localLeaderboardEntry(row, value)
			entry.RateNumerator = numerator
			entry.RateDenominator = denominator
			candidates = append(candidates, candidate{apiKeyID: row.APIKeyID, entry: entry})
		}
	}
	lowerIsBetter := metric == MetricTTFTAverage || metric == MetricLatencyAverage
	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].entry.Value != candidates[right].entry.Value {
			if lowerIsBetter {
				return candidates[left].entry.Value < candidates[right].entry.Value
			}
			return candidates[left].entry.Value > candidates[right].entry.Value
		}
		return candidates[left].apiKeyID < candidates[right].apiKeyID
	})
	if len(candidates) > localRankingTopLimit {
		candidates = candidates[:localRankingTopLimit]
	}
	entries := make([]LeaderboardEntry, len(candidates))
	for index, item := range candidates {
		item.entry.Rank = uint16(index + 1)
		entries[index] = item.entry
	}
	return entries
}

func localLeaderboardEntry(row localRankingPopulationRow, value int64) LeaderboardEntry {
	displayName := helper.CPAAPIKeyDisplayName(entities.CPAAPIKey{
		ID: row.APIKeyID, APIKey: row.APIKey, DisplayKey: row.DisplayKey, KeyAlias: row.KeyAlias,
	})
	avatarID := defaultLocalRankingAvatarID(row.APIKeyID)
	if row.LocalRankingAvatarID != nil && *row.LocalRankingAvatarID >= MinAvatarID && *row.LocalRankingAvatarID <= MaxAvatarID {
		avatarID = *row.LocalRankingAvatarID
	}
	return LeaderboardEntry{
		ParticipantID: strconv.FormatInt(row.APIKeyID, 10), DisplayName: displayName, KeyAlias: strings.TrimSpace(row.KeyAlias), AvatarID: avatarID, Value: value,
	}
}

func defaultLocalRankingAvatarID(apiKeyID int64) uint8 {
	if apiKeyID <= 0 {
		return MinAvatarID
	}
	return uint8(1 + ((apiKeyID - 1) % MaxAvatarID))
}

func localMetricValue(row localRankingPopulationRow, metric LeaderboardMetric) (int64, int64, int64, bool) {
	switch metric {
	case MetricTotalTokens:
		return row.TotalTokens, 0, 0, row.TotalTokens > 0
	case MetricRequestCount:
		return row.RequestCount, 0, 0, row.RequestCount > 0
	case MetricCacheReadRate:
		return scaledRatio(row.CacheReadTokens, row.InputTokens, 1_000_000), row.CacheReadTokens, row.InputTokens, row.InputTokens > 0
	case MetricTTFTAverage:
		return scaledRatio(row.TTFTSumMS, row.TTFTSampleCount, 1_000), row.TTFTSumMS, row.TTFTSampleCount, row.TTFTSampleCount > 0 && row.TTFTSumMS > 0
	case MetricLatencyAverage:
		return scaledRatio(row.LatencySumMS, row.LatencySampleCount, 1_000), row.LatencySumMS, row.LatencySampleCount, row.LatencySampleCount > 0 && row.LatencySumMS > 0
	case MetricPeakTPM:
		return row.Peak5MTotalTokens, 0, 0, row.Peak5MTotalTokens > 0
	case MetricPeakRPM:
		return row.Peak5MRequestCount, 0, 0, row.Peak5MRequestCount > 0
	case MetricCost:
		// 周期内有任何用量即上榜：未命中价格表的 key 费用为 0 但仍列出。
		return localCostMicroUSD(row.CostUSD), 0, 0, row.RequestCount > 0
	default:
		return 0, 0, 0, false
	}
}

func localLeaderboardMetricsMap(row localRankingPopulationRow) map[LeaderboardMetric]int64 {
	return map[LeaderboardMetric]int64{
		MetricTotalTokens:    row.TotalTokens,
		MetricRequestCount:   row.RequestCount,
		MetricCacheReadRate:  scaledRatio(row.CacheReadTokens, row.InputTokens, 1_000_000),
		MetricTTFTAverage:    scaledRatio(row.TTFTSumMS, row.TTFTSampleCount, 1_000),
		MetricLatencyAverage: scaledRatio(row.LatencySumMS, row.LatencySampleCount, 1_000),
		MetricPeakTPM:        row.Peak5MTotalTokens,
		MetricPeakRPM:        row.Peak5MRequestCount,
		MetricCost:           localCostMicroUSD(row.CostUSD),
	}
}

// localCostMicroUSD 把 USD 费用四舍五入成 micro-USD 定点整数，保持榜单 value 的整数语义。
func localCostMicroUSD(cost float64) int64 {
	if math.IsNaN(cost) || math.IsInf(cost, 0) || cost <= 0 {
		return 0
	}
	scaled := cost * 1_000_000
	if scaled >= math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(math.Round(scaled))
}

// validLocalLeaderboardPeriod 在 Community 周期集之外额外接受本地独有的自然周周期。
func validLocalLeaderboardPeriod(period LeaderboardPeriod) bool {
	return localRankingWeekPeriod(period) || validLeaderboardPeriod(period)
}

func localRankingWeekPeriod(period LeaderboardPeriod) bool {
	return period == LeaderboardCurrentWeek || period == LeaderboardPreviousWeek
}

// validLocalLeaderboardMetric 在 Community 指标集之外额外接受本地独有的 cost 维度。
func validLocalLeaderboardMetric(metric LeaderboardMetric) bool {
	return metric == MetricCost || validLeaderboardMetric(metric)
}

func scaledRatio(numerator, denominator, scale int64) int64 {
	if denominator <= 0 || numerator <= 0 || scale <= 0 {
		return 0
	}
	value := new(big.Int).Mul(big.NewInt(numerator), big.NewInt(scale))
	value.Div(value, big.NewInt(denominator))
	if !value.IsInt64() {
		return math.MaxInt64
	}
	return value.Int64()
}

func localRankingPeriodWindows(now time.Time) []localRankingPeriodWindow {
	now = now.In(localRankingPeriodLocation)
	dayStart := localRankingStartOfDay(now)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, localRankingPeriodLocation)
	previousDayStart := dayStart.AddDate(0, 0, -1)
	previousMonthStart := monthStart.AddDate(0, -1, 0)
	return []localRankingPeriodWindow{
		{Period: LeaderboardToday, Kind: entities.LocalRankingPeriodDay, Key: dayStart.Format("2006-01-02"), Start: dayStart, End: now},
		{Period: LeaderboardYesterday, Kind: entities.LocalRankingPeriodDay, Key: previousDayStart.Format("2006-01-02"), Start: previousDayStart, End: dayStart},
		{Period: LeaderboardCurrentMonth, Kind: entities.LocalRankingPeriodMonth, Key: monthStart.Format("2006-01"), Start: monthStart, End: now},
		{Period: LeaderboardPreviousMonth, Kind: entities.LocalRankingPeriodMonth, Key: previousMonthStart.Format("2006-01"), Start: previousMonthStart, End: monthStart},
	}
}

// localRankingWeekWindows 返回本周与上周（周一起始）窗口；周榜没有快照行，读取时直接从 usage_events 现算。
func localRankingWeekWindows(now time.Time) []localRankingPeriodWindow {
	now = now.In(localRankingPeriodLocation)
	dayStart := localRankingStartOfDay(now)
	weekStart := dayStart.AddDate(0, 0, -((int(dayStart.Weekday()) + 6) % 7))
	previousWeekStart := weekStart.AddDate(0, 0, -7)
	return []localRankingPeriodWindow{
		{Period: LeaderboardCurrentWeek, Key: localRankingWeekKey(weekStart), Start: weekStart, End: now},
		{Period: LeaderboardPreviousWeek, Key: localRankingWeekKey(previousWeekStart), Start: previousWeekStart, End: weekStart},
	}
}

func localRankingWeekKey(start time.Time) string {
	year, week := start.ISOWeek()
	return fmt.Sprintf("%04d-W%02d", year, week)
}

func localRankingWindowForPeriod(now time.Time, period LeaderboardPeriod) (localRankingPeriodWindow, bool) {
	if localRankingWeekPeriod(period) {
		for _, window := range localRankingWeekWindows(now) {
			if window.Period == period {
				return window, true
			}
		}
		return localRankingPeriodWindow{}, false
	}
	for _, window := range localRankingPeriodWindows(now) {
		if window.Period == period {
			return window, true
		}
	}
	return localRankingPeriodWindow{}, false
}

func localRankingStartOfDay(now time.Time) time.Time {
	local := now.In(localRankingPeriodLocation)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, localRankingPeriodLocation)
}

func localRankingTodayWindow(now time.Time) localRankingPeriodWindow {
	start := localRankingStartOfDay(now)
	return localRankingPeriodWindow{
		Period: LeaderboardToday, Kind: entities.LocalRankingPeriodDay,
		Key: start.Format("2006-01-02"), Start: start, End: now.In(localRankingPeriodLocation),
	}
}

func localRankingCompleteDayWindow(start time.Time) localRankingPeriodWindow {
	start = localRankingStartOfDay(start)
	return localRankingPeriodWindow{
		Kind: entities.LocalRankingPeriodDay, Key: start.Format("2006-01-02"), Start: start, End: start.AddDate(0, 0, 1),
	}
}

func localRankingMetricsFromRaw(row localRankingRawAggregate) localRankingMetrics {
	return localRankingMetrics{
		RequestCount: row.RequestCount, SuccessCount: row.SuccessCount, FailureCount: row.FailureCount,
		InputTokens: row.InputTokens, CacheReadTokens: row.CacheReadTokens, TotalTokens: row.TotalTokens,
		TTFTSumMS: row.TTFTSumMS, TTFTSampleCount: row.TTFTSampleCount,
		LatencySumMS: row.LatencySumMS, LatencySampleCount: row.LatencySampleCount,
		Peak5MRequestCount: row.Peak5MRequestCount, Peak5MTotalTokens: row.Peak5MTotalTokens,
	}
}

func localRankingMetricsFromStat(row entities.LocalRankingPeriodStat) localRankingMetrics {
	return localRankingMetrics{
		RequestCount: row.RequestCount, SuccessCount: row.SuccessCount, FailureCount: row.FailureCount,
		InputTokens: row.InputTokens, CacheReadTokens: row.CacheReadTokens, TotalTokens: row.TotalTokens,
		TTFTSumMS: row.TTFTSumMS, TTFTSampleCount: row.TTFTSampleCount,
		LatencySumMS: row.LatencySumMS, LatencySampleCount: row.LatencySampleCount,
		Peak5MRequestCount: row.Peak5MRequestCount, Peak5MTotalTokens: row.Peak5MTotalTokens,
	}
}

func localRankingStatFromMetrics(key localRankingStatKey, metrics localRankingMetrics, now time.Time) entities.LocalRankingPeriodStat {
	return entities.LocalRankingPeriodStat{
		PeriodKind: key.Kind, PeriodKey: key.Key, APIKeyID: key.APIKeyID,
		RequestCount: metrics.RequestCount, SuccessCount: metrics.SuccessCount, FailureCount: metrics.FailureCount,
		InputTokens: metrics.InputTokens, CacheReadTokens: metrics.CacheReadTokens, TotalTokens: metrics.TotalTokens,
		TTFTSumMS: metrics.TTFTSumMS, TTFTSampleCount: metrics.TTFTSampleCount,
		LatencySumMS: metrics.LatencySumMS, LatencySampleCount: metrics.LatencySampleCount,
		Peak5MRequestCount: metrics.Peak5MRequestCount, Peak5MTotalTokens: metrics.Peak5MTotalTokens,
		UpdatedAt: now,
	}
}
