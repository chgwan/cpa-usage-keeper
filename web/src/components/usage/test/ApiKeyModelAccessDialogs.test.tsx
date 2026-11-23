// @vitest-environment happy-dom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ApiKeyModelAccessState, CpaApiKeySettingsItem } from '@/lib/types'
import {
  ApiKeyModelAccessModal,
  ApiKeyModelAccessSection,
  buildModelAccessUpdateRequest,
  formatModelAccessTooltip,
  modelPatternMatches,
  normalizeModelPatterns,
} from '../ApiKeyModelAccessDialogs'
import { ApiKeySettingsCard } from '../ApiKeySettingsCard'

globalThis.IS_REACT_ACT_ENVIRONMENT = true

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, params?: Record<string, unknown>) => {
      if (!params) return key
      return `${key}:${JSON.stringify(params)}`
    },
  }),
}))

const alphaKey: CpaApiKeySettingsItem = {
  id: '101',
  apiKey: 'sk-alpha123456',
  keyAlias: 'Alpha',
  displayKey: 'sk-***123456',
  label: 'Alpha',
  lastSyncedAt: '2026-11-22T00:00:00Z',
  sourceType: 'native',
  accessProvider: '',
  policy: { enabled: true, enforcementState: 'active', tightest: null },
}

const betaKey: CpaApiKeySettingsItem = {
  id: '102',
  apiKey: 'sk-beta654321',
  keyAlias: 'Beta',
  displayKey: 'sk-***654321',
  label: 'Beta',
  lastSyncedAt: '2026-11-22T00:00:00Z',
  sourceType: 'native',
  accessProvider: '',
  policy: { enabled: true, enforcementState: 'active', tightest: null },
}

const sampleModelAccess: ApiKeyModelAccessState = {
  available: true,
  status: {
    version: '0.2.2',
    policyCount: 1,
    revision: 7,
    policyFile: 'plugins/key-model-access/config.toml',
    persistentUpdates: true,
    source: 'plugins/key-model-access/config.toml',
    updatedAt: '2026-11-22T00:00:00Z',
    failClosed: false,
    lastError: '',
  },
  models: ['claude-sonnet-4', 'gpt-5', 'gpt-5-preview'],
  policies: {
    '101': {
      allowModels: ['gpt-*'],
      denyModels: ['*-preview'],
    },
  },
}

describe('ApiKeyModelAccessDialogs pure logic', () => {
  it('matches wildcards across slashes and respects case sensitivity', () => {
    expect(modelPatternMatches('gpt-*', 'gpt-5-preview')).toBe(true)
    expect(modelPatternMatches('gpt-*', 'GPT-5')).toBe(false)
    expect(modelPatternMatches('models/*', 'models/gemini/2.5')).toBe(true)
    expect(modelPatternMatches('gpt-?', 'gpt-5')).toBe(true)
    expect(modelPatternMatches('gpt-?', 'gpt-50')).toBe(false)
    expect(modelPatternMatches('*', 'any/model.name+1')).toBe(true)
  })

  it('normalizes and deduplicates model patterns', () => {
    expect(normalizeModelPatterns([' gpt-* ', '', 'claude-sonnet-4', 'gpt-*'])).toEqual(['gpt-*', 'claude-sonnet-4'])
  })

  it('validates selection when building update requests', () => {
    expect(buildModelAccessUpdateRequest([], ['gpt-*'], [])).toEqual({ request: null, error: 'keys' })
    expect(buildModelAccessUpdateRequest(['101', '102'], [' gpt-* ', 'gpt-*'], [' *-preview '])).toEqual({
      request: {
        apiKeyIds: ['101', '102'],
        allowModels: ['gpt-*'],
        denyModels: ['*-preview'],
      },
      error: null,
    })
  })

  it('formats tooltip text cleanly', () => {
    expect(formatModelAccessTooltip({ allowModels: ['gpt-*'], denyModels: ['*-preview'] }, 'Allow', 'Deny'))
      .toBe('Allow: gpt-* · Deny: *-preview')
  })
})

