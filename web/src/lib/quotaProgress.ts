import type { ApiKeyPolicySummary } from './types';

export function resolveQuotaProgress(summary: ApiKeyPolicySummary | undefined): { ratio: number; label: string; breached: boolean } | null {
  if (!summary?.tightest) {
    return null;
  }
  const breached = summary.tightest.ratio >= 1 || summary.enforcementState === 'disabled_by_quota';
  return {
    ratio: Math.min(summary.tightest.ratio, 1),
    label: `${summary.tightest.type}/${summary.tightest.window}`,
    breached,
  };
}
