// @vitest-environment happy-dom

import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { RankingLeaderboardResults } from '../RankingLeaderboardResults';
import { RANKING_METRICS, type RankingLeaderboardEntry } from '../../types';

globalThis.IS_REACT_ACT_ENVIRONMENT = true;

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, params?: Record<string, string | number>) => params ? `${key}:${JSON.stringify(params)}` : key,
  }),
}));

const metrics = (totalTokens: number, cost: number, requestCount: number) => ({
  total_tokens: totalTokens,
  request_count: requestCount,
  cache_read_rate: 500_000,
  ttft_average: 120_000,
  latency_average: 900_000,
  peak_tpm: 5_000,
  peak_rpm: 50,
  cost,
});

// 榜单原序按 Token 总量降序；花费与请求数的排序与之不同。
const entries: RankingLeaderboardEntry[] = [
  { rank: 1, participant_id: '1', display_name: 'Alpha', avatar_id: 1, value: 9_000, metrics: metrics(9_000, 100, 5) },
  { rank: 2, participant_id: '2', display_name: 'Beta', avatar_id: 2, value: 7_000, metrics: metrics(7_000, 300, 9) },
  { rank: 3, participant_id: '3', display_name: 'Gamma', avatar_id: 3, value: 5_000, metrics: metrics(5_000, 200, 1) },
  { rank: 4, participant_id: '4', display_name: 'Delta', avatar_id: 4, value: 3_000, metrics: metrics(3_000, 400, 3) },
];

describe('RankingLeaderboardResults', () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    container = document.createElement('div');
    document.body.appendChild(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    document.body.replaceChildren();
  });

  const render = async (onEditLocalProfile?: (entry: RankingLeaderboardEntry) => void) => {
    await act(async () => {
      root.render(<RankingLeaderboardResults entries={entries} onEditLocalProfile={onEditLocalProfile} />);
    });
  };
  const rowNames = () => Array.from(container.querySelectorAll('[data-ranking-row] strong')).map((node) => node.textContent);
  const header = (column: string) => container.querySelector(`[data-ranking-sort="${column}"]`)?.closest('th');
  const clickSort = async (column: string) => {
    await act(async () => container.querySelector<HTMLButtonElement>(`[data-ranking-sort="${column}"]`)!.click());
  };

  it('renders every metric column without a score column', async () => {
    await render();

    expect(container.querySelectorAll('[data-ranking-podium-rank]')).toHaveLength(3);
    expect(container.querySelectorAll('[data-ranking-row]')).toHaveLength(4);
    expect(Array.from(container.querySelectorAll('[data-ranking-sort]')).map((node) => node.getAttribute('data-ranking-sort')))
      .toEqual([...RANKING_METRICS]);
    expect(container.querySelector('[data-ranking-sort="score"]')).toBeNull();
    expect(container.textContent).not.toContain('PTS');
    expect(container.querySelector('[data-ranking-participant-column]')?.textContent).toBe('ranking.api_key');
  });

  it('sorts by total tokens descending by default', async () => {
    await render();

    expect(header('total_tokens')?.getAttribute('aria-sort')).toBe('descending');
    expect(header('cost')?.getAttribute('aria-sort')).toBe('none');
    expect(rowNames()).toEqual(['Alpha', 'Beta', 'Gamma', 'Delta']);
    expect(container.querySelector('[data-ranking-podium-rank="1"]')?.textContent).toContain('9.00K');
  });

  it('toggles a column between descending and ascending and restarts at descending on a new column', async () => {
    await render();

    await clickSort('total_tokens');
    expect(header('total_tokens')?.getAttribute('aria-sort')).toBe('ascending');
    expect(rowNames()).toEqual(['Delta', 'Gamma', 'Beta', 'Alpha']);
    await clickSort('total_tokens');
    expect(header('total_tokens')?.getAttribute('aria-sort')).toBe('descending');

    await clickSort('cost');
    expect(header('cost')?.getAttribute('aria-sort')).toBe('descending');
    expect(header('total_tokens')?.getAttribute('aria-sort')).toBe('none');
    expect(rowNames()).toEqual(['Delta', 'Beta', 'Gamma', 'Alpha']);
    // 名次徽标与领奖台仍按 Token 榜单原序。
    expect(Array.from(container.querySelectorAll('[data-ranking-position]')).map((node) => node.textContent)).toEqual(['4', '2', '3', '1']);
    expect(container.querySelector('[data-ranking-podium-rank="1"]')?.textContent).toContain('Alpha');
  });

  it('formats each metric cell from the fixed-point metrics map', async () => {
    await render();
    const firstRow = container.querySelector('[data-ranking-row]')!;
    const cell = (metric: string) => firstRow.querySelector(`[data-ranking-metric-cell="${metric}"]`)?.textContent;

    expect(cell('total_tokens')).toBe('9.00K');
    expect(cell('cache_read_rate')).toBe('50.00%');
    expect(cell('ttft_average')).toBe('120ms');
    expect(cell('peak_rpm')).toBe('10');
    expect(cell('cost')).toBe('$0.0001');
  });

  it('exposes podium and table avatar edits only when the caller provides the action', async () => {
    await render();
    expect(container.querySelectorAll('[data-ranking-local-profile-edit]')).toHaveLength(0);

    const onEditLocalProfile = vi.fn();
    await render(onEditLocalProfile);
    await act(async () => {
      container.querySelector<HTMLButtonElement>('[data-ranking-podium-rank="1"] [data-ranking-local-profile-edit="1"]')!.click();
    });
    await act(async () => {
      container.querySelector<HTMLButtonElement>('tbody [data-ranking-local-profile-edit="4"]')!.click();
    });

    expect(onEditLocalProfile).toHaveBeenNthCalledWith(1, entries[0]);
    expect(onEditLocalProfile).toHaveBeenNthCalledWith(2, entries[3]);
  });
});
