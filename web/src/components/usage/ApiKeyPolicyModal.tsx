import { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { Modal } from '@/components/ui/Modal';
import { fetchCpaApiKeyEnforcementLogs, fetchCpaApiKeyPolicy, saveCpaApiKeyPolicy } from '@/lib/api';
import type {
  ApiKeyEnforcementLogEntry,
  ApiKeyLimitType,
  ApiKeyLimitWindow,
  ApiKeyPolicyLimit,
  ApiKeyPolicyResponse,
  ApiKeyPolicyUsage,
} from '@/lib/types';
import styles from '@/pages/UsagePage.module.scss';

const LIMIT_TYPES: readonly ApiKeyLimitType[] = ['requests', 'tokens', 'cost'];
const LIMIT_WINDOWS: readonly ApiKeyLimitWindow[] = ['daily', 'monthly'];
// 后端 enforcement-logs 的 limit 上限是 200；默认 50 条已足够弹窗审阅。
const ENFORCEMENT_LOG_FETCH_LIMIT = 50;

// 六个限额输入框以 `${type}:${window}` 为键，空串表示该槽位不限额。
export function limitsToForm(limits: ApiKeyPolicyLimit[]): Record<string, string> {
  const form: Record<string, string> = {};
  for (const limit of limits) {
    form[`${limit.type}:${limit.window}`] = String(limit.value);
  }
  return form;
}

// 校验失败时 error 返回触发问题的输入键（非 null），组件据此渲染本地化错误文案。
export function formToLimits(form: Record<string, string>): { limits: ApiKeyPolicyLimit[]; error: string | null } {
  const limits: ApiKeyPolicyLimit[] = [];
  for (const [key, raw] of Object.entries(form)) {
    const trimmed = raw.trim();
    if (!trimmed) {
      continue;
    }
    const value = Number(trimmed);
    if (!Number.isFinite(value) || value <= 0) {
      return { limits: [], error: key };
    }
    const [type, window] = key.split(':');
    limits.push({ type: type as ApiKeyLimitType, window: window as ApiKeyLimitWindow, value });
  }
  return { limits, error: null };
}

export interface ApiKeyPolicyModalProps {
  apiKeyId: string;
  apiKeyLabel: string;
  onClose: () => void;
  onSaved?: () => void;
  onNotice?: (kind: 'success' | 'info' | 'error', message: string) => void;
}

function formatUsageValue(usage: ApiKeyPolicyUsage | undefined, window: ApiKeyLimitWindow, type: ApiKeyLimitType): string {
  const bucket = usage?.[window];
  if (!bucket) {
    return '';
  }
  // 费用固定 4 位小数；请求与 Tokens 按本地化数字展示。
  return type === 'cost' ? bucket.costUsd.toFixed(4) : bucket[type].toLocaleString();
}

function formatLogNumber(type: string | null, value: number): string {
  return type === 'cost' ? value.toFixed(4) : value.toLocaleString();
}

function formatLogDate(createdAt: string): string {
  const date = new Date(createdAt);
  return Number.isNaN(date.getTime()) ? createdAt : date.toLocaleString();
}

export function ApiKeyPolicyModal({ apiKeyId, apiKeyLabel, onClose, onSaved, onNotice }: ApiKeyPolicyModalProps) {
  const { t } = useTranslation();
  const [policy, setPolicy] = useState<ApiKeyPolicyResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState('');
  const [form, setForm] = useState<Record<string, string>>({});
  const [enabled, setEnabled] = useState(true);
  const [validationError, setValidationError] = useState(false);
  const [saveError, setSaveError] = useState('');
  const [saving, setSaving] = useState(false);
  const [logs, setLogs] = useState<ApiKeyEnforcementLogEntry[]>([]);
  const [logsOpen, setLogsOpen] = useState(false);

  const applyPolicy = useCallback((next: ApiKeyPolicyResponse) => {
    setPolicy(next);
    setForm(limitsToForm(next.limits ?? []));
    setEnabled(next.enabled);
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    let active = true;
    setLoading(true);
    setLoadError('');
    fetchCpaApiKeyPolicy(apiKeyId, controller.signal)
      .then((next) => {
        if (!active) return;
        applyPolicy(next);
      })
      .catch((error: unknown) => {
        if (!active) return;
        setLoadError(error instanceof Error ? error.message : 'Failed to load CPA API key policy');
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    // 审计日志加载失败不阻塞策略编辑，仅在展开时显示为空。
    fetchCpaApiKeyEnforcementLogs(apiKeyId, ENFORCEMENT_LOG_FETCH_LIMIT, controller.signal)
      .then((next) => {
        if (active) setLogs(next.items ?? []);
      })
      .catch(() => {
        if (active) setLogs([]);
      });
    return () => {
      active = false;
      controller.abort();
    };
  }, [apiKeyId, applyPolicy]);

  const handleSave = useCallback(async () => {
    const { limits, error } = formToLimits(form);
    if (error !== null) {
      setValidationError(true);
      return;
    }
    setValidationError(false);
    setSaveError('');
    setSaving(true);
    try {
      await saveCpaApiKeyPolicy(apiKeyId, limits, enabled);
    } catch (error) {
      setSaveError(error instanceof Error ? error.message : 'Failed to save CPA API key policy');
      return;
    } finally {
      setSaving(false);
    }
    onNotice?.('success', t('usage_stats.api_key_policy_saved'));
    onSaved?.();
    try {
      applyPolicy(await fetchCpaApiKeyPolicy(apiKeyId));
    } catch {
      // 保存成功但重拉失败：保留当前表单内容，下次打开弹窗会重新加载。
    }
  }, [apiKeyId, applyPolicy, enabled, form, onNotice, onSaved, t]);

  const localizeLogValue = useCallback((kind: 'action' | 'reason', value: string) => {
    const key = `usage_stats.api_key_log_${kind}_${value}`;
    const label = t(key);
    // 未知枚举值（i18next 返回 key 本身）回退展示原始字符串。
    return label === key ? value : label;
  }, [t]);

  const enforcementState = policy?.enforcementState ?? 'active';
  const stateBadge = enforcementState === 'disabled_by_quota'
    ? t('usage_stats.api_key_settings_disabled_by_quota')
    : enforcementState === 'disabled_manual'
      ? t('usage_stats.api_key_settings_disabled_manual')
      : t('usage_stats.api_key_policy_state_active');
  const hasLimits = useMemo(() => Object.values(form).some((value) => value.trim() !== ''), [form]);
  const canEdit = !loading && !loadError;

  return (
    <Modal
      open
      title={t('usage_stats.api_key_policy_title')}
      onClose={onClose}
      closeDisabled={saving}
      footer={
        <>
          <Button type="button" variant="secondary" onClick={onClose} disabled={saving}>
            {t('common.close')}
          </Button>
          <Button type="button" loading={saving} disabled={!canEdit} onClick={() => void handleSave()}>
            {t('common.save')}
          </Button>
        </>
      }
    >
      <div className={styles.apiKeyPolicyStack}>
        <div className={styles.apiKeyPolicyHeader}>
          <span className={styles.apiKeyPolicyKeyLabel} title={apiKeyLabel}>{apiKeyLabel}</span>
          {/* 徽章只反映已取到的策略状态：加载中或加载失败时不展示，避免闪现 “Active”。 */}
          {!loading && policy && (
            <span
              className={`${styles.apiKeyBadge} ${enforcementState !== 'active' ? styles.apiKeyBadgeBreached : ''}`.trim()}
            >
              {stateBadge}
            </span>
          )}
        </div>

        {loading ? (
          <div className={styles.hint}>{t('common.loading')}</div>
        ) : loadError ? (
          <div className={styles.apiKeyPolicyError}>{loadError}</div>
        ) : (
          <>
            <button
              type="button"
              role="switch"
              aria-checked={enabled}
              className={`${styles.apiKeyPolicyToggle} ${enabled ? styles.apiKeyPolicyToggleActive : ''}`.trim()}
              onClick={() => setEnabled((current) => !current)}
              disabled={saving}
            >
              <span className={styles.apiKeyPolicyToggleLabel}>{t('usage_stats.api_key_policy_enabled')}</span>
              <span className={styles.apiKeyPolicyToggleTrack}>
                <span className={styles.apiKeyPolicyToggleThumb} />
              </span>
            </button>

            {/* 窗口语义提示：每天 / 每月都是日历窗口，明确写出各自的重置时间点。 */}
            <p className={styles.apiKeyPolicyUsageHint}>{t('usage_stats.api_key_policy_window_hint')}</p>

            <div className={styles.apiKeyPolicyGrid}>
              {LIMIT_TYPES.map((type) => LIMIT_WINDOWS.map((window) => {
                const key = `${type}:${window}`;
                const raw = form[key] ?? '';
                const usageHint = raw.trim() ? formatUsageValue(policy?.usage, window, type) : '';
                return (
                  <div key={key} className={styles.apiKeyPolicyField}>
                    <Input
                      type="number"
                      inputMode="decimal"
                      step="any"
                      min={0}
                      label={`${t(`usage_stats.api_key_policy_limit_${type}`)} · ${t(`usage_stats.api_key_policy_window_${window}`)}`}
                      placeholder="--"
                      value={raw}
                      className={`${styles.usagePillControl} ${styles.apiKeyPolicyInput}`.trim()}
                      disabled={saving}
                      onChange={(event) => {
                        setValidationError(false);
                        setForm((current) => ({ ...current, [key]: event.target.value }));
                      }}
                    />
                    {usageHint && (
                      <div className={styles.apiKeyPolicyUsageHint}>
                        {t('usage_stats.api_key_policy_current_usage', { value: usageHint })}
                      </div>
                    )}
                  </div>
                );
              }))}
            </div>

            {!hasLimits && <p className={styles.apiKeyPolicyUsageHint}>{t('usage_stats.api_key_policy_no_limits')}</p>}
            {validationError && (
              <p className={styles.apiKeyPolicyError}>{t('usage_stats.api_key_policy_invalid_value')}</p>
            )}
            {saveError && <p className={styles.apiKeyPolicyError}>{saveError}</p>}
            <p className={styles.apiKeyPolicyUsageHint}>{t('usage_stats.api_key_policy_cost_requires_pricing')}</p>

            <section className={styles.apiKeyPolicyLogs}>
              <button
                type="button"
                className={styles.apiKeyPolicyLogsToggle}
                aria-expanded={logsOpen}
                onClick={() => setLogsOpen((current) => !current)}
              >
                {t('usage_stats.api_key_policy_logs')}
              </button>
              {logsOpen && (logs.length === 0 ? (
                <p className={styles.apiKeyPolicyUsageHint}>{t('usage_stats.api_key_policy_logs_empty')}</p>
              ) : (
                <ul className={styles.apiKeyLogList}>
                  {logs.map((entry) => {
                    const actionLabel = localizeLogValue('action', entry.action);
                    const reasonLabel = localizeLogValue('reason', entry.reason);
                    const scope = entry.limitType && entry.window
                      ? `${entry.limitType}/${entry.window}`
                      : '';
                    const values = entry.usedValue !== null && entry.limitValue !== null
                      ? `${formatLogNumber(entry.limitType, entry.usedValue)} / ${formatLogNumber(entry.limitType, entry.limitValue)}`
                      : '';
                    return (
                      <li key={entry.id} className={styles.apiKeyLogItem}>
                        <span className={styles.apiKeyLogHead}>
                          {reasonLabel ? `${actionLabel} (${reasonLabel})` : actionLabel}
                        </span>
                        {(scope || values) && (
                          <span className={styles.apiKeyLogMeta}>
                            {`${scope ? `${scope} ${values}`.trim() : values} — ${formatLogDate(entry.createdAt)}`}
                          </span>
                        )}
                      </li>
                    );
                  })}
                </ul>
              ))}
            </section>
          </>
        )}
      </div>
    </Modal>
  );
}
