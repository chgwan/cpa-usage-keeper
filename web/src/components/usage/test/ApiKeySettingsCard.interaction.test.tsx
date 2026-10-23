// @vitest-environment happy-dom

import { act, type ComponentProps } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { CreatedApiKey, CpaApiKeySettingsItem } from '@/lib/types'
import { ApiKeySettingsCard } from '../ApiKeySettingsCard'

globalThis.IS_REACT_ACT_ENVIRONMENT = true

const translations: Record<string, string> = {
  'common.save': 'Save',
  'common.cancel': 'Cancel',
  'common.close': 'Close',
  'usage_stats.api_key_settings_title': 'API Key Settings',
  'usage_stats.api_key_settings_subtitle': 'Set display aliases.',
  'usage_stats.api_key_settings_display_key': 'API Key',
  'usage_stats.api_key_settings_alias': 'Alias',
  'usage_stats.api_key_settings_show_full': 'Show full API keys',
  'usage_stats.api_key_settings_copy': 'Copy',
  'usage_stats.api_key_settings_copied': 'Copied',
  'usage_stats.api_key_settings_copy_success': 'API Key copied.',
  'usage_stats.api_key_settings_copy_failed': 'Unable to copy API Key.',
  'usage_stats.api_key_settings_regenerate': 'Regenerate',
  'usage_stats.api_key_settings_regenerate_confirm': 'The old key stops working immediately.',
  'usage_stats.api_key_settings_regenerate_blocked': 'Restore the key before regenerating it.',
  'usage_stats.api_key_settings_delete': 'Delete',
  'usage_stats.api_key_settings_delete_confirm': 'Delete this API key from CPA?',
  'usage_stats.api_key_settings_disable': 'Disable',
  'usage_stats.api_key_settings_restore': 'Restore',
  'usage_stats.api_key_settings_quota': 'Quota',
  'usage_stats.api_key_settings_action_failed': 'Action failed',
  'usage_stats.api_key_settings_saving': 'Saving',
  'usage_stats.api_key_settings_reveal_title': 'API key created',
  'usage_stats.api_key_settings_reveal_warning': 'Copy it now — it will not be shown again.',
  'usage_stats.api_key_settings_reveal_done': "I've saved it",
  'usage_stats.api_key_settings_disabled_by_quota': 'Disabled (quota)',
  'usage_stats.api_key_settings_disabled_manual': 'Manually disabled',
  'usage_stats.api_key_settings_sort': 'Sort keys',
  'usage_stats.api_key_settings_sort_name_asc': 'Name A → Z',
  'usage_stats.api_key_settings_sort_name_desc': 'Name Z → A',
  'usage_stats.api_key_settings_sort_status': 'Status',
  'usage_stats.api_key_settings_sort_quota_ratio': 'Quota used %',
  'usage_stats.api_key_settings_sort_quota_spend': 'Quota spend',
}

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => translations[key] ?? key,
  }),
}))

const apiKey: CpaApiKeySettingsItem = {
  id: '9007199254740993',
  apiKey: 'sk-alpha123456',
  keyAlias: 'Primary',
  displayKey: 'sk-*********123456',
  label: 'Primary',
  lastSyncedAt: '2026-05-13T00:00:00Z',
  policy: { enabled: true, enforcementState: 'active', tightest: null },
}

