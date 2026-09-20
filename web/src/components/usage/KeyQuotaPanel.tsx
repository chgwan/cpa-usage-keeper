import { useTranslation } from 'react-i18next';
import type { KeyOverviewQuota, KeyOverviewQuotaWindow } from '@/lib/types';
import styles from '@/pages/UsagePage.module.scss';

export interface KeyQuotaPanelProps {
  quota: KeyOverviewQuota | null;
  loading: boolean;
}

const formatUsd = (value: number) => `$${value.toFixed(4)}`;

// 额度是固定日历窗口（项目 TZ），与页面顶部的时间范围筛选无关。
export function KeyQuotaPanel({ quota, loading }: KeyQuotaPanelProps) {
  const { t } = useTranslation();
  if (loading || !quota) {
    return null;
  }
  // 三个日历窗口始终展示：未配置限额的窗口只显示当期花费。
  const visible = quota.windows;
  if (visible.length === 0) {
    return null;
  }
  const disabledByQuota = quota.enforcementState === 'disabled_by_quota';
  const renderCard = (item: KeyOverviewQuotaWindow) => {
    const percent = item.ratio !== undefined ? Math.round(item.ratio * 100) : null;
    const breached = (item.ratio !== undefined && item.ratio >= 1) || (disabledByQuota && item.limit !== undefined);
    return (
      <div key={item.window} className={styles.keyQuotaCard} data-breached={breached ? 'true' : undefined}>
        <div className={styles.keyQuotaCardHead}>
          <span className={styles.keyQuotaWindowLabel}>{t(`usage_stats.api_key_policy_window_${item.window}`)}</span>
          <span className={styles.keyQuotaResetHint}>{t(`key_overview.quota_resets_${item.window}`)}</span>
        </div>
        <div className={styles.keyQuotaValue}>
          {item.limit !== undefined
            ? `${formatUsd(item.costUsd)} / ${formatUsd(item.limit)}`
            : formatUsd(item.costUsd)}
        </div>
        {percent !== null ? (
          <div
            className={styles.keyQuotaBarTrack}
            role="progressbar"
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuenow={percent}
          >
            <span
              className={`${styles.keyQuotaBarFill} ${breached ? styles.keyQuotaBarFillBreached : ''}`.trim()}
              style={{ width: `${Math.min(percent, 100)}%` }}
            />
          </div>
        ) : (
          <div className={styles.keyQuotaResetHint}>{t('key_overview.quota_no_limit')}</div>
        )}
      </div>
    );
  };
  return (
    <section className={styles.keyQuotaSection}>
      <div className={styles.keyQuotaHeader}>
        <h2 className={styles.keyQuotaTitle}>{t('key_overview.quota_title')}</h2>
        {disabledByQuota && (
          <span className={`${styles.apiKeyBadge} ${styles.apiKeyBadgeBreached}`}>
            {t('usage_stats.api_key_settings_disabled_by_quota')}
          </span>
        )}
      </div>
      <div className={styles.keyQuotaGrid}>{visible.map(renderCard)}</div>
    </section>
  );
}
