import React from 'react';
import '@/i18n';
import { describe, expect, it, vi } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { ApiKeySettingsCard, copyApiKeyToClipboard, getApiKeySettingsVisibleKey, resolveStoredApiKeySort, sortApiKeySettingsItems, type ApiKeySortKey } from '../ApiKeySettingsCard';
import type { CpaApiKeySettingsItem } from '@/lib/types';

const apiKeys: CpaApiKeySettingsItem[] = [
  // policy.enforcementState 为 active，行内启停按钮才会显示 “Disable”（缺失时按非 active 处理）。
  { id: '9007199254740993', apiKey: 'sk-alpha123456', keyAlias: 'Primary', displayKey: 'sk-*********123456', label: 'Primary', lastSyncedAt: '2026-05-13T00:00:00Z', policy: { enabled: true, enforcementState: 'active', tightest: null } },
  { id: '9007199254740994', apiKey: 'sk-beta654321', keyAlias: '', displayKey: 'sk-*********654321', label: 'sk-*********654321', lastSyncedAt: null, policy: { enabled: true, enforcementState: 'active', tightest: null } },
];

const renderCard = (props: Partial<React.ComponentProps<typeof ApiKeySettingsCard>> = {}) => renderToStaticMarkup(
  <ApiKeySettingsCard
    apiKeys={apiKeys}
    loading={false}
    savingId={null}
    onSaveAlias={() => undefined}
    {...props}
  />,
);

const countOccurrences = (text: string, value: string) => text.split(value).length - 1;

