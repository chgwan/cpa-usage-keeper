// @vitest-environment happy-dom

import { act, type ComponentProps } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { CpaApiKeySettingsItem } from '@/lib/types'
import { ApiKeyManageDialog } from '../ApiKeyManageDialog'

globalThis.IS_REACT_ACT_ENVIRONMENT = true

const translations: Record<string, string> = {
  'common.save': 'Save',
  'common.cancel': 'Cancel',
  'common.close': 'Close',
  'usage_stats.api_key_settings_regenerate': 'Regenerate',
  'usage_stats.api_key_settings_regenerate_confirm': 'The old key stops working immediately.',
  'usage_stats.api_key_settings_regenerate_blocked': 'Restore the key before regenerating it.',
  'usage_stats.api_key_settings_delete': 'Delete',
  'usage_stats.api_key_settings_delete_confirm': 'Delete this API key from CPA?',
  'usage_stats.api_key_settings_disable': 'Disable',
  'usage_stats.api_key_settings_restore': 'Restore',
  'usage_stats.api_key_settings_action_failed': 'Action failed',
  'usage_stats.api_key_settings_reveal_title': 'API key created',
  'usage_stats.api_key_settings_reveal_warning': 'Copy it now — it will not be shown again.',
  'usage_stats.api_key_settings_reveal_done': "I've saved it",
  'usage_stats.api_key_settings_copy': 'Copy',
  'usage_stats.api_key_settings_copy_success': 'API Key copied.',
  'usage_stats.api_key_settings_copy_failed': 'Unable to copy API Key.',
}

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => translations[key] ?? key }),
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