describe('ApiKeySettingsCard copy action', () => {
  let container: HTMLDivElement
  let root: Root
  let clipboardDescriptor: PropertyDescriptor | undefined
  let execCommandDescriptor: PropertyDescriptor | undefined

  beforeEach(() => {
    clipboardDescriptor = Object.getOwnPropertyDescriptor(globalThis.navigator, 'clipboard')
    execCommandDescriptor = Object.getOwnPropertyDescriptor(document, 'execCommand')
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
    if (clipboardDescriptor) {
      Object.defineProperty(globalThis.navigator, 'clipboard', clipboardDescriptor)
    } else {
      delete (globalThis.navigator as Navigator & { clipboard?: Clipboard }).clipboard
    }
    if (execCommandDescriptor) {
      Object.defineProperty(document, 'execCommand', execCommandDescriptor)
    } else {
      delete (document as Document & { execCommand?: (command: string) => boolean }).execCommand
    }
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  const renderCard = async (onNotice: ReturnType<typeof vi.fn>) => {
    await act(async () => {
      root.render(
        <ApiKeySettingsCard
          apiKeys={[apiKey]}
          onSaveAlias={() => undefined}
          onNotice={onNotice}
        />,
      )
    })
  }

  it('copies the raw key and resets the accessible success label', async () => {
    vi.useFakeTimers()
    const writeText = vi.fn(async () => undefined)
    const onNotice = vi.fn()
    Object.defineProperty(globalThis.navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    })
    await renderCard(onNotice)

    const copyButton = container.querySelector<HTMLButtonElement>('button[aria-label="Copy"]')
    expect(copyButton).not.toBeNull()
    expect(container.textContent).toContain(apiKey.displayKey)
    expect(container.textContent).not.toContain(apiKey.apiKey)

    await act(async () => {
      copyButton!.click()
      await Promise.resolve()
    })

    expect(writeText).toHaveBeenCalledWith(apiKey.apiKey)
    expect(onNotice).toHaveBeenCalledWith('success', 'API Key copied.')
    const copiedButton = container.querySelector<HTMLButtonElement>('button[aria-label="Copied"]')
    expect(copiedButton).not.toBeNull()

    await act(async () => vi.advanceTimersByTime(1600))
    expect(container.querySelector('button[aria-label="Copy"]')).not.toBeNull()
    expect(container.querySelector('button[aria-label="Copied"]')).toBeNull()
  })

  it.each([true, false])('restores the current control after legacy copy, success: %s', async (success) => {
    Object.defineProperty(globalThis.navigator, 'clipboard', {
      configurable: true,
      value: { writeText: async () => { throw new Error('blocked') } },
    })
    Object.defineProperty(document, 'execCommand', { configurable: true, value: () => success })
    await renderCard(vi.fn())
    const copyButton = container.querySelector<HTMLButtonElement>('button[aria-label="Copy"]')!
    await act(async () => { copyButton.focus(); copyButton.click(); await Promise.resolve() })
    expect(document.activeElement).toBe(copyButton)
    expect(document.querySelector('textarea')).toBeNull()
  })

  it('keeps the copy action available when both copy paths fail', async () => {
    const writeText = vi.fn(async () => { throw new Error('blocked') })
    const execCommand = vi.fn(() => false)
    const onNotice = vi.fn()
    Object.defineProperty(globalThis.navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    })
    Object.defineProperty(document, 'execCommand', {
      configurable: true,
      value: execCommand,
    })
    await renderCard(onNotice)

    const copyButton = container.querySelector<HTMLButtonElement>('button[aria-label="Copy"]')
    expect(copyButton).not.toBeNull()

    await act(async () => {
      copyButton!.click()
      await Promise.resolve()
    })

    expect(writeText).toHaveBeenCalledWith(apiKey.apiKey)
    expect(execCommand).toHaveBeenCalledWith('copy')
    expect(onNotice).toHaveBeenCalledWith('error', 'Unable to copy API Key.')
    expect(container.querySelector('button[aria-label="Copy"]')).not.toBeNull()
    expect(container.querySelector('button[aria-label="Copied"]')).toBeNull()
  })
})

