import { describe, expect, it } from 'vitest';
import { formatEntryMetric, formatMetricValue } from '../format';
import type { RankingLeaderboardEntry } from '../types';

describe('ranking value formatting', () => {
  it('uses compact English units for count and token metrics', () => {
    expect(formatMetricValue('total_tokens', 999)).toBe('999');
    expect(formatMetricValue('total_tokens', 1_500)).toBe('1.50K');
    expect(formatMetricValue('request_count', 2_500_000)).toBe('2.50M');
    expect(formatMetricValue('total_tokens', 3_200_000_000)).toBe('3.20B');
  });

  it('decodes fixed-point rates, durations, peaks and costs', () => {
    expect(formatMetricValue('cache_read_rate', 800_000)).toBe('80.00%');
    expect(formatMetricValue('ttft_average', 125_000)).toBe('125ms');
    expect(formatMetricValue('latency_average', 2_500_000)).toBe('2.50s');
    expect(formatMetricValue('peak_tpm', 5_000)).toBe('1.00K');
    expect(formatMetricValue('peak_rpm', 12)).toBe('2.4');
    expect(formatMetricValue('cost', 1_234_567)).toBe('$1.2346');
  });

  it('renders a dash for metrics missing from an entry', () => {
    const entry: RankingLeaderboardEntry = {
      rank: 1, participant_id: '1', display_name: 'Alpha', avatar_id: 1, value: 10, metrics: { total_tokens: 10 },
    };
    expect(formatEntryMetric(entry, 'total_tokens')).toBe('10');
    expect(formatEntryMetric(entry, 'cost')).toBe('—');
  });
});
