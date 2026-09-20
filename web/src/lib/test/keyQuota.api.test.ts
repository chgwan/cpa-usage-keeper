import { afterEach, describe, expect, it, vi } from 'vitest';
import { fetchKeyOverviewQuota } from '../api';

describe('fetchKeyOverviewQuota', () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it('loads the viewer cost quota from the key-overview quota endpoint', async () => {
    vi.stubGlobal('window', { __APP_BASE_PATH__: undefined });
    const quota = {
      enforcementState: 'active',
      windows: [
        { window: 'daily', costUsd: 1.5 },
        { window: 'weekly', costUsd: 8.1, limit: 20, ratio: 0.405 },
        { window: 'monthly', costUsd: 23.4, limit: 60, ratio: 0.39 },
      ],
    };
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue({
      ok: true,
      json: async () => quota,
    } as Response);

    const result = await fetchKeyOverviewQuota();

    const [url, init] = fetchMock.mock.calls[0];
    expect(new URL(String(url), 'http://localhost').pathname).toBe('/api/v1/key-overview/quota');
    expect(init).toMatchObject({ credentials: 'include' });
    expect(result).toEqual(quota);
  });
});
