// @vitest-environment happy-dom

import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { RankingLeaderboardResults } from '../RankingLeaderboardResults';
import type { RankingLeaderboardEntry } from '../../types';

globalThis.IS_REACT_ACT_ENVIRONMENT = true;

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, params?: Record<string, string | number>) => params ? `${key}:${JSON.stringify(params)}` : key,
  }),
}));

const entries: RankingLeaderboardEntry[] = [
  { rank: 1, participant_id: '1', display_name: 'Alpha', avatar_id: 1, value: 100 },
  { rank: 2, participant_id: '2', display_name: 'Beta', avatar_id: 2, value: 90 },
  { rank: 3, participant_id: '3', display_name: 'Gamma', avatar_id: 3, value: 80 },
  { rank: 4, participant_id: '4', display_name: 'Delta', avatar_id: 4, value: 70 },
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

  it('renders the same local podium and table as a read-only view without an edit callback', async () => {
    await act(async () => {
      root.render(<RankingLeaderboardResults scope="local" metric="overall" entries={entries} />);
    });

    expect(container.querySelectorAll('[data-ranking-podium-rank]')).toHaveLength(3);
    expect(container.querySelectorAll('[data-ranking-row]')).toHaveLength(4);
    expect(container.querySelectorAll('[data-ranking-local-profile-edit]')).toHaveLength(0);
    expect(container.querySelector('[data-ranking-participant-column]')?.textContent).toBe('ranking.api_key');
  });

  it('exposes both podium and table avatar edits only when the caller provides the action', async () => {
    const onEditLocalProfile = vi.fn();
    await act(async () => {
      root.render(
        <RankingLeaderboardResults
          scope="local"
          metric="overall"
          entries={entries}
          onEditLocalProfile={onEditLocalProfile}
        />,
      );
    });

    await act(async () => {
      container.querySelector<HTMLButtonElement>(
        '[data-ranking-podium-rank="1"] [data-ranking-local-profile-edit="1"]',
      )!.click();
    });
    await act(async () => {
      container.querySelector<HTMLButtonElement>(
        'tbody [data-ranking-local-profile-edit="4"]',
      )!.click();
    });

    expect(onEditLocalProfile).toHaveBeenNthCalledWith(1, entries[0]);
    expect(onEditLocalProfile).toHaveBeenNthCalledWith(2, entries[3]);
  });

  describe('column sorting', () => {
    // 数值取自真实本地榜单：综合分最高的并不是花费或请求数最高的那个 Key。
    const scored: RankingLeaderboardEntry[] = [
      { rank: 1, participant_id: 'mw', display_name: 'MinglongWang', avatar_id: 1, value: 80, metrics: { total_tokens: 1_890_000_000, request_count: 6_750, cache_read_rate: 926_200, ttft_average: 3_460, latency_average: 15_200, peak_tpm: 6_770_000, peak_rpm: 26.8, cost: 830.8636 } },
      { rank: 2, participant_id: 'cw', display_name: 'ChenguangWan', avatar_id: 2, value: 73, metrics: { total_tokens: 1_740_000_000, request_count: 11_200, cache_read_rate: 943_200, ttft_average: 5_110, latency_average: 19_500, peak_tpm: 2_390_000, peak_rpm: 12.4, cost: 1382.6736 } },
      { rank: 3, participant_id: 'yz', display_name: 'YalingZhang', avatar_id: 3, value: 65, metrics: { total_tokens: 128_640_000, request_count: 1_900, cache_read_rate: 909_700, ttft_average: 4_290, latency_average: 13_900, peak_tpm: 614_590, peak_rpm: 6, cost: 102.1927 } },
      { rank: 4, participant_id: 'nd', display_name: 'NoData', avatar_id: 4, value: 10 },
    ];

    const renderScored = async () => {
      await act(async () => {
        root.render(<RankingLeaderboardResults scope="local" metric="overall" entries={scored} />);
      });
    };
    const rowNames = () => Array.from(container.querySelectorAll('tbody [data-ranking-row] strong')).map((node) => node.textContent);
    const rowRanks = () => Array.from(container.querySelectorAll('tbody [data-ranking-position]')).map((node) => node.textContent);
    const sortButton = (column: string) => container.querySelector<HTMLButtonElement>(`[data-ranking-sort="${column}"]`);
    const header = (column: string) => sortButton(column)?.closest('th');
    const clickSort = async (column: string) => {
      await act(async () => sortButton(column)?.click());
    };

    it('offers a sort button on Score and every metric column, including Cost', async () => {
      await renderScored();

      for (const column of ['score', 'total_tokens', 'request_count', 'cache_read_rate', 'ttft_average', 'latency_average', 'peak_tpm', 'peak_rpm', 'cost']) {
        expect(sortButton(column), column).not.toBeNull();
        expect(header(column)?.getAttribute('aria-sort'), column).toBe('none');
      }
      expect(container.querySelector('[data-ranking-rank-column] button')).toBeNull();
      expect(container.querySelector('[data-ranking-participant-column] button')).toBeNull();
    });

    it('cycles descending, ascending, then back to the leaderboard order', async () => {
      await renderScored();
      expect(rowNames()).toEqual(['MinglongWang', 'ChenguangWan', 'YalingZhang', 'NoData']);

      await clickSort('cost');
      expect(rowNames()).toEqual(['ChenguangWan', 'MinglongWang', 'YalingZhang', 'NoData']);
      expect(header('cost')?.getAttribute('aria-sort')).toBe('descending');

      await clickSort('cost');
      expect(rowNames()).toEqual(['YalingZhang', 'MinglongWang', 'ChenguangWan', 'NoData']);
      expect(header('cost')?.getAttribute('aria-sort')).toBe('ascending');

      await clickSort('cost');
      expect(rowNames()).toEqual(['MinglongWang', 'ChenguangWan', 'YalingZhang', 'NoData']);
      expect(header('cost')?.getAttribute('aria-sort')).toBe('none');
    });

    it('keeps rows without a value last in both directions', async () => {
      await renderScored();

      await clickSort('ttft_average');
      expect(rowNames()).toEqual(['ChenguangWan', 'YalingZhang', 'MinglongWang', 'NoData']);
      await clickSort('ttft_average');
      expect(rowNames()).toEqual(['MinglongWang', 'YalingZhang', 'ChenguangWan', 'NoData']);
    });

    it('sorts Score by the leaderboard value and switching columns restarts at descending', async () => {
      await renderScored();

      await clickSort('score');
      await clickSort('score');
      expect(rowNames()).toEqual(['NoData', 'YalingZhang', 'ChenguangWan', 'MinglongWang']);

      await clickSort('request_count');
      expect(rowNames()).toEqual(['ChenguangWan', 'MinglongWang', 'YalingZhang', 'NoData']);
      expect(header('request_count')?.getAttribute('aria-sort')).toBe('descending');
      expect(header('score')?.getAttribute('aria-sort')).toBe('none');
    });

    it('keeps each key on its leaderboard rank and leaves the podium alone while sorted', async () => {
      await renderScored();

      await clickSort('cost');

      expect(rowRanks()).toEqual(['2', '1', '3', '4']);
      const podiumNames = Array.from(container.querySelectorAll('[data-ranking-podium] article > strong')).map((node) => node.textContent);
      expect(podiumNames).toEqual(['MinglongWang', 'ChenguangWan', 'YalingZhang']);
    });

    it('also sorts the single value column of a non-overall metric', async () => {
      await act(async () => {
        root.render(<RankingLeaderboardResults scope="local" metric="cost" entries={[
          { rank: 1, participant_id: 'a', display_name: 'A', avatar_id: 1, value: 3 },
          { rank: 2, participant_id: 'b', display_name: 'B', avatar_id: 2, value: 9 },
        ]} />);
      });

      await clickSort('score');
      expect(rowNames()).toEqual(['B', 'A']);
    });
  });
});
