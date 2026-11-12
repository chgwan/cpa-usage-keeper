import { apiFetch, apiPath } from '@/lib/api';
import type {
  LocalRankingProfileRequest,
  LocalRankingProfileResponse,
  RankingLeaderboardResponse,
  RankingMetric,
  RankingPeriod,
} from './types';

export class RankingApiError extends Error {
  status: number;
  code: string;

  constructor(code: string, status: number) {
    super(code);
    this.name = 'RankingApiError';
    this.status = status;
    this.code = code;
  }
}

const requestRankingJSON = async <T>(path: string, init: RequestInit = {}): Promise<T> => {
  const headers = new Headers(init.headers);
  const method = (init.method ?? 'GET').toUpperCase();
  if (method !== 'GET' && method !== 'HEAD') {
    headers.set('X-CPA-Usage-Keeper-Request', 'fetch');
  }
  const response = await apiFetch(apiPath(path), {
    ...init,
    credentials: 'include',
    cache: 'no-store',
    headers,
  });
  if (!response.ok) {
    let code = 'ranking_request_failed';
    try {
      const payload = await response.json() as { error?: string };
      code = payload.error?.trim() || code;
    } catch {
      // 非 JSON 错误仍保留稳定的本地错误码。
    }
    throw new RankingApiError(code, response.status);
  }
  return response.json() as Promise<T>;
};

export const fetchLocalRankingLeaderboard = (
  period: RankingPeriod,
  metric: RankingMetric,
  signal?: AbortSignal,
) => {
  const query = new URLSearchParams({ period, metric });
  return requestRankingJSON<RankingLeaderboardResponse>(`/ranking/local/leaderboards?${query.toString()}`, { signal });
};

export const updateLocalRankingProfile = (
  participantID: string,
  profile: LocalRankingProfileRequest,
  signal?: AbortSignal,
) => requestRankingJSON<LocalRankingProfileResponse>(
  `/ranking/local/profiles/${encodeURIComponent(participantID)}`,
  {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(profile),
    signal,
  },
);
