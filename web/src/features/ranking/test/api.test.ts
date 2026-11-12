import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from 'vitest';
import { fetchLocalRankingLeaderboard, RankingApiError, updateLocalRankingProfile } from '../api';

describe('ranking API', () => {
  let fetchMock: MockInstance<typeof fetch>;

  beforeEach(() => {
    vi.stubGlobal('window', { __APP_BASE_PATH__: '/keeper/' });
    fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async () => Response.json({}));
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it('uses the Keeper base path and the local leaderboard endpoint', async () => {
    await fetchLocalRankingLeaderboard('today', 'total_tokens');

    const [rawURL, init] = fetchMock.mock.calls[0];
    const url = new URL(String(rawURL), 'http://localhost');
    expect(url.pathname).toBe('/keeper/api/v1/ranking/local/leaderboards');
    expect(url.search).toBe('?period=today&metric=total_tokens');
    expect(init).toMatchObject({ credentials: 'include', cache: 'no-store' });
    expect(new Headers(init?.headers).get('X-CPA-Usage-Keeper-Request')).toBeNull();
  });

  it('updates a local Key profile through the dedicated admin endpoint', async () => {
    await updateLocalRankingProfile('42', { key_alias: 'Primary', avatar_id: 17 });

    const [rawURL, init] = fetchMock.mock.calls[0];
    expect(new URL(String(rawURL), 'http://localhost').pathname).toBe('/keeper/api/v1/ranking/local/profiles/42');
    expect(init).toMatchObject({ method: 'PATCH', credentials: 'include', cache: 'no-store' });
    expect(new Headers(init?.headers).get('X-CPA-Usage-Keeper-Request')).toBe('fetch');
    expect(init?.body).toBe(JSON.stringify({ key_alias: 'Primary', avatar_id: 17 }));
  });

  it('preserves the server error code for actionable feedback', async () => {
    fetchMock.mockResolvedValue(Response.json({ error: 'local_ranking_key_not_found' }, { status: 404 }));

    await expect(updateLocalRankingProfile('42', { key_alias: '', avatar_id: 1 })).rejects.toMatchObject({
      name: 'RankingApiError',
      status: 404,
      code: 'local_ranking_key_not_found',
    } satisfies Partial<RankingApiError>);
  });
});