describe('ApiKeySettingsCard', () => {
  it('sorts by one key at a time, with name A–Z as the tie-break', () => {
    const tightest = (ratio: number, used: number) => ({ type: 'cost' as const, window: 'daily' as const, value: 10, used, ratio });
    const item = (
      id: string,
      keyAlias: string,
      displayKey: string,
      state?: 'active' | 'disabled_manual' | 'disabled_by_quota',
      limit: ReturnType<typeof tightest> | null = null,
    ): CpaApiKeySettingsItem => ({
      id, apiKey: '', keyAlias, displayKey, label: keyAlias || displayKey, lastSyncedAt: null,
      policy: state ? { enabled: true, enforcementState: state, tightest: limit } : undefined,
    });
    const items = [
      item('1', 'zeta', 'sk-***1', 'active', tightest(0.5, 5)),
      item('2', '', 'sk-***2', 'disabled_manual'),
      item('3', 'Alpha', 'sk-***3', 'disabled_by_quota', tightest(1.2, 12)),
      item('4', 'key10', 'sk-***4', 'active', tightest(0.9, 1.8)),
      item('5', 'key9', 'sk-***5'),
      item('6', 'beta', 'sk-***6', 'active', tightest(0.5, 5)),
    ];
    const ids = (sortKey: ApiKeySortKey) => sortApiKeySettingsItems(items, sortKey).map((value) => value.id);

    expect(ids('name_asc')).toEqual(['3', '6', '5', '4', '2', '1']);
    expect(ids('name_desc')).toEqual(['1', '2', '4', '5', '6', '3']);
    expect(ids('status')).toEqual(['6', '4', '1', '2', '3', '5']);
    // 无限额（tightest 为空）的 key 在两种限额排序下都排在最后。
    expect(ids('quota_ratio')).toEqual(['3', '4', '6', '1', '5', '2']);
    expect(ids('quota_spend')).toEqual(['3', '6', '1', '4', '5', '2']);
    expect(items.map((value) => value.id)).toEqual(['1', '2', '3', '4', '5', '6']);
  });

  it('restores the saved sort key and migrates the old toggle settings', () => {
    expect(resolveStoredApiKeySort(null, null)).toBe('name_asc');
    expect(resolveStoredApiKeySort('quota_spend', null)).toBe('quota_spend');
    expect(resolveStoredApiKeySort('bogus', null)).toBe('name_asc');
    expect(resolveStoredApiKeySort(null, '{"alphabetical":"desc","status":null}')).toBe('name_desc');
    expect(resolveStoredApiKeySort(null, '{"alphabetical":"asc","status":"desc"}')).toBe('status');
    expect(resolveStoredApiKeySort(null, '{"alphabetical":null,"status":null}')).toBe('name_asc');
    expect(resolveStoredApiKeySort(null, 'not json')).toBe('name_asc');
    // 新值优先于旧值。
    expect(resolveStoredApiKeySort('name_asc', '{"alphabetical":"desc","status":null}')).toBe('name_asc');
  });

  it('renders one closed sort menu trigger showing the default order', () => {
    const html = renderCard();

    expect(countOccurrences(html, 'data-api-key-sort-trigger')).toBe(1);
    expect(html).toContain('aria-haspopup="menu"');
    expect(html).toContain('aria-expanded="false"');
    expect(html).toContain('Name A → Z');
    expect(html).not.toContain('role="menu"');
  });

  it('renders alias, masked key, and string ids without local ids by default', () => {
    const html = renderCard();

    expect(countOccurrences(html, 'API Key Settings')).toBe(1);
    expect(html).toContain('Primary');
    expect(html).toContain('sk-*********123456');
    expect(html).toContain('sk-*********654321');
    expect(html).not.toContain('sk-alpha123456');
    expect(html).not.toContain('sk-beta654321');
    expect(html).toContain('aria-label="Show full API keys"');
    expect(countOccurrences(html, 'aria-label="Copy"')).toBe(2);
    expect(html).toContain('title="sk-*********123456"');
    // 该 id 只应出现在两个测试钩子里（行容器 + 管理面板打开按钮），不作为可见文案展示。
    expect(countOccurrences(html, '9007199254740993')).toBe(2);
    expect(html).toContain('data-api-key-settings-item="9007199254740993"');
    expect(html).toContain('data-api-key-manage-open="9007199254740993"');
    expect(html).not.toContain('Local ID');
  });

  it('uses the title eye toggle state to choose masked or raw keys', () => {
    expect(getApiKeySettingsVisibleKey(apiKeys[0], false)).toBe('sk-*********123456');
    expect(getApiKeySettingsVisibleKey(apiKeys[0], true)).toBe('sk-alpha123456');
  });

  it('keeps only alias + Save inline per row; lifecycle actions moved to the per-key manage dialog', () => {
    const html = renderCard({
      onCreateKey: () => Promise.resolve(null),
      onRegenerateKey: () => Promise.resolve(null),
      onDeleteKey: () => Promise.resolve(false),
      onDisableKey: () => Promise.resolve(false),
      onRestoreKey: () => Promise.resolve(false),
      onSavePolicies: () => Promise.resolve(true),
      onResetQuotas: () => Promise.resolve(true),
    });

    // 行内只剩 保存；重新生成/启停/删除移进了单 key 管理面板，不再平铺在行内。
    // 两行 key → 保存与管理面板打开按钮各出现两次；限额设置/清零仍在卡片头部，各一次。
    expect(countOccurrences(html, '>Edit<')).toBe(0);
    expect(countOccurrences(html, '>Save<')).toBe(2);
    expect(countOccurrences(html, '>Regenerate<')).toBe(0);
    expect(countOccurrences(html, '>Disable<')).toBe(0);
    expect(countOccurrences(html, '>Delete<')).toBe(0);
    expect(countOccurrences(html, 'data-api-key-manage-open="9007199254740993"')).toBe(1);
    expect(countOccurrences(html, 'data-api-key-manage-open="9007199254740994"')).toBe(1);
    expect(countOccurrences(html, 'aria-haspopup="dialog"')).toBe(2);
    expect(countOccurrences(html, '>Quota<')).toBe(0);
    expect(countOccurrences(html, '>Quota settings<')).toBe(1);
    expect(countOccurrences(html, '>Reset quota<')).toBe(1);
    // 未提供对应生命周期 handler 时不渲染相应按钮；管理面板打开按钮始终渲染（行点击目标）。
    const bare = renderCard();
    expect(bare).not.toContain('>Edit<');
    expect(bare).not.toContain('>Regenerate<');
    expect(bare).not.toContain('>Disable<');
    expect(bare).not.toContain('>Delete<');
    expect(bare).not.toContain('>Quota settings<');
    expect(bare).not.toContain('>Reset quota<');
    expect(bare).toContain('>Save<');
    expect(countOccurrences(bare, 'aria-haspopup="dialog"')).toBe(2);
  });

  it('copies the raw key value', async () => {
    const writes: string[] = [];

    await copyApiKeyToClipboard(apiKeys[0].apiKey, { clipboard: { writeText: async (value) => { writes.push(value); } } });

    expect(writes).toEqual(['sk-alpha123456']);
  });

  it.each(['unavailable', 'blocked'])('uses textarea copy when Clipboard API is %s', async (state) => {
    const textarea = {
      value: '',
      readOnly: false,
      style: {},
      setAttribute: vi.fn(),
      focus: vi.fn(),
      select: vi.fn(),
      remove: vi.fn(),
    };
    const documentRef = {
      body: { appendChild: vi.fn() },
      createElement: vi.fn(() => textarea),
      execCommand: vi.fn(() => true),
    };

    const writeText = vi.fn(async () => { throw new Error('blocked'); });
    await copyApiKeyToClipboard(apiKeys[0].apiKey, {
      document: documentRef,
      clipboard: state === 'blocked' ? { writeText } : undefined,
    });
    expect(writeText).toHaveBeenCalledTimes(state === 'blocked' ? 1 : 0);

    expect(textarea.value).toBe('sk-alpha123456');
    expect(documentRef.body.appendChild).toHaveBeenCalledWith(textarea);
    expect(documentRef.execCommand).toHaveBeenCalledWith('copy');
    expect(textarea.remove).toHaveBeenCalledTimes(1);
  });

  it('renders empty and loading states', () => {
    expect(renderCard({ apiKeys: [], loading: true })).toContain('Loading...');
    expect(renderCard({ apiKeys: [], loading: false })).toContain('No CPA API keys synced yet.');
  });

  it('shows a temp adjustment badge only on the window with an active adjustment', () => {
    const limited: CpaApiKeySettingsItem = {
      ...apiKeys[0],
      policy: {
        enabled: true,
        enforcementState: 'active',
        tightest: null,
        limits: [
          { type: 'cost', window: 'weekly', value: 2600, used: 40.0631, ratio: 0.0154, baseValue: 2100, adjustmentUsd: 500 },
          { type: 'cost', window: 'monthly', value: 10, used: 12, ratio: 1.2 },
        ],
      },
    };
    const html = renderCard({ apiKeys: [limited] });
    expect(html).toContain('$40.0631 / $2600.0000');
    expect(html).toContain('temp +$500.00');
    expect(html).toContain('title="Normal limit: $2100.0000"');
    expect(html).toContain('$12.0000 / $10.0000');
    // 只有一个窗口带调整：徽章只出现一次。
    expect(countOccurrences(html, 'temp ')).toBe(1);
  });

  it('formats a negative temp adjustment with a true minus sign', () => {
    const limited: CpaApiKeySettingsItem = {
      ...apiKeys[0],
      policy: {
        enabled: true,
        enforcementState: 'active',
        tightest: null,
        limits: [{ type: 'cost', window: 'daily', value: 300, used: 10, ratio: 0.0333, baseValue: 500, adjustmentUsd: -200 }],
      },
    };
    const html = renderCard({ apiKeys: [limited] });
    expect(html).toContain('temp −$200.00');
  });
});