describe('ApiKeySettingsCard row actions', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
    vi.restoreAllMocks()
    // “记住排序选择”的测试会写 localStorage，清掉避免影响后面的测试。
    window.localStorage.clear()
  })

  // 确认弹窗通过 portal 挂到 document.body，行内动作按钮仍在 container 里。
  const findRowButton = (label: string) =>
    Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find((button) => button.textContent === label)
  const findModalButton = (label: string) =>
    Array.from(document.querySelectorAll<HTMLButtonElement>('.modal button'))
      .find((button) => button.textContent === label)
  const modalText = () => document.querySelector('.modal')?.textContent ?? ''

  const renderCard = async (props: Partial<ComponentProps<typeof ApiKeySettingsCard>>) => {
    await act(async () => {
      root.render(
        <ApiKeySettingsCard
          apiKeys={[apiKey]}
          onSaveAlias={() => undefined}
          onNotice={() => undefined}
          {...props}
        />,
      )
    })
  }

  it('regenerates through the inline confirm, blocks actions while busy, and reveals the new key once', async () => {
    let resolveRegenerate: (created: CreatedApiKey | null) => void = () => undefined
    const onRegenerateKey = vi.fn(() => new Promise<CreatedApiKey | null>((resolve) => {
      resolveRegenerate = resolve
    }))
    await renderCard({ onRegenerateKey })

    await act(async () => {
      findRowButton('Regenerate')?.click()
      await Promise.resolve()
    })
    expect(modalText()).toContain('The old key stops working immediately.')

    await act(async () => {
      findModalButton('Regenerate')?.click()
      await Promise.resolve()
    })
    expect(onRegenerateKey).toHaveBeenCalledWith(apiKey.id)
    // pendingId 忙态期间行内动作全部禁用。
    expect(findRowButton('Regenerate')?.disabled).toBe(true)
    expect(findRowButton('Save')?.disabled).toBe(true)

    await act(async () => {
      resolveRegenerate({ id: apiKey.id, key: 'sk-once-new', keyAlias: 'Primary' })
      await Promise.resolve()
    })
    expect(document.body.textContent).toContain('sk-once-new')
    expect(document.body.textContent).toContain('API key created')
    // 确认弹窗已进入关闭流程，一次性 reveal 弹窗成为当前可操作层。
    expect(findModalButton("I've saved it")).toBeDefined()
  })

  const secondKey: CpaApiKeySettingsItem = { ...apiKey, id: '42', apiKey: 'sk-beta', keyAlias: 'Beta', displayKey: 'sk-***beta', label: 'Beta' }
  const check = (selector: string) => document.querySelector<HTMLInputElement>(selector)!

  it('opens the quota reset dialog defaulting to all keys and the weekly period', async () => {
    const onResetQuotas = vi.fn(() => Promise.resolve(true))
    await renderCard({ apiKeys: [apiKey, secondKey], onResetQuotas })
    expect(findRowButton('Quota')).toBeUndefined()

    await act(async () => {
      container.querySelector<HTMLButtonElement>('[data-api-key-quota-reset-open]')!.click()
      await Promise.resolve()
    })
    expect(check(`[data-api-key-quota-key="${apiKey.id}"]`).checked).toBe(true)
    expect(check('[data-api-key-quota-key="42"]').checked).toBe(true)
    expect(check('[data-api-key-quota-period="weekly"]').checked).toBe(true)
    expect(check('[data-api-key-quota-period="daily"]').checked).toBe(false)

    await act(async () => {
      document.querySelector<HTMLButtonElement>('[data-api-key-quota-reset-submit]')!.click()
      await Promise.resolve()
    })
    expect(onResetQuotas).toHaveBeenLastCalledWith({ allKeys: true, apiKeyIds: [], windows: ['weekly'] })
  })

  it('requires keys in the quota settings dialog and submits only the chosen periods', async () => {
    const onSavePolicies = vi.fn(() => Promise.resolve(true))
    await renderCard({ apiKeys: [apiKey, secondKey], onSavePolicies })

    await act(async () => {
      container.querySelector<HTMLButtonElement>('[data-api-key-quota-settings-open]')!.click()
      await Promise.resolve()
    })
    // 无任何默认选择：直接保存只提示必须选 key。
    expect(check(`[data-api-key-quota-key="${apiKey.id}"]`).checked).toBe(false)
    expect(check('[data-api-key-quota-period="weekly"]').checked).toBe(false)
    await act(async () => {
      document.querySelector<HTMLButtonElement>('[data-api-key-quota-settings-submit]')!.click()
      await Promise.resolve()
    })
    expect(onSavePolicies).not.toHaveBeenCalled()
    expect(modalText()).toContain('usage_stats.api_key_quota_select_keys_required')

    await act(async () => {
      check('[data-api-key-quota-key="42"]').click()
    })
    await act(async () => {
      check('[data-api-key-quota-period="daily"]').click()
    })
    const input = check('[data-api-key-quota-value="daily"]')
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, '2.5')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => {
      document.querySelector<HTMLButtonElement>('[data-api-key-quota-settings-submit]')!.click()
      await Promise.resolve()
    })
    expect(onSavePolicies).toHaveBeenLastCalledWith({ apiKeyIds: ['42'], limits: { daily: 2.5 } })
  })

  it('blocks regenerate for a disabled key, offers restore, and deletes via the danger confirm', async () => {
    const disabledKey: CpaApiKeySettingsItem = {
      ...apiKey,
      id: '9007199254740994',
      policy: { enabled: true, enforcementState: 'disabled_manual', tightest: null },
    }
    const onRestoreKey = vi.fn(async () => true)
    const onDisableKey = vi.fn(async () => true)
    const onDeleteKey = vi.fn(async () => true)
    const onRegenerateKey = vi.fn(async () => null)
    await act(async () => {
      root.render(
        <ApiKeySettingsCard
          apiKeys={[disabledKey]}
          onSaveAlias={() => undefined}
          onNotice={() => undefined}
          onRegenerateKey={onRegenerateKey}
          onDeleteKey={onDeleteKey}
          onDisableKey={onDisableKey}
          onRestoreKey={onRestoreKey}
        />,
      )
    })

    expect(container.textContent).toContain('Manually disabled')
    // 非 active：重新生成禁用并给出提示，启停按钮切到“恢复”。
    expect(findRowButton('Regenerate')?.disabled).toBe(true)
    expect(findRowButton('Regenerate')?.title).toBe('Restore the key before regenerating it.')
    expect(findRowButton('Disable')).toBeUndefined()

    await act(async () => {
      findRowButton('Restore')?.click()
      await Promise.resolve()
    })
    expect(onRestoreKey).toHaveBeenCalledWith(disabledKey.id)
    expect(onDisableKey).not.toHaveBeenCalled()

    await act(async () => {
      findRowButton('Delete')?.click()
      await Promise.resolve()
    })
    expect(modalText()).toContain('Delete this API key from CPA?')

    await act(async () => {
      findModalButton('Delete')?.click()
      await Promise.resolve()
    })
    expect(onDeleteKey).toHaveBeenCalledWith(disabledKey.id)
  })

  it('reorders rows from the sort menu and remembers the choice', () => {
    window.localStorage.clear()
    const keys: CpaApiKeySettingsItem[] = [
      { ...apiKey, id: 'a', keyAlias: 'alpha', policy: { enabled: true, enforcementState: 'active', tightest: { type: 'cost', window: 'daily', value: 10, used: 1, ratio: 0.1 } } },
      { ...apiKey, id: 'b', keyAlias: 'bravo', policy: { enabled: true, enforcementState: 'active', tightest: { type: 'cost', window: 'daily', value: 10, used: 9, ratio: 0.9 } } },
    ]
    act(() => {
      root.render(<ApiKeySettingsCard apiKeys={keys} loading={false} savingId={null} onSaveAlias={() => undefined} />)
    })
    const rowIds = () => Array.from(container.querySelectorAll('[data-api-key-settings-item]')).map((row) => row.getAttribute('data-api-key-settings-item'))
    expect(rowIds()).toEqual(['a', 'b'])

    const trigger = container.querySelector<HTMLButtonElement>('[data-api-key-sort-trigger]')!
    act(() => { trigger.click() })
    expect(trigger.getAttribute('aria-expanded')).toBe('true')
    const options = Array.from(container.querySelectorAll('[data-api-key-sort-option]')).map((option) => option.getAttribute('data-api-key-sort-option'))
    expect(options).toEqual(['name_asc', 'name_desc', 'status', 'quota_ratio', 'quota_spend'])
    expect(container.querySelector('[data-api-key-sort-option="name_asc"]')!.getAttribute('aria-checked')).toBe('true')

    act(() => { container.querySelector<HTMLButtonElement>('[data-api-key-sort-option="quota_spend"]')!.click() })
    expect(rowIds()).toEqual(['b', 'a'])
    expect(container.querySelector('[role="menu"]')).toBeNull()
    expect(trigger.textContent).toContain('Quota spend')
    expect(window.localStorage.getItem('cpa-usage-keeper-api-key-settings-sort-v2')).toBe('quota_spend')
  })
})

