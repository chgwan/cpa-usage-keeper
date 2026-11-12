import { RANKING_PERIODS, type RankingPeriod } from './types';

// v2 只保存周期：综合分移除后指标改为表头排序，不再持久化。
export const RANKING_PERIOD_STORAGE_KEY = 'cli-proxy-usage-ranking-period-v2';
export const DEFAULT_RANKING_PERIOD: RankingPeriod = 'today';

interface RankingPeriodStorage {
  getItem: (key: string) => string | null;
  setItem: (key: string, value: string) => void;
}

const defaultStorage = (): RankingPeriodStorage | undefined => (
  typeof localStorage === 'undefined' ? undefined : localStorage
);

// 周期会直接拼进榜单请求，读回来必须按白名单校验。
export const normalizeRankingPeriod = (value: unknown): RankingPeriod | null => (
  typeof value === 'string' && RANKING_PERIODS.includes(value as RankingPeriod)
    ? value as RankingPeriod
    : null
);

export const loadRankingPeriod = (storage: RankingPeriodStorage | undefined = defaultStorage()): RankingPeriod => {
  try {
    return normalizeRankingPeriod(storage?.getItem(RANKING_PERIOD_STORAGE_KEY)) ?? DEFAULT_RANKING_PERIOD;
  } catch {
    return DEFAULT_RANKING_PERIOD;
  }
};

export const persistRankingPeriod = (
  period: RankingPeriod,
  storage: RankingPeriodStorage | undefined = defaultStorage(),
): boolean => {
  try {
    if (!storage) return false;
    storage.setItem(RANKING_PERIOD_STORAGE_KEY, period);
    return true;
  } catch {
    return false;
  }
};
