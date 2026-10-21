import { describe, expect, it } from 'vitest';
import { buildQuotaPolicyUpdateRequest, buildQuotaResetRequest } from '../ApiKeyQuotaDialogs';

describe('buildQuotaResetRequest', () => {
  const allIds = ['1', '2', '3'];

  it('uses the global scope when every key is selected', () => {
    expect(buildQuotaResetRequest(new Set(allIds), allIds, ['weekly'])).toEqual({
      request: { allKeys: true, apiKeyIds: [], windows: ['weekly'] },
      error: null,
    });
  });

  it('sends selected keys in list order and windows in daily → weekly → monthly order', () => {
    expect(buildQuotaResetRequest(new Set(['3', '1']), allIds, ['monthly', 'daily'])).toEqual({
      request: { allKeys: false, apiKeyIds: ['1', '3'], windows: ['daily', 'monthly'] },
      error: null,
    });
  });

  it('rejects an empty key or period selection', () => {
    expect(buildQuotaResetRequest(new Set(), allIds, ['weekly']).error).toBe('keys');
    expect(buildQuotaResetRequest(new Set(['1']), allIds, []).error).toBe('periods');
  });
});

describe('buildQuotaPolicyUpdateRequest', () => {
  it('sends only chosen periods and treats empty values as removal', () => {
    expect(buildQuotaPolicyUpdateRequest(['7'], ['monthly', 'daily'], { daily: ' 4 ', monthly: '' }, 'keep')).toEqual({
      request: { apiKeyIds: ['7'], limits: { daily: 4, monthly: null } },
      error: null,
    });
  });

  it('allows an enforcement-only change without periods', () => {
    expect(buildQuotaPolicyUpdateRequest(['7', '8'], [], {}, 'off')).toEqual({
      request: { apiKeyIds: ['7', '8'], limits: {}, enabled: false },
      error: null,
    });
  });

  it('requires keys, a change, and positive values', () => {
    expect(buildQuotaPolicyUpdateRequest([], ['daily'], { daily: '1' }, 'keep').error).toBe('keys');
    expect(buildQuotaPolicyUpdateRequest(['7'], [], {}, 'keep').error).toBe('periods');
    expect(buildQuotaPolicyUpdateRequest(['7'], ['daily'], { daily: '0' }, 'keep').error).toBe('value');
    expect(buildQuotaPolicyUpdateRequest(['7'], ['daily'], { daily: 'abc' }, 'on').error).toBe('value');
  });
});
