export const RANKING_PERIODS = ['today', 'yesterday', 'current_week', 'previous_week', 'current_month', 'previous_month'] as const;
export type RankingPeriod = (typeof RANKING_PERIODS)[number];

// 表格列顺序即指标顺序；榜单默认按 Token 总量排序。
export const RANKING_METRICS = [
  'total_tokens',
  'request_count',
  'cache_read_rate',
  'ttft_average',
  'latency_average',
  'peak_tpm',
  'peak_rpm',
  'cost',
] as const;
export type RankingMetric = (typeof RANKING_METRICS)[number];

export const DEFAULT_RANKING_METRIC: RankingMetric = 'total_tokens';

export interface RankingLeaderboardEntry {
  rank: number;
  participant_id: string;
  display_name: string;
  key_alias?: string;
  avatar_id: number;
  value: number;
  rate_numerator?: number;
  rate_denominator?: number;
  metrics?: Partial<Record<RankingMetric, number>>;
}

export interface RankingLeaderboardResponse {
  period: RankingPeriod;
  period_key: string;
  metric: RankingMetric;
  generated_at: string;
  stale: boolean;
  entries: RankingLeaderboardEntry[];
}

export interface LocalRankingProfileRequest {
  key_alias: string;
  avatar_id: number;
}

export interface LocalRankingProfileResponse extends LocalRankingProfileRequest {
  participant_id: string;
  display_name: string;
}
