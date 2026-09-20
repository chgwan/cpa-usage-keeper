import React from 'react';
import '@/i18n';
import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { KeyQuotaPanel } from '../KeyQuotaPanel';
import type { KeyOverviewQuota } from '@/lib/types';

const quota: KeyOverviewQuota = {
  enforcementState: 'active',
  windows: [
    { window: 'daily', costUsd: 1.5 },
    { window: 'weekly', costUsd: 8.1, limit: 20, ratio: 0.405 },
    { window: 'monthly', costUsd: 23.4, limit: 60, ratio: 0.39 },
  ],
};

describe('KeyQuotaPanel', () => {
  it('always renders daily, weekly, and monthly cards', () => {
    const html = renderToStaticMarkup(<KeyQuotaPanel quota={quota} loading={false} />);
    expect(html).toContain('Cost Quota');
    expect(html).toContain('>Daily<');
    expect(html).toContain('>Weekly<');
    expect(html).toContain('>Monthly<');
    // 未配置日限额：日卡片只展示花费。
    expect(html).toContain('$1.5000');
    expect(html).toContain('$8.1000 / $20.0000');
    expect(html).toContain('$23.4000 / $60.0000');
  });

  it('shows the daily limit and bar when a daily limit exists', () => {
    const withDaily: KeyOverviewQuota = {
      ...quota,
      windows: [{ window: 'daily', costUsd: 1.5, limit: 5, ratio: 0.3 }, ...quota.windows.slice(1)],
    };
    const html = renderToStaticMarkup(<KeyQuotaPanel quota={withDaily} loading={false} />);
    expect(html).toContain('$1.5000 / $5.0000');
    expect(html).toContain('aria-valuenow="30"');
  });

  it('renders spend without a bar for windows lacking a limit', () => {
    const partial: KeyOverviewQuota = {
      enforcementState: 'active',
      windows: [
        { window: 'daily', costUsd: 1.5 },
        { window: 'weekly', costUsd: 8.1 },
        { window: 'monthly', costUsd: 23.4, limit: 60, ratio: 0.39 },
      ],
    };
    const html = renderToStaticMarkup(<KeyQuotaPanel quota={partial} loading={false} />);
    expect(html).toContain('aria-valuenow="39"');
    expect(html).toContain('No limit');
    expect(html).toContain('$8.1000');
    // 周窗口无限额：只有月度一个进度条。
    expect(html.split('role="progressbar"').length - 1).toBe(1);
  });

  it('rounds the weekly progress ratio to whole percent', () => {
    const html = renderToStaticMarkup(<KeyQuotaPanel quota={quota} loading={false} />);
    expect(html).toContain('aria-valuenow="41"');
  });

  it('marks breached windows and the disabled-by-quota state', () => {
    const breached: KeyOverviewQuota = {
      enforcementState: 'disabled_by_quota',
      windows: [
        { window: 'daily', costUsd: 6, limit: 5, ratio: 1.2 },
        { window: 'weekly', costUsd: 8.1 },
        { window: 'monthly', costUsd: 23.4 },
      ],
    };
    const html = renderToStaticMarkup(<KeyQuotaPanel quota={breached} loading={false} />);
    expect(html).toContain('data-breached="true"');
    expect(html).toContain('Disabled (quota)');
  });

  it('renders nothing without quota data', () => {
    expect(renderToStaticMarkup(<KeyQuotaPanel quota={null} loading={false} />)).toBe('');
  });
});