describe('ApiKeySettingsCard expand action', () => {
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

  it('moves the card into a body overlay, keeps unsaved drafts, and collapses on Escape', () => {
    act(() => {
      root.render(<ApiKeySettingsCard apiKeys={[apiKey]} onSaveAlias={vi.fn()} />)
    })
    const aliasInput = () => document.querySelector<HTMLInputElement>('input[aria-label^="Alias"]')!
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
      setter.call(aliasInput(), 'Draft alias')
      aliasInput().dispatchEvent(new Event('input', { bubbles: true }))
    })

    act(() => {
      container.querySelector<HTMLButtonElement>('button[aria-label="usage_stats.api_key_settings_expand"]')!.click()
    })
    const dialog = document.body.querySelector('[role="dialog"][aria-modal="true"]')
    expect(dialog).not.toBeNull()
    expect(container.contains(dialog)).toBe(false)
    expect(aliasInput().value).toBe('Draft alias')
    expect(document.body.style.overflow).toBe('hidden')

    act(() => {
      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    })
    expect(document.body.querySelector('[role="dialog"][aria-modal="true"]')).toBeNull()
    expect(container.querySelector('button[aria-label="usage_stats.api_key_settings_expand"]')).not.toBeNull()
    expect(aliasInput().value).toBe('Draft alias')
    expect(document.body.style.overflow).toBe('')
  })

  it('closes only the sort menu on Escape, keeping the card expanded; a second Escape then collapses it', () => {
    act(() => {
      root.render(<ApiKeySettingsCard apiKeys={[apiKey]} onSaveAlias={vi.fn()} />)
    })
    act(() => {
      container.querySelector<HTMLButtonElement>('button[aria-label="usage_stats.api_key_settings_expand"]')!.click()
    })
    const dialog = () => document.body.querySelector('[role="dialog"][aria-modal="true"]')
    expect(dialog()).not.toBeNull()

    const trigger = document.body.querySelector<HTMLButtonElement>('[data-api-key-sort-trigger]')!
    act(() => { trigger.click() })
    expect(trigger.getAttribute('aria-expanded')).toBe('true')
    act(() => { trigger.focus() })

    // 用真实的冒泡 keydown（而非 window.dispatchEvent）让同一次 Escape 同时到达菜单自己的 onKeyDown 和展开卡片的 window 监听。
    act(() => {
      trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }))
    })
    // 核心断言：菜单自己处理掉 Escape 后，外层展开卡片不能被同一次按键连带收起。
    expect(dialog()).not.toBeNull()
    const triggerAfterFirstEscape = document.body.querySelector<HTMLButtonElement>('[data-api-key-sort-trigger]')
    expect(triggerAfterFirstEscape).not.toBeNull()
    expect(document.body.querySelector('[role="menu"]')).toBeNull()
    expect(triggerAfterFirstEscape!.getAttribute('aria-expanded')).toBe('false')
    // 焦点回到触发按钮。
    expect(document.activeElement).toBe(triggerAfterFirstEscape)

    act(() => {
      triggerAfterFirstEscape!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }))
    })
    expect(dialog()).toBeNull()
  })
})