describe('ApiKeyManageDialog', () => {
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

  // 面板和确认层都是各自 Modal 的 portal，统一挂到 document.body；靠标题文案区分到底是哪一层。
  const dialogOf = (titleText: string) =>
    Array.from(document.body.querySelectorAll<HTMLElement>('.modal[role="dialog"][aria-modal="true"]'))
      .find((modal) => modal.querySelector('.modal-title')?.textContent === titleText)
  const buttonIn = (modal: HTMLElement | undefined, label: string) =>
    modal
      ? Array.from(modal.querySelectorAll<HTMLButtonElement>('button')).find((button) => button.textContent === label)
      : undefined

  const renderDialog = async (props: Partial<ComponentProps<typeof ApiKeyManageDialog>> = {}) => {
    const onClose = props.onClose ?? vi.fn()
    const onRegenerated = props.onRegenerated ?? vi.fn()
    await act(async () => {
      root.render(<ApiKeyManageDialog item={apiKey} onClose={onClose} onRegenerated={onRegenerated} {...props} />)
    })
    return onClose
  }

  it('titles the dialog with the alias and shows the key-preselected quota section, reset quota, lifecycle actions, and the embedded enforcement log', async () => {
    await renderDialog({
      onSavePolicies: vi.fn(async () => true),
      onResetQuotas: vi.fn(async () => true),
      onRegenerateKey: vi.fn(async () => null),
      onDisableKey: vi.fn(async () => true),
      onDeleteKey: vi.fn(async () => true),
    })
    const main = dialogOf('Primary')
    expect(main).toBeDefined()

    // 配额区块：只针对这一个 key，key 选择器隐藏，周期选择器照常渲染。
    expect(main!.querySelector('[data-api-key-manage-quota] [data-api-key-quota-select-all]')).toBeNull()
    expect(main!.querySelector(`[data-api-key-manage-quota] [data-api-key-quota-key="${apiKey.id}"]`)).toBeNull()
    expect(main!.querySelector('[data-api-key-manage-quota] [data-api-key-quota-period="weekly"]')).not.toBeNull()
    expect(buttonIn(main, 'Save')).toBeDefined()

    // 重置区块：默认勾选 weekly，和批量弹窗一致，但只作用于这一个 key。
    expect(main!.querySelector<HTMLInputElement>('[data-api-key-manage-reset] [data-api-key-quota-period="weekly"]')!.checked).toBe(true)

    // 生命周期动作从行内搬进了面板。
    expect(buttonIn(main, 'Regenerate')).toBeDefined()
    expect(buttonIn(main, 'Disable')).toBeDefined()
    expect(buttonIn(main, 'Delete')).toBeDefined()

    // 配额区块内嵌的执行审计入口（ApiKeyQuotaSection 内部已经渲染，不用再单独挂一份）。
    expect(main!.textContent).toContain('usage_stats.api_key_policy_logs')
  })

  it('omits a section entirely when its corresponding handler prop is not provided', async () => {
    await renderDialog({})
    const main = dialogOf('Primary')!

    expect(main.querySelector('[data-api-key-manage-quota]')).toBeNull()
    expect(main.querySelector('[data-api-key-manage-reset]')).toBeNull()
    expect(buttonIn(main, 'Regenerate')).toBeUndefined()
    expect(buttonIn(main, 'Disable')).toBeUndefined()
    expect(buttonIn(main, 'Restore')).toBeUndefined()
    expect(buttonIn(main, 'Delete')).toBeUndefined()
  })

  it('confirms before regenerating, then hands the new key up via onRegenerated and leaves the manage dialog open', async () => {
    const created = { id: apiKey.id, key: 'sk-new-key-999', keyAlias: apiKey.keyAlias }
    const onRegenerateKey = vi.fn(async () => created)
    const onRegenerated = vi.fn()
    await renderDialog({ onRegenerateKey, onRegenerated })
    const main = dialogOf('Primary')!

    await act(async () => {
      buttonIn(main, 'Regenerate')!.click()
    })
    const confirm = dialogOf('Regenerate')
    expect(confirm).toBeDefined()
    expect(confirm!.textContent).toContain('The old key stops working immediately.')

    await act(async () => {
      buttonIn(confirm, 'Regenerate')!.click()
      await Promise.resolve()
    })
    expect(onRegenerateKey).toHaveBeenCalledWith(apiKey.id)
    // Modal 由 open prop 受控、靠 350ms 动画淡出，不是整体卸载，这里断言进入关闭态而不是立刻从 DOM 消失。
    expect(confirm!.className).toContain('modal-closing')
    // 一次性 key 不再由面板自己展示：面板可能被父级整体卸载，必须交给卡片级 reveal 状态，这里只断言回调拿到了新 key。
    expect(onRegenerated).toHaveBeenCalledWith(created)
    expect(dialogOf('API key created')).toBeUndefined()
    expect(dialogOf('Primary')).toBeDefined()
  })

  it('confirms before deleting, then calls onDeleteKey and closes the entire manage dialog', async () => {
    const onDeleteKey = vi.fn(async () => true)
    const onClose = await renderDialog({ onDeleteKey })
    const main = dialogOf('Primary')!

    await act(async () => {
      buttonIn(main, 'Delete')!.click()
    })
    const confirm = dialogOf('Delete')
    expect(confirm).toBeDefined()
    expect(confirm!.textContent).toContain('Delete this API key from CPA?')

    await act(async () => {
      buttonIn(confirm, 'Delete')!.click()
      await Promise.resolve()
    })
    expect(onDeleteKey).toHaveBeenCalledWith(apiKey.id)
    // 删除成功后这个 key 不存在了，面板没有继续管理的意义：直接关闭整个弹窗。
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('disables Regenerate with an explanatory tooltip when the key is inactive, and swaps Disable for Restore', async () => {
    const inactiveKey: CpaApiKeySettingsItem = { ...apiKey, policy: { enabled: true, enforcementState: 'disabled_manual', tightest: null } }
    const onRestoreKey = vi.fn(async () => true)
    const onDisableKey = vi.fn(async () => true)
    await act(async () => {
      root.render(
        <ApiKeyManageDialog
          item={inactiveKey}
          onClose={vi.fn()}
          onRegenerateKey={vi.fn(async () => null)}
          onRegenerated={vi.fn()}
          onDisableKey={onDisableKey}
          onRestoreKey={onRestoreKey}
        />,
      )
    })
    const main = dialogOf('Primary')!
    const regenerateButton = buttonIn(main, 'Regenerate')!
    expect(regenerateButton.disabled).toBe(true)
    expect(regenerateButton.title).toBe('Restore the key before regenerating it.')
    expect(buttonIn(main, 'Disable')).toBeUndefined()

    await act(async () => {
      buttonIn(main, 'Restore')!.click()
      await Promise.resolve()
    })
    expect(onRestoreKey).toHaveBeenCalledWith(inactiveKey.id)
    expect(onDisableKey).not.toHaveBeenCalled()
  })

  it('requires at least one period before resetting quota, and otherwise scopes the reset to only this key', async () => {
    const onResetQuotas = vi.fn(async () => true)
    await renderDialog({ onResetQuotas })
    const main = dialogOf('Primary')!
    const reset = main.querySelector<HTMLElement>('[data-api-key-manage-reset]')!
    const weekly = reset.querySelector<HTMLInputElement>('[data-api-key-quota-period="weekly"]')!

    // 取消默认勾选的 weekly，变成零选中再提交。
    await act(async () => {
      weekly.click()
    })
    expect(weekly.checked).toBe(false)
    await act(async () => {
      reset.querySelector<HTMLButtonElement>('[data-api-key-quota-reset-submit]')!.click()
      await Promise.resolve()
    })
    expect(onResetQuotas).not.toHaveBeenCalled()
    expect(reset.textContent).toContain('usage_stats.api_key_quota_select_periods_required')

    // 重新勾选后可以正常提交；请求必须固定是单 key（allKeys:false），不会被误判成全局重置。
    await act(async () => {
      weekly.click()
    })
    await act(async () => {
      reset.querySelector<HTMLButtonElement>('[data-api-key-quota-reset-submit]')!.click()
      await Promise.resolve()
    })
    expect(onResetQuotas).toHaveBeenCalledWith({ allKeys: false, apiKeyIds: [apiKey.id], windows: ['weekly'] })
  })

  it('closes via Escape and the floating close button when no confirm layer is on top', async () => {
    const onClose = await renderDialog()
    const main = dialogOf('Primary')!
    main.querySelector<HTMLButtonElement>('.modal-close-floating')!.focus()

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    })
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('treats the regenerate/delete confirm as an inner modal stacked on the manage dialog: Escape closes only the confirm layer', async () => {
    const onClose = await renderDialog({ onRegenerateKey: vi.fn(async () => null) })
    const main = dialogOf('Primary')!

    await act(async () => {
      buttonIn(main, 'Regenerate')!.click()
    })
    const confirm = dialogOf('Regenerate')!
    // 焦点落在确认层内部，让 Modal 的 isTopmostModal 判定命中确认层，而不是回退到 DOM 顺序兜底。
    confirm.querySelector<HTMLButtonElement>('.modal-close-floating')!.focus()

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    })
    // 只有确认层进入关闭动画；管理面板自己的 Modal 实例保持原样，没有被同一次按键连带关闭。
    expect(confirm.className).toContain('modal-closing')
    expect(dialogOf('Primary')!.className).not.toContain('modal-closing')
    expect(onClose).not.toHaveBeenCalled()
  })
})