describe('ApiKeyModelAccessDialogs UI', () => {
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

  it('prefills rules for a single selected key in batch modal and submits updated patterns', async () => {
    const onSubmit = vi.fn(async () => true)
    const onClose = vi.fn()
    const onReload = vi.fn(async () => true)

    await act(async () => {
      root.render(
        <ApiKeyModelAccessModal
          apiKeys={[alphaKey, betaKey]}
          modelAccess={sampleModelAccess}
          onClose={onClose}
          onSubmit={onSubmit}
          onReload={onReload}
        />,
      )
    })

    // Select Alpha — its existing rules should prefill.
    const alphaCheckbox = document.body.querySelector<HTMLInputElement>('[data-api-key-quota-key="101"]')!
    await act(async () => {
      alphaCheckbox.click()
    })

    expect(document.body.querySelector('[data-model-access-chip="allow:gpt-*"]')).not.toBeNull()
    expect(document.body.querySelector('[data-model-access-chip="deny:*-preview"]')).not.toBeNull()

    // Open allow picker, verify gpt-5 is marked derived by gpt-*, and select claude-sonnet-4.
    const allowToggle = document.body.querySelector<HTMLButtonElement>('[data-model-access-toggle="allow"]')!
    await act(async () => {
      allowToggle.click()
    })
    const derivedOption = document.body.querySelector<HTMLButtonElement>('[data-model-access-option="allow:gpt-5"]')!
    expect(derivedOption.disabled).toBe(true)

    const claudeOption = document.body.querySelector<HTMLButtonElement>('[data-model-access-option="allow:claude-sonnet-4"]')!
    await act(async () => {
      claudeOption.click()
    })

    // Trigger reload button from status bar.
    const reloadBtn = document.body.querySelector<HTMLButtonElement>('[data-model-access-reload]')!
    await act(async () => {
      reloadBtn.click()
    })
    expect(onReload).toHaveBeenCalledTimes(1)

    // Save batch modal.
    const submitBtn = document.body.querySelector<HTMLButtonElement>('[data-api-key-model-access-submit]')!
    await act(async () => {
      submitBtn.click()
    })
    expect(onSubmit).toHaveBeenCalledWith({
      apiKeyIds: ['101'],
      allowModels: ['gpt-*', 'claude-sonnet-4'],
      denyModels: ['*-preview'],
    })
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('supports adding custom wildcard patterns in per-key section', async () => {
    const onSubmit = vi.fn(async () => true)
    await act(async () => {
      root.render(
        <ApiKeyModelAccessSection
          item={betaKey}
          modelAccess={sampleModelAccess}
          onSubmit={onSubmit}
        />,
      )
    })

    const customInput = container.querySelector<HTMLInputElement>('[data-model-access-custom-input="deny"]')!
    const addButton = container.querySelector<HTMLButtonElement>('[data-model-access-custom-add="deny"]')!
    await act(async () => {
      const nativeValueSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
      nativeValueSetter.call(customInput, 'o3-*')
      customInput.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => {
      addButton.click()
    })

    expect(container.querySelector('[data-model-access-chip="deny:o3-*"]')).not.toBeNull()

    const submitBtn = container.querySelector<HTMLButtonElement>('[data-api-key-model-access-submit]')!
    await act(async () => {
      submitBtn.click()
    })
    expect(onSubmit).toHaveBeenCalledWith({
      apiKeyIds: ['102'],
      allowModels: [],
      denyModels: ['o3-*'],
    })
  })

  it('renders row badges, header button, and manage dialog section only when plugin is available', async () => {
    const onSaveModelAccess = vi.fn(async () => true)

    await act(async () => {
      root.render(
        <ApiKeySettingsCard
          apiKeys={[alphaKey, betaKey]}
          onSaveAlias={vi.fn()}
          modelAccess={{ available: false, models: [], policies: {} }}
          onSaveModelAccess={onSaveModelAccess}
        />,
      )
    })
    expect(container.querySelector('[data-api-key-model-access-open]')).toBeNull()
    expect(container.querySelector('[data-api-key-model-access-badge="101"]')).toBeNull()

    await act(async () => {
      root.render(
        <ApiKeySettingsCard
          apiKeys={[alphaKey, betaKey]}
          onSaveAlias={vi.fn()}
          modelAccess={sampleModelAccess}
          onSaveModelAccess={onSaveModelAccess}
        />,
      )
    })
    expect(container.querySelector('[data-api-key-model-access-open]')).not.toBeNull()
    expect(container.querySelector('[data-api-key-model-access-badge="101"]')).not.toBeNull()
    expect(container.querySelector('[data-api-key-model-access-badge="102"]')).toBeNull()

    // Clicking Alpha opens manage dialog containing embedded model access section.
    const manageBtn = container.querySelector<HTMLButtonElement>('[data-api-key-manage-open="101"]')!
    await act(async () => {
      manageBtn.click()
    })
    expect(document.body.querySelector('[data-api-key-manage-model-access]')).not.toBeNull()
  })
})
