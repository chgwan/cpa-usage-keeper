import type { RankingLeaderboardEntry, RankingMetric } from './types';

const formatCompactNumber = (value: number, maximumFractionDigits = 0): string => {
  const absolute = Math.abs(value);
  if (absolute < 1_000) {
    return new Intl.NumberFormat('en-US', { maximumFractionDigits }).format(value);
  }
  if (absolute < 1_000_000) return `${(value / 1_000).toFixed(2)}K`;
  if (absolute < 1_000_000_000) return `${(value / 1_000_000).toFixed(2)}M`;
  return `${(value / 1_000_000_000).toFixed(2)}B`;
};

const formatDuration = (milliseconds: number): string => {
  if (milliseconds < 1_000) return `${Math.round(milliseconds)}ms`;
  const seconds = milliseconds / 1_000;
  if (seconds < 60) {
    return `${seconds.toFixed(seconds < 10 ? 2 : 1).replace(/\.0+$/, '')}s`;
  }
  const minutes = Math.floor(seconds / 60);
  const remainingSeconds = Math.round(seconds % 60);
  return remainingSeconds > 0 ? `${minutes}m ${remainingSeconds}s` : `${minutes}m`;
};

// formatMetricValue 把后端定点整数还原成展示值：比率为 ppm、耗时为微秒、峰值按五分钟桶、费用为 micro-USD。
export const formatMetricValue = (metric: RankingMetric, value: number): string => {
  if (metric === 'cache_read_rate') return `${(value / 10_000).toFixed(2)}%`;
  if (metric === 'ttft_average' || metric === 'latency_average') return formatDuration(value / 1_000);
  if (metric === 'peak_tpm' || metric === 'peak_rpm') return formatCompactNumber(value / 5, 2);
  // 与配额弹窗一致固定 4 位小数。
  if (metric === 'cost') return `$${(value / 1_000_000).toFixed(4)}`;
  return formatCompactNumber(value);
};

export const metricValue = (entry: RankingLeaderboardEntry, metric: RankingMetric): number | undefined => (
  entry.metrics?.[metric]
);

export const formatEntryMetric = (entry: RankingLeaderboardEntry, metric: RankingMetric): string => {
  const value = metricValue(entry, metric);
  return value === undefined ? '—' : formatMetricValue(metric, value);
};
