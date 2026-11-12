import { useCallback, useEffect, useRef, useState } from 'react';
import { fetchLocalRankingLeaderboard, RankingApiError, updateLocalRankingProfile } from '../api';
import { loadRankingPeriod, persistRankingPeriod } from '../preferences';
import {
  DEFAULT_RANKING_METRIC,
  type LocalRankingProfileRequest,
  type LocalRankingProfileResponse,
  type RankingLeaderboardResponse,
  type RankingMetric,
  type RankingPeriod,
} from '../types';

export const LOCAL_RANKING_POLL_INTERVAL_MS = 60_000;

export interface LocalRankingDataAPI {
  leaderboard: (
    period: RankingPeriod,
    metric: RankingMetric,
    signal?: AbortSignal,
  ) => Promise<RankingLeaderboardResponse>;
  updateProfile?: (
    participantID: string,
    profile: LocalRankingProfileRequest,
    signal?: AbortSignal,
  ) => Promise<LocalRankingProfileResponse>;
}

const defaultAPI: LocalRankingDataAPI = {
  leaderboard: fetchLocalRankingLeaderboard,
  updateProfile: updateLocalRankingProfile,
};
const isAbortError = (error: unknown) => error instanceof DOMException && error.name === 'AbortError';
type LocalRankingProfilePatch = Partial<Pick<LocalRankingProfileResponse, 'key_alias' | 'display_name' | 'avatar_id'>>;
const applyLocalProfile = (
  board: RankingLeaderboardResponse,
  participantID: string,
  profile: LocalRankingProfilePatch,
): RankingLeaderboardResponse => ({
  ...board,
  entries: board.entries.map((entry) => entry.participant_id === participantID
    ? {
      ...entry,
      ...profile,
    }
    : entry),
});

export interface UseLocalRankingDataOptions {
  enabled: boolean;
  onAuthRequired?: () => void;
  onBackgroundRefreshError?: (error: unknown) => void;
  api?: LocalRankingDataAPI;
}

// useLocalRankingData 维护本地榜单的周期选择与数据；榜单始终按 Token 总量拉取，其余列在表格内排序。
export function useLocalRankingData({
  enabled,
  onAuthRequired,
  onBackgroundRefreshError,
  api = defaultAPI,
}: UseLocalRankingDataOptions) {
  const [period, setPeriodState] = useState<RankingPeriod>(() => loadRankingPeriod());
  const [leaderboard, setLeaderboard] = useState<RankingLeaderboardResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const controllerRef = useRef<AbortController | null>(null);
  const generationRef = useRef(0);
  const cacheRef = useRef(new Map<RankingPeriod, RankingLeaderboardResponse>());

  const setPeriod = useCallback((next: RankingPeriod) => {
    setPeriodState(next);
    persistRankingPeriod(next);
  }, []);

  const loadLeaderboard = useCallback(async (
    nextPeriod: RankingPeriod,
    { silent = false }: { silent?: boolean } = {},
  ) => {
    controllerRef.current?.abort();
    const controller = new AbortController();
    controllerRef.current = controller;
    const generation = ++generationRef.current;
    const cachedBoard = cacheRef.current.get(nextPeriod) ?? null;
    if (!silent) {
      setLoading(true);
      setError(null);
      // 目标缓存缺失时保留上一份响应；页面会按 period 过滤内容，但标题栏无需闪动。
      setLeaderboard((current) => cachedBoard ?? current);
    }
    try {
      const board = await api.leaderboard(nextPeriod, DEFAULT_RANKING_METRIC, controller.signal);
      if (generationRef.current === generation) {
        cacheRef.current.set(nextPeriod, board);
        setLeaderboard(board);
        setError(null);
      }
      return board;
    } catch (loadError) {
      if (!isAbortError(loadError) && generationRef.current === generation) {
        if (loadError instanceof RankingApiError && loadError.status === 401) onAuthRequired?.();
        if (cachedBoard) {
          setLeaderboard({ ...cachedBoard, stale: true });
          setError(null);
          onBackgroundRefreshError?.(loadError);
        } else {
          setLeaderboard(null);
          setError(loadError);
        }
      }
      return null;
    } finally {
      if (controllerRef.current === controller) controllerRef.current = null;
      if (generationRef.current === generation) setLoading(false);
    }
  }, [api, onAuthRequired, onBackgroundRefreshError]);

  const refreshLeaderboard = useCallback(
    () => loadLeaderboard(period),
    [loadLeaderboard, period],
  );

  const patchProfileCache = useCallback((participantID: string, profile: LocalRankingProfilePatch) => {
    // 设置页和排行弹窗共用 Key 资料；同步废弃旧读取并更新所有已加载榜单投影。
    controllerRef.current?.abort();
    controllerRef.current = null;
    generationRef.current += 1;
    setLoading(false);
    for (const [key, cachedBoard] of cacheRef.current) {
      cacheRef.current.set(key, applyLocalProfile(cachedBoard, participantID, profile));
    }
    setLeaderboard((current) => current ? applyLocalProfile(current, participantID, profile) : current);
  }, []);

  const updateProfile = useCallback(async (
    participantID: string,
    profile: LocalRankingProfileRequest,
    signal?: AbortSignal,
  ) => {
    const update = api.updateProfile ?? defaultAPI.updateProfile;
    if (!update) throw new Error('local ranking profile API is not configured');
    try {
      const updated = await update(participantID, profile, signal);
      patchProfileCache(updated.participant_id, updated);
      return updated;
    } catch (updateError) {
      if (updateError instanceof RankingApiError && updateError.status === 401) onAuthRequired?.();
      throw updateError;
    }
  }, [api, onAuthRequired, patchProfileCache]);

  useEffect(() => {
    if (!enabled) {
      controllerRef.current?.abort();
      controllerRef.current = null;
      setLoading(false);
      return;
    }
    void loadLeaderboard(period);
    return () => controllerRef.current?.abort();
  }, [enabled, loadLeaderboard, period]);

  useEffect(() => {
    if (!enabled) return;
    const interval = window.setInterval(() => {
      if (document.visibilityState === 'hidden') return;
      void loadLeaderboard(period, { silent: true });
    }, LOCAL_RANKING_POLL_INTERVAL_MS);
    return () => window.clearInterval(interval);
  }, [enabled, loadLeaderboard, period]);

  useEffect(() => () => controllerRef.current?.abort(), []);

  return {
    period,
    setPeriod,
    leaderboard,
    leaderboardLoading: loading,
    leaderboardError: error,
    refreshLeaderboard,
    updateProfile,
    patchProfileCache,
  };
}