describe('ApiKeySettingsCard expanded quota windows', () => {
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

  it('lists configured daily and monthly quotas in both compact and expanded views', () => {
    const limited: CpaApiKeySettingsItem = {
      ...apiKey,
      policy: {
        enabled: true,
        enforcementState: 'active',
        tightest: { type: 'cost', window: 'daily', value: 2, used: 1.5, ratio: 0.75 },
        limits: [
          { type: 'cost', window: 'daily', value: 2, used: 1.5, ratio: 0.75 },
          { type: 'cost', window: 'monthly', value: 10, used: 12, ratio: 1.2 },
        ],
      },
    }
    act(() => {
      root.render(<ApiKeySettingsCard apiKeys={[limited]} onSaveAlias={vi.fn()} />)
    })
    const rowsText = () => Array.from(document.querySelectorAll('[data-api-key-quota-windows] li')).map((row) => row.textContent)
    const expected = [
      'usage_stats.api_key_policy_window_daily$1.5000 / $2.000075%',
      'usage_stats.api_key_policy_window_monthly$12.0000 / $10.0000120%',
    ]
    expect(rowsText()).toEqual(expected)

    act(() => {
      container.querySelector<HTMLButtonElement>('button[aria-label="usage_stats.api_key_settings_expand"]')!.click()
    })
    expect(rowsText()).toEqual(expected)
  })
})
