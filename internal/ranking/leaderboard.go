package ranking

import (
	"time"

	"cpa-usage-keeper/internal/timeutil"
)

const (
	MinAvatarID = 1
	MaxAvatarID = 66
)

type LeaderboardPeriod string

const (
	LeaderboardToday         LeaderboardPeriod = "today"
	LeaderboardYesterday     LeaderboardPeriod = "yesterday"
	LeaderboardCurrentWeek   LeaderboardPeriod = "current_week"
	LeaderboardPreviousWeek  LeaderboardPeriod = "previous_week"
	LeaderboardCurrentMonth  LeaderboardPeriod = "current_month"
	LeaderboardPreviousMonth LeaderboardPeriod = "previous_month"
)

// LeaderboardMetric 决定榜单排序与入榜条件；每个条目仍通过 Metrics 携带全部指标。
type LeaderboardMetric string

const (
	MetricTotalTokens    LeaderboardMetric = "total_tokens"
	MetricRequestCount   LeaderboardMetric = "request_count"
	MetricCacheReadRate  LeaderboardMetric = "cache_read_rate"
	MetricTTFTAverage    LeaderboardMetric = "ttft_average"
	MetricLatencyAverage LeaderboardMetric = "latency_average"
	MetricPeakTPM        LeaderboardMetric = "peak_tpm"
	MetricPeakRPM        LeaderboardMetric = "peak_rpm"
	MetricCost           LeaderboardMetric = "cost"
)

type LeaderboardEntry struct {
	Rank            uint16                      `json:"rank"`
	ParticipantID   string                      `json:"participant_id"`
	DisplayName     string                      `json:"display_name"`
	KeyAlias        string                      `json:"key_alias,omitempty"`
	AvatarID        uint8                       `json:"avatar_id"`
	Value           int64                       `json:"value"`
	RateNumerator   int64                       `json:"rate_numerator,omitempty"`
	RateDenominator int64                       `json:"rate_denominator,omitempty"`
	Metrics         map[LeaderboardMetric]int64 `json:"metrics,omitempty"`
}

type Leaderboard struct {
	Period      LeaderboardPeriod  `json:"period"`
	PeriodKey   string             `json:"period_key"`
	Metric      LeaderboardMetric  `json:"metric"`
	GeneratedAt time.Time          `json:"generated_at"`
	Stale       bool               `json:"stale"`
	Entries     []LeaderboardEntry `json:"entries"`
}

// ValidLeaderboardPeriod 校验本地榜单支持的自然日、自然周和自然月周期。
func ValidLeaderboardPeriod(period LeaderboardPeriod) bool {
	switch period {
	case LeaderboardToday, LeaderboardYesterday, LeaderboardCurrentWeek, LeaderboardPreviousWeek, LeaderboardCurrentMonth, LeaderboardPreviousMonth:
		return true
	default:
		return false
	}
}

// ValidLeaderboardMetric 校验本地榜单可用的排序指标。
func ValidLeaderboardMetric(metric LeaderboardMetric) bool {
	switch metric {
	case MetricTotalTokens, MetricRequestCount, MetricCacheReadRate, MetricTTFTAverage, MetricLatencyAverage, MetricPeakTPM, MetricPeakRPM, MetricCost:
		return true
	default:
		return false
	}
}

func rankingTimeRangePredicate(start, end time.Time) (string, []any) {
	// storageTime 使用 Keeper 本地 offset，DST 回拨时文本顺序不等于 instant 顺序。
	// 外层宽范围继续使用 timestamp 索引，strftime 只负责候选行的真实时间复核。
	coarseStart := timeutil.FormatStorageTime(start.Add(-24 * time.Hour))
	coarseEnd := timeutil.FormatStorageTime(end.Add(24 * time.Hour))
	epochExpression := "CAST(strftime('%s', timestamp) AS INTEGER)"
	if end.Nanosecond() == 0 {
		return "timestamp >= ? AND timestamp < ? AND " + epochExpression + " >= ? AND " + epochExpression + " < ?", []any{
			coarseStart, coarseEnd, start.Unix(), end.Unix(),
		}
	}
	return "timestamp >= ? AND timestamp < ? AND " + epochExpression + " >= ? AND (" + epochExpression + " < ? OR (" + epochExpression + " = ? AND timestamp < ?))", []any{
		coarseStart, coarseEnd, start.Unix(), end.Unix(), end.Unix(), timeutil.FormatStorageTime(end),
	}
}
