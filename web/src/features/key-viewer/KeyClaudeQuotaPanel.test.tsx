import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import '../../i18n'
import { KeyClaudeQuotaPanel } from './KeyClaudeQuotaPanel'

describe('KeyClaudeQuotaPanel', () => {
  it('renders each Claude quota cache independently without credential details', () => {
    const html = renderToStaticMarkup(createElement(KeyClaudeQuotaPanel, {
      data: {
        items: [
          {
            status: 'completed',
            subscription: { plan: 'Team' },
            quota: [
              { key: 'seven_day_fable', usedPercent: 9, resetAt: '2026-09-08T10:00:00Z' },
              { key: 'five_hour', usedPercent: 35, resetAt: '2026-09-01T15:00:00Z' },
              { key: 'seven_day', usedPercent: 72, resetAt: '2026-09-07T10:00:00Z' },
            ],
          },
          { status: 'completed', subscription: { plan: 'Enterprise' }, quota: [{ key: 'five_hour', usedPercent: 12 }] },
        ],
      },
      loading: false,
      error: '',
    }))

    expect(html).toContain('5-hour limit')
    expect(html).toContain('7-day limit')
    expect(html).toContain('7-day Fable 5')
    expect(html).toContain('35%')
    expect(html).toContain('Quota 1')
    expect(html).toContain('Quota 2')
    expect(html).toContain('Team')
    expect(html).toContain('Enterprise')
    expect(html).not.toContain('auth_index')
    expect(html).not.toContain('file_name')
  })
})