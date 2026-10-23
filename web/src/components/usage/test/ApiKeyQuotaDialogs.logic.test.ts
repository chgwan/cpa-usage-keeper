import { describe, expect, it } from 'vitest';
import { buildQuotaAdjustmentRequest, buildQuotaPolicyUpdateRequest, buildQuotaResetRequest } from '../ApiKeyQuotaDialogs';

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
    expect(buildQuotaPolicyUpdateRequest(['7'], ['monthly', 'daily'], { daily: ' 4 ', monthly: '' })).toEqual({
      request: { apiKeyIds: ['7'], limits: { daily: 4, monthly: null } },
      error: null,
    });
  });

  it('requires keys, at least one period, and positive values', () => {
    expect(buildQuotaPolicyUpdateRequest([], ['daily'], { daily: '1' }).error).toBe('keys');
    expect(buildQuotaPolicyUpdateRequest(['7'], [], {}).error).toBe('periods');
    expect(buildQuotaPolicyUpdateRequest(['7'], ['daily'], { daily: '0' }).error).toBe('value');
    expect(buildQuotaPolicyUpdateRequest(['7'], ['daily'], { daily: 'abc' }).error).toBe('value');
  });
});

describe('buildQuotaAdjustmentRequest', () => {
  it('parses a positive signed amount', () => {
    expect(buildQuotaAdjustmentRequest(['7'], ['weekly'], '+500')).toEqual({
      request: { apiKeyIds: ['7'], windows: ['weekly'], deltaUsd: 500 },
      error: null,
    });
  });

  it('parses a negative signed amount', () => {
    expect(buildQuotaAdjustmentRequest(['7'], ['weekly'], '-200')).toEqual({
      request: { apiKeyIds: ['7'], windows: ['weekly'], deltaUsd: -200 },
      error: null,
    });
  });

  it('parses an unsigned amount as positive', () => {
    expect(buildQuotaAdjustmentRequest(['7'], ['weekly'], '500')).toEqual({
      request: { apiKeyIds: ['7'], windows: ['weekly'], deltaUsd: 500 },
      error: null,
    });
  });

  it('sends windows in daily → weekly → monthly order regardless of iteration order', () => {
    expect(buildQuotaAdjustmentRequest(['7', '9'], ['monthly', 'daily'], '100')).toEqual({
      request: { apiKeyIds: ['7', '9'], windows: ['daily', 'monthly'], deltaUsd: 100 },
      error: null,
    });
  });

  it('requires keys, periods, and a non-zero numeric value', () => {
    expect(buildQuotaAdjustmentRequest([], ['weekly'], '500').error).toBe('keys');
    expect(buildQuotaAdjustmentRequest(['7'], [], '500').error).toBe('periods');
    expect(buildQuotaAdjustmentRequest(['7'], ['weekly'], '').error).toBe('value');
    expect(buildQuotaAdjustmentRequest(['7'], ['weekly'], 'abc').error).toBe('value');
    expect(buildQuotaAdjustmentRequest(['7'], ['weekly'], '0').error).toBe('value');
  });

  // item 4：和后端 quotaAdjustmentMaxAbsDeltaUSD 的 1e9 上限保持一致，超出的 delta 本来就会
  // 让 limit+delta 有溢出到 +Inf 的风险，前端应该提前拦住而不是等一次往返请求失败。
  it('rejects a delta whose magnitude exceeds the 1e9 cap, but accepts exactly the cap', () => {
    expect(buildQuotaAdjustmentRequest(['7'], ['weekly'], String(1e9 + 1)).error).toBe('value');
    expect(buildQuotaAdjustmentRequest(['7'], ['weekly'], String(-1e9 - 1)).error).toBe('value');
    expect(buildQuotaAdjustmentRequest(['7'], ['weekly'], String(1e9)).error).toBeNull();
    expect(buildQuotaAdjustmentRequest(['7'], ['weekly'], String(-1e9)).error).toBeNull();
  });
});
