// @vitest-environment happy-dom

import { act, useEffect } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { RANKING_PERIOD_STORAGE_KEY } from '../../preferences';
import type { RankingLeaderboardResponse, RankingMetric, RankingPeriod } from '../../types';
import {
  LOCAL_RANKING_POLL_INTERVAL_MS,
  useLocalRankingData,
  type LocalRankingDataAPI,
} from '../useLocalRankingData';

globalThis.IS_REACT_ACT_ENVIRONMENT = true;

const board = (period: RankingPeriod, metric: RankingMetric, value = 93): RankingLeaderboardResponse => ({
  period,
  period_key: period.includes('month') ? '2026-07' : '2026-07-31',
  metric,
  generated_at: '2026-07-31T04:00:00Z',
  stale: false,
  entries: [{ rank: 1, participant_id: '1', display_name: 'Primary', avatar_id: 1, value }],
});

let latest: ReturnType<typeof useLocalRankingData> | null = null;

function Harness({ enabled, api }: { enabled: boolean; api: LocalRankingDataAPI }) {
  const result = useLocalRankingData({ enabled, api });
  useEffect(() => { latest = result; }, [result]);
  return null;
}

describe('useLocalRankingData', () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    window.localStorage.clear();
    container = document.createElement('div');
    document.body.appendChild(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
    latest = null;
    vi.useRealTimers();
  });

  const renderHook = async (enabled: boolean, api: LocalRankingDataAPI) => {
    await act(async () => {
      root.render(<Harness enabled={enabled} api={api} />);
    });
  };

  it('loads the token board only while enabled and follows period changes', async () => {
    const calls: string[] = [];
    const api: LocalRankingDataAPI = {
      leaderboard: async (period, metric) => {
        calls.push(`${period}:${metric}`);
        return board(period, metric);
      },
    };

    await renderHook(false, api);
    expect(calls).toEqual([]);
    await renderHook(true, api);
    await act(async () => latest!.setPeriod('yesterday'));

    expect(calls).toEqual(['today:total_tokens', 'yesterday:total_tokens']);
    expect(latest!.period).toBe('yesterday');
    expect(latest!.leaderboard).toMatchObject({ period: 'yesterday', metric: 'total_tokens' });
  });

  it('persists the selected period and restores it on the next mount', async () => {
    const calls: string[] = [];
    const api: LocalRankingDataAPI = {
      leaderboard: async (period, metric) => {
        calls.push(period);
        return board(period, metric);
      },
    };

    await renderHook(true, api);
    await act(async () => latest!.setPeriod('current_week'));
    expect(window.localStorage.getItem(RANKING_PERIOD_STORAGE_KEY)).toBe('current_week');

    await act(async () => root.unmount());
    root = createRoot(container);
    await renderHook(true, api);
    expect(latest!.period).toBe('current_week');
    expect(calls.at(-1)).toBe('current_week');
  });

  it('ignores an unknown stored period', async () => {
    window.localStorage.setItem(RANKING_PERIOD_STORAGE_KEY, 'overall');
    await renderHook(false, { leaderboard: async (period, metric) => board(period, metric) });
    expect(latest!.period).toBe('today');
  });

  it('polls the selected board every minute', async () => {
    vi.useFakeTimers();
    let calls = 0;
    const api: LocalRankingDataAPI = {
      leaderboard: async (period, metric) => {
        calls += 1;
        return board(period, metric, 90 + calls);
      },
    };

    await renderHook(true, api);
    expect(calls).toBe(1);
    await act(async () => vi.advanceTimersByTimeAsync(LOCAL_RANKING_POLL_INTERVAL_MS));
    expect(calls).toBe(2);
    expect(latest!.leaderboard?.entries[0]?.value).toBe(92);
  });

  it('retains the previous response while another period is loading', async () => {
    const pendingYesterday = Promise.withResolvers<RankingLeaderboardResponse>();
    const api: LocalRankingDataAPI = {
      leaderboard: async (period, metric) => (
        period === 'yesterday' ? pendingYesterday.promise : board(period, metric)
      ),
    };

    await renderHook(true, api);
    await act(async () => latest!.setPeriod('yesterday'));

    expect(latest!.leaderboardLoading).toBe(true);
    expect(latest!.leaderboard).toMatchObject({ period: 'today' });

    await act(async () => pendingYesterday.resolve(board('yesterday', 'total_tokens', 88)));
    expect(latest!.leaderboardLoading).toBe(false);
    expect(latest!.leaderboard).toMatchObject({ period: 'yesterday' });
  });

  it('updates every cached occurrence of a saved Key profile immediately', async () => {
    const updateProfile = vi.fn(async () => ({
      participant_id: '1', key_alias: 'Renamed', display_name: 'Renamed', avatar_id: 42,
    }));
    const api: LocalRankingDataAPI = {
      leaderboard: async (period, metric) => board(period, metric),
      updateProfile,
    };

    await renderHook(true, api);
    await act(async () => latest!.updateProfile('1', { key_alias: 'Renamed', avatar_id: 42 }));

    expect(updateProfile).toHaveBeenCalledWith('1', { key_alias: 'Renamed', avatar_id: 42 }, undefined);
    expect(latest!.leaderboard?.entries[0]).toMatchObject({
      participant_id: '1', key_alias: 'Renamed', display_name: 'Renamed', avatar_id: 42,
    });
  });

  it('keeps a settings alias patch when the board reload fails', async () => {
    let shouldFail = false;
    const api: LocalRankingDataAPI = {
      leaderboard: async (period, metric) => {
        if (shouldFail) throw new Error('local board unavailable');
        return board(period, metric);
      },
    };

    await renderHook(true, api);
    await renderHook(false, api);
    await act(async () => latest!.patchProfileCache('1', {
      key_alias: 'Settings Alias',
      display_name: 'Settings Alias',
    }));
    shouldFail = true;
    await renderHook(true, api);

    expect(latest!.leaderboard).toMatchObject({ stale: true });
    expect(latest!.leaderboard?.entries[0]).toMatchObject({
      participant_id: '1',
      key_alias: 'Settings Alias',
      display_name: 'Settings Alias',
      avatar_id: 1,
    });
  });

  it('does not let an older in-flight board overwrite a saved Key profile', async () => {
    const pendingRefresh = Promise.withResolvers<RankingLeaderboardResponse>();
    let leaderboardCalls = 0;
    const api: LocalRankingDataAPI = {
      leaderboard: async (period, metric) => {
        leaderboardCalls += 1;
        return leaderboardCalls === 1 ? board(period, metric) : pendingRefresh.promise;
      },
      updateProfile: async () => ({
        participant_id: '1', key_alias: 'Renamed', display_name: 'Renamed', avatar_id: 42,
      }),
    };

    await renderHook(true, api);
    let pendingLoad!: Promise<RankingLeaderboardResponse | null>;
    await act(async () => {
      pendingLoad = latest!.refreshLeaderboard();
    });
    await act(async () => latest!.updateProfile('1', { key_alias: 'Renamed', avatar_id: 42 }));
    await act(async () => pendingRefresh.resolve(board('today', 'total_tokens')));
    await pendingLoad;

    expect(latest!.leaderboard?.entries[0]).toMatchObject({
      key_alias: 'Renamed', display_name: 'Renamed', avatar_id: 42,
    });
    expect(latest!.leaderboardLoading).toBe(false);
  });
});
