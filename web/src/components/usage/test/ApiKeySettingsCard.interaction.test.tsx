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

  it('copies the raw key, shows the success icon, and resets its label', async () => {
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
    const copyIcon = copyButton?.querySelector('svg')?.innerHTML

    await act(async () => {
      copyButton?.click()
      await Promise.resolve()
    })

    expect(writeText).toHaveBeenCalledWith(apiKey.apiKey)
    expect(onNotice).toHaveBeenCalledWith('success', 'API Key copied.')
    const copiedButton = container.querySelector<HTMLButtonElement>('button[aria-label="Copied"]')
    expect(copiedButton).not.toBeNull()
    expect(copiedButton?.querySelector('svg')?.innerHTML).not.toBe(copyIcon)

    await act(async () => vi.advanceTimersByTime(1600))
    expect(container.querySelector('button[aria-label="Copy"]')).not.toBeNull()
    expect(container.querySelector('button[aria-label="Copied"]')).toBeNull()
  })

  it('keeps the copy icon and reports an error when both copy paths fail', async () => {
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
      copyButton?.click()
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
    const onOpenPolicy = vi.fn()
    await renderCard({ onRegenerateKey, onOpenPolicy })

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
    expect(findRowButton('Quota')?.disabled).toBe(true)
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

  it('routes quota straight from the row to onOpenPolicy', async () => {
    const onOpenPolicy = vi.fn()
    await renderCard({ onOpenPolicy })

    await act(async () => {
      findRowButton('Quota')?.click()
      await Promise.resolve()
    })
    expect(onOpenPolicy).toHaveBeenCalledWith(apiKey.id)
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
})
