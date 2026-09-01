import { useTranslation } from 'react-i18next'
import type { KeyClaudeQuotaResponse, UsageQuotaRow } from '@/lib/types'
import styles from './KeyViewerShell.module.scss'

const preferredQuotaOrder = ['five_hour', 'seven_day', 'seven_day_fable']

const quotaOrder = (row: UsageQuotaRow) => {
  const index = preferredQuotaOrder.indexOf(row.key)
  return index === -1 ? preferredQuotaOrder.length : index
}

const usedPercent = (row: UsageQuotaRow) => {
  if (Number.isFinite(row.usedPercent)) return Math.max(0, Math.min(100, row.usedPercent as number))
  if (Number.isFinite(row.remainingFraction)) return Math.max(0, Math.min(100, 100 - (row.remainingFraction as number) * 100))
  if (Number.isFinite(row.used) && Number.isFinite(row.limit) && (row.limit as number) > 0) {
    return Math.max(0, Math.min(100, ((row.used as number) / (row.limit as number)) * 100))
  }
  return null
}

const resetTime = (value: string | undefined, locale: string) => {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  return new Intl.DateTimeFormat(locale, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }).format(date)
}

export function KeyClaudeQuotaPanel({ data, loading, error }: {
  data: KeyClaudeQuotaResponse | null
  loading: boolean
  error: string
}) {
  const { t, i18n } = useTranslation()

  return (
    <section className={styles.claudeQuotaSection} aria-labelledby="key-claude-quota-title">
      <div className={styles.claudeQuotaHeading}>
        <div>
          <span className={styles.claudeQuotaEyebrow}>Claude</span>
          <h2 id="key-claude-quota-title">{t('key_overview.claude_quota_title')}</h2>
        </div>
      </div>

      {error && <div className={styles.claudeQuotaMessage}>{error}</div>}
      {loading && !data && <div className={styles.claudeQuotaMessage}>{t('common.loading')}</div>}
      {!loading && !error && data?.items.length === 0 && (
        <div className={styles.claudeQuotaMessage}>{t('key_overview.claude_quota_empty')}</div>
      )}

      {data && data.items.length > 0 && (
        <div className={styles.claudeQuotaAccounts}>
          {data.items.map((item, itemIndex) => {
            const rows = [...(item.quota ?? [])].sort((left, right) => quotaOrder(left) - quotaOrder(right))
            return (
              <div className={styles.claudeQuotaAccount} key={`${item.refreshed_at ?? 'quota'}-${itemIndex}`}>
                {(data.items.length > 1 || item.subscription?.plan) && (
                  <div className={styles.claudeQuotaAccountHeader}>
                    {data.items.length > 1 && (
                      <span className={styles.claudeQuotaAccountLabel}>{t('key_overview.claude_quota_account', { index: itemIndex + 1 })}</span>
                    )}
                    {item.subscription?.plan && <span className={styles.claudeQuotaPlan}>{item.subscription.plan}</span>}
                  </div>
                )}
                {rows.map((row) => {
                  const percent = usedPercent(row)
                  const reset = resetTime(row.resetAt, i18n.language)
                  return (
                    <div className={styles.claudeQuotaRow} key={row.key}>
                      <div className={styles.claudeQuotaRowHeader}>
                        <span>{t(`key_overview.claude_quota_window_${row.key}`, { defaultValue: row.label || row.key })}</span>
                        <strong>{percent === null ? '-' : `${Math.round(percent)}%`}</strong>
                      </div>
                      <div className={styles.claudeQuotaTrack}>
                        <span style={{ width: `${percent ?? 0}%` }} />
                      </div>
                      {reset && <div className={styles.claudeQuotaReset}>{t('key_overview.claude_quota_resets', { time: reset })}</div>}
                    </div>
                  )
                })}
              </div>
            )
          })}
        </div>
      )}
    </section>
  )
}