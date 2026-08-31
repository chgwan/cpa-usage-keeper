import { describe, expect, it } from 'vitest';

import { resolveQuotaProgress } from '../quotaProgress';

describe('resolveQuotaProgress', () => {
  it('returns null when summary or tightest is missing', () => {
    expect(resolveQuotaProgress(undefined)).toBeNull();
    expect(resolveQuotaProgress({ enabled: false, enforcementState: 'active', tightest: null })).toBeNull();
  });

  it('caps ratio above 1 for breached limits', () => {
    const progress = resolveQuotaProgress({
      enabled: true,
      enforcementState: 'disabled_by_quota',
      tightest: { type: 'tokens', window: 'daily', value: 100, used: 250, ratio: 2.5 },
    });
    expect(progress?.ratio).toBe(1);
    expect(progress?.breached).toBe(true);
  });

  it('passes through healthy ratios', () => {
    const progress = resolveQuotaProgress({
      enabled: true,
      enforcementState: 'active',
      tightest: { type: 'requests', window: 'monthly', value: 300, used: 90, ratio: 0.3 },
    });
    expect(progress?.ratio).toBe(0.3);
    expect(progress?.breached).toBe(false);
  });
});
