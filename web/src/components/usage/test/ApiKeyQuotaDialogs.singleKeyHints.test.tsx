// @vitest-environment happy-dom

// item 5 回归：per-key 面板（hideKeyPicker）里的提示语和"当前调整"展示。
// ApiKeyQuotaSection 永远 hideKeyPicker=true，是验证这个场景最直接的组件。

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { CpaApiKeySettingsItem } from '@/lib/types'
import { ApiKeyQuotaSection } from '../ApiKeyQuotaDialogs'

globalThis.IS_REACT_ACT_ENVIRONMENT = true

const translations: Record<string, string> = {
  'common.save': 'Save',
  'usage_stats.api_key_quota_mode_switch_label': 'Quota mode',
  'usage_stats.api_key_quota_mode_normal': 'Normal limit',
  'usage_stats.api_key_quota_mode_temporary': 'Temporary adjustment',
  // 单 key 与多 key 的提示语必须是两份不同的文案，这样才能断言面板用的是哪一份。
  'usage_stats.api_key_quota_settings_hint': 'MULTI_KEY_NORMAL_HINT',
  'usage_stats.api_key_quota_settings_hint_single_key': 'SINGLE_KEY_NORMAL_HINT',
  'usage_stats.api_key_quota_adjustment_hint': 'MULTI_KEY_TEMP_HINT',
  'usage_stats.api_key_quota_adjustment_hint_single_key': 'SINGLE_KEY_TEMP_HINT',
  'usage_stats.api_key_quota_periods_label': 'Periods',
  'usage_stats.api_key_policy_window_daily': 'Daily',
  'usage_stats.api_key_policy_window_weekly': 'Weekly',
  'usage_stats.api_key_policy_window_monthly': 'Monthly',
  'usage_stats.api_key_quota_adjustment_amount_label': 'Adjustment amount (USD)',
  'usage_stats.api_key_quota_adjustment_apply': 'Apply adjustment',
  'usage_stats.api_key_quota_adjustment_clear': 'Clear adjustment',
  'usage_stats.api_key_quota_adjustment_current': 'Current: {{window}} {{value}}',
  'usage_stats.api_key_quota_adjustment_none': 'No temporary adjustment this period.',
  'usage_stats.api_key_policy_logs': 'Logs',
}

// 真正展开 {{placeholder}}，这样才能确认传进 t() 的 window/value 参数正确，不只是 key 命中。
const t = (key: string, options?: Record<string, string>) => {
  const template = translations[key] ?? key
  if (!options) return template
  return template.replace(/\{\{(\w+)\}\}/g, (_, name: string) => String(options[name] ?? ''))
}

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t }),
}))

// 单 key 面板场景：weekly 有正常限额 100 + 当前生效调整 +500（effective 600），daily 没有任何限额/调整。
const item: CpaApiKeySettingsItem = {
  id: '42',
  apiKey: 'sk-single123456',
  keyAlias: 'Single',
  displayKey: 'sk-*********123456',
  label: 'Single',
  lastSyncedAt: '2026-05-13T00:00:00Z',
  policy: {
    enabled: true,
    enforcementState: 'active',
    tightest: null,
    limits: [
      { type: 'cost', window: 'weekly', value: 600, used: 40, ratio: 40 / 600, baseValue: 100, adjustmentUsd: 500 },
    ],
  },
}

describe('ApiKeyQuotaSection single-key hints and current adjustment (item 5)', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  it('shows the single-key hint (not the multi-key one) in normal and temporary mode', () => {
    act(() => {
      root.render(
        <ApiKeyQuotaSection
          item={item}
          onSubmit={vi.fn(async () => true)}
          onAdjust={vi.fn(async () => null)}
          onClearAdjustments={vi.fn(async () => true)}
        />,
      )
    })
    expect(container.textContent).toContain('SINGLE_KEY_NORMAL_HINT')
    expect(container.textContent).not.toContain('MULTI_KEY_NORMAL_HINT')

    act(() => {
      container.querySelector<HTMLButtonElement>('[data-api-key-quota-mode="temporary"]')!.click()
    })
    expect(container.textContent).toContain('SINGLE_KEY_TEMP_HINT')
    expect(container.textContent).not.toContain('MULTI_KEY_TEMP_HINT')
  })

  it('shows the current active adjustment for a period that has one, and "no adjustment" for one that does not', () => {
    act(() => {
      root.render(
        <ApiKeyQuotaSection
          item={item}
          onSubmit={vi.fn(async () => true)}
          onAdjust={vi.fn(async () => null)}
          onClearAdjustments={vi.fn(async () => true)}
        />,
      )
    })
    act(() => {
      container.querySelector<HTMLButtonElement>('[data-api-key-quota-mode="temporary"]')!.click()
    })
    // 还没选任何周期时，不展示任何"当前调整"行。
    expect(container.querySelector('[data-api-key-quota-current-adjustment]')).toBeNull()

    act(() => {
      container.querySelector<HTMLInputElement>('input[data-api-key-quota-period="weekly"]')!.click()
    })
    const weeklyLine = container.querySelector('[data-api-key-quota-current-adjustment="weekly"]')
    expect(weeklyLine?.textContent).toBe('Current: Weekly +$500.00')

    act(() => {
      container.querySelector<HTMLInputElement>('input[data-api-key-quota-period="daily"]')!.click()
    })
    const dailyLine = container.querySelector('[data-api-key-quota-current-adjustment="daily"]')
    expect(dailyLine?.textContent).toBe('No temporary adjustment this period.')
  })
})
