import { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { Modal } from '@/components/ui/Modal';
import { Select } from '@/components/ui/Select';
import { fetchCpaApiKeyEnforcementLogs } from '@/lib/api';
import type { ApiKeyEnforcementLogEntry, ApiKeyLimitWindow, CpaApiKeySettingsItem } from '@/lib/types';
import styles from '@/pages/UsagePage.module.scss';

export const QUOTA_WINDOWS: readonly ApiKeyLimitWindow[] = ['daily', 'weekly', 'monthly'];
// 后端 enforcement-logs 的 limit 上限是 200；单 key 审阅 50 条足够。
const ENFORCEMENT_LOG_FETCH_LIMIT = 50;

export type QuotaEnforcementChoice = 'keep' | 'on' | 'off';

export interface QuotaResetRequest {
  allKeys: boolean;
  apiKeyIds: string[];
  windows: ApiKeyLimitWindow[];
}

export interface QuotaPolicyUpdateRequest {
  apiKeyIds: string[];
  limits: Partial<Record<ApiKeyLimitWindow, number | null>>;
  enabled?: boolean;
}

const orderedWindows = (windows: Iterable<ApiKeyLimitWindow>) => {
  const selected = new Set(windows);
  return QUOTA_WINDOWS.filter((window) => selected.has(window));
};

const keyLabel = (item: CpaApiKeySettingsItem) => item.keyAlias.trim() || item.displayKey;

// 选中全部 key 时走全局清零（对之后新建的 key 同样生效），否则只清零所选 key。
export function buildQuotaResetRequest(
  selectedIds: ReadonlySet<string>,
  allIds: readonly string[],
  windows: Iterable<ApiKeyLimitWindow>,
): { request: QuotaResetRequest | null; error: 'keys' | 'periods' | null } {
  const orderedSelection = allIds.filter((id) => selectedIds.has(id));
  if (orderedSelection.length === 0) return { request: null, error: 'keys' };
  const selectedWindows = orderedWindows(windows);
  if (selectedWindows.length === 0) return { request: null, error: 'periods' };
  const allKeys = orderedSelection.length === allIds.length;
  return {
    request: { allKeys, apiKeyIds: allKeys ? [] : orderedSelection, windows: selectedWindows },
    error: null,
  };
}

// 只提交所选窗口：空输入表示移除该窗口限额，未选窗口保持各 key 原值。
export function buildQuotaPolicyUpdateRequest(
  selectedIds: readonly string[],
  windows: Iterable<ApiKeyLimitWindow>,
  values: Partial<Record<ApiKeyLimitWindow, string>>,
  enforcement: QuotaEnforcementChoice,
): { request: QuotaPolicyUpdateRequest | null; error: 'keys' | 'periods' | 'value' | null } {
  if (selectedIds.length === 0) return { request: null, error: 'keys' };
  const selectedWindows = orderedWindows(windows);
  if (selectedWindows.length === 0 && enforcement === 'keep') return { request: null, error: 'periods' };
  const limits: Partial<Record<ApiKeyLimitWindow, number | null>> = {};
  for (const window of selectedWindows) {
    const raw = (values[window] ?? '').trim();
    if (!raw) {
      limits[window] = null;
      continue;
    }
    const value = Number(raw);
    if (!Number.isFinite(value) || value <= 0) return { request: null, error: 'value' };
    limits[window] = value;
  }
  const request: QuotaPolicyUpdateRequest = { apiKeyIds: [...selectedIds], limits };
  if (enforcement !== 'keep') request.enabled = enforcement === 'on';
  return { request, error: null };
}

interface ApiKeyPickerProps {
  apiKeys: CpaApiKeySettingsItem[];
  selected: ReadonlySet<string>;
  onChange: (next: Set<string>) => void;
  disabled?: boolean;
}

// 多选 key 列表：顶部“全选”三态复选框 + 按别名 / key 过滤。
function ApiKeyPicker({ apiKeys, selected, onChange, disabled = false }: ApiKeyPickerProps) {
  const { t } = useTranslation();
  const [filter, setFilter] = useState('');
  const visible = useMemo(() => {
    const needle = filter.trim().toLowerCase();
    if (!needle) return apiKeys;
    return apiKeys.filter((item) => keyLabel(item).toLowerCase().includes(needle) || item.displayKey.toLowerCase().includes(needle));
  }, [apiKeys, filter]);
  const allSelected = apiKeys.length > 0 && apiKeys.every((item) => selected.has(item.id));
  const someSelected = !allSelected && apiKeys.some((item) => selected.has(item.id));

  return (
    <fieldset className={styles.apiKeyQuotaFieldset} disabled={disabled}>
      <legend className={styles.apiKeyQuotaLegend}>
        {t('usage_stats.api_key_quota_keys_label')}
        <span className={styles.apiKeyQuotaCount}>{t('usage_stats.api_key_quota_keys_selected', { count: selected.size, total: apiKeys.length })}</span>
      </legend>
      <div className={styles.apiKeyQuotaPickerToolbar}>
        <label className={styles.apiKeyQuotaCheck}>
          <input
            type="checkbox"
            checked={allSelected}
            ref={(element) => {
              if (element) element.indeterminate = someSelected;
            }}
            onChange={() => onChange(allSelected ? new Set() : new Set(apiKeys.map((item) => item.id)))}
            data-api-key-quota-select-all
          />
          <span>{t('usage_stats.api_key_quota_select_all')}</span>
        </label>
        {apiKeys.length > 6 && (
          <Input
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
            placeholder={t('usage_stats.api_key_quota_filter_placeholder')}
            aria-label={t('usage_stats.api_key_quota_filter_placeholder')}
            className={`${styles.usagePillControl} ${styles.apiKeyQuotaFilter}`.trim()}
          />
        )}
      </div>
      <ul className={styles.apiKeyQuotaPicker}>
        {visible.map((item) => (
          <li key={item.id}>
            <label className={styles.apiKeyQuotaCheck} title={item.displayKey}>
              <input
                type="checkbox"
                checked={selected.has(item.id)}
                onChange={() => {
                  const next = new Set(selected);
                  if (next.has(item.id)) next.delete(item.id);
                  else next.add(item.id);
                  onChange(next);
                }}
                data-api-key-quota-key={item.id}
              />
              <span className={styles.apiKeyQuotaKeyName}>{keyLabel(item)}</span>
              {item.keyAlias.trim() && <span className={styles.apiKeyQuotaKeyMeta}>{item.displayKey}</span>}
            </label>
          </li>
        ))}
      </ul>
    </fieldset>
  );
}

interface PeriodPickerProps {
  selected: ReadonlySet<ApiKeyLimitWindow>;
  onToggle: (window: ApiKeyLimitWindow) => void;
  disabled?: boolean;
}

function PeriodPicker({ selected, onToggle, disabled = false }: PeriodPickerProps) {
  const { t } = useTranslation();
  return (
    <fieldset className={styles.apiKeyQuotaFieldset} disabled={disabled}>
      <legend className={styles.apiKeyQuotaLegend}>{t('usage_stats.api_key_quota_periods_label')}</legend>
      <div className={styles.apiKeyQuotaPeriods}>
        {QUOTA_WINDOWS.map((window) => (
          <label key={window} className={`${styles.apiKeyQuotaCheck} ${styles.apiKeyQuotaPeriodChip}`.trim()}>
            <input type="checkbox" checked={selected.has(window)} onChange={() => onToggle(window)} data-api-key-quota-period={window} />
            <span>{t(`usage_stats.api_key_policy_window_${window}`)}</span>
          </label>
        ))}
      </div>
    </fieldset>
  );
}

const toggleWindow = (current: ReadonlySet<ApiKeyLimitWindow>, window: ApiKeyLimitWindow) => {
  const next = new Set(current);
  if (next.has(window)) next.delete(window);
  else next.add(window);
  return next;
};

export interface ApiKeyQuotaResetModalProps {
  apiKeys: CpaApiKeySettingsItem[];
  onClose: () => void;
  onSubmit: (request: QuotaResetRequest) => Promise<boolean>;
}

// 默认：全部 key + 每周；提交前用户可改选。
export function ApiKeyQuotaResetModal({ apiKeys, onClose, onSubmit }: ApiKeyQuotaResetModalProps) {
  const { t } = useTranslation();
  const allIds = useMemo(() => apiKeys.map((item) => item.id), [apiKeys]);
  const [selectedKeys, setSelectedKeys] = useState<Set<string>>(() => new Set(allIds));
  const [windows, setWindows] = useState<Set<ApiKeyLimitWindow>>(() => new Set<ApiKeyLimitWindow>(['weekly']));
  const [error, setError] = useState<'keys' | 'periods' | null>(null);
  const [pending, setPending] = useState(false);

  const handleSubmit = useCallback(async () => {
    const built = buildQuotaResetRequest(selectedKeys, allIds, windows);
    setError(built.error);
    if (!built.request) return;
    setPending(true);
    try {
      if (await onSubmit(built.request)) onClose();
    } finally {
      setPending(false);
    }
  }, [allIds, onClose, onSubmit, selectedKeys, windows]);

  return (
    <Modal
      open
      title={t('usage_stats.api_key_quota_reset_title')}
      onClose={onClose}
      closeDisabled={pending}
      footer={
        <>
          <Button type="button" variant="secondary" onClick={onClose} disabled={pending}>{t('common.cancel')}</Button>
          <Button type="button" variant="danger" loading={pending} onClick={() => void handleSubmit()} data-api-key-quota-reset-submit>
            {t('usage_stats.api_key_quota_reset_submit')}
          </Button>
        </>
      }
    >
      <div className={styles.apiKeyPolicyStack}>
        <p className={styles.apiKeyPolicyUsageHint}>{t('usage_stats.api_key_quota_reset_hint')}</p>
        <ApiKeyPicker apiKeys={apiKeys} selected={selectedKeys} onChange={(next) => { setSelectedKeys(next); setError(null); }} disabled={pending} />
        <PeriodPicker selected={windows} onToggle={(window) => { setWindows((current) => toggleWindow(current, window)); setError(null); }} disabled={pending} />
        {error === 'keys' && <p className={styles.apiKeyPolicyError}>{t('usage_stats.api_key_quota_select_keys_required')}</p>}
        {error === 'periods' && <p className={styles.apiKeyPolicyError}>{t('usage_stats.api_key_quota_select_periods_required')}</p>}
      </div>
    </Modal>
  );
}

function formatLogNumber(type: string | null, value: number): string {
  return type === 'cost' ? value.toFixed(4) : value.toLocaleString();
}

function formatLogDate(createdAt: string): string {
  const date = new Date(createdAt);
  return Number.isNaN(date.getTime()) ? createdAt : date.toLocaleString();
}

// 只选中一个 key 时展示它的执行审计，替代原先逐行的配额弹窗。
function SingleKeyEnforcementLogs({ apiKeyId }: { apiKeyId: string }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [logs, setLogs] = useState<ApiKeyEnforcementLogEntry[]>([]);

  useEffect(() => {
    if (!open) return undefined;
    const controller = new AbortController();
    let active = true;
    fetchCpaApiKeyEnforcementLogs(apiKeyId, ENFORCEMENT_LOG_FETCH_LIMIT, controller.signal)
      .then((next) => { if (active) setLogs(next.items ?? []); })
      .catch(() => { if (active) setLogs([]); });
    return () => {
      active = false;
      controller.abort();
    };
  }, [apiKeyId, open]);

  const localize = (kind: 'action' | 'reason', value: string) => {
    const key = `usage_stats.api_key_log_${kind}_${value}`;
    const label = t(key);
    // 未知枚举值（i18next 返回 key 本身）回退展示原始字符串。
    return label === key ? value : label;
  };

  return (
    <section className={styles.apiKeyPolicyLogs}>
      <button type="button" className={styles.apiKeyPolicyLogsToggle} aria-expanded={open} onClick={() => setOpen((current) => !current)}>
        {t('usage_stats.api_key_policy_logs')}
      </button>
      {open && (logs.length === 0 ? (
        <p className={styles.apiKeyPolicyUsageHint}>{t('usage_stats.api_key_policy_logs_empty')}</p>
      ) : (
        <ul className={styles.apiKeyLogList}>
          {logs.map((entry) => {
            const actionLabel = localize('action', entry.action);
            const reasonLabel = localize('reason', entry.reason);
            const scope = entry.limitType && entry.window ? `${entry.limitType}/${entry.window}` : entry.detail;
            const values = entry.usedValue !== null && entry.limitValue !== null && entry.limitType
              ? `${formatLogNumber(entry.limitType, entry.usedValue)} / ${formatLogNumber(entry.limitType, entry.limitValue)}`
              : '';
            return (
              <li key={entry.id} className={styles.apiKeyLogItem}>
                <span className={styles.apiKeyLogHead}>{reasonLabel ? `${actionLabel} (${reasonLabel})` : actionLabel}</span>
                <span className={styles.apiKeyLogMeta}>{`${`${scope} ${values}`.trim()} — ${formatLogDate(entry.createdAt)}`.replace(/^ — /, '')}</span>
              </li>
            );
          })}
        </ul>
      ))}
    </section>
  );
}

export interface ApiKeyQuotaSettingsModalProps {
  apiKeys: CpaApiKeySettingsItem[];
  onClose: () => void;
  onSubmit: (request: QuotaPolicyUpdateRequest) => Promise<boolean>;
}

// 无任何默认：必须先选 key，再选要修改的周期；只选一个 key 时预填其当前限额。
export function ApiKeyQuotaSettingsModal({ apiKeys, onClose, onSubmit }: ApiKeyQuotaSettingsModalProps) {
  const { t } = useTranslation();
  const [selectedKeys, setSelectedKeys] = useState<Set<string>>(() => new Set());
  const [windows, setWindows] = useState<Set<ApiKeyLimitWindow>>(() => new Set());
  const [values, setValues] = useState<Partial<Record<ApiKeyLimitWindow, string>>>({});
  const [enforcement, setEnforcement] = useState<QuotaEnforcementChoice>('keep');
  const [error, setError] = useState<'keys' | 'periods' | 'value' | null>(null);
  const [pending, setPending] = useState(false);

  const selectedIds = useMemo(() => apiKeys.filter((item) => selectedKeys.has(item.id)).map((item) => item.id), [apiKeys, selectedKeys]);
  const singleKey = selectedIds.length === 1 ? apiKeys.find((item) => item.id === selectedIds[0]) ?? null : null;
  const currentLimit = useCallback((window: ApiKeyLimitWindow) => (
    singleKey?.policy?.limits?.find((limit) => limit.window === window)?.value
  ), [singleKey]);

  const handleToggleWindow = useCallback((window: ApiKeyLimitWindow) => {
    setError(null);
    const next = toggleWindow(windows, window);
    // 单 key 编辑时勾选周期即预填当前值，避免误把已有限额清空。
    if (next.has(window) && !windows.has(window)) {
      const existing = currentLimit(window);
      setValues((draft) => ({ ...draft, [window]: draft[window] ?? (existing !== undefined ? String(existing) : '') }));
    }
    setWindows(next);
  }, [currentLimit, windows]);

  const handleSubmit = useCallback(async () => {
    const built = buildQuotaPolicyUpdateRequest(selectedIds, windows, values, enforcement);
    setError(built.error);
    if (!built.request) return;
    setPending(true);
    try {
      if (await onSubmit(built.request)) onClose();
    } finally {
      setPending(false);
    }
  }, [enforcement, onClose, onSubmit, selectedIds, values, windows]);

  const enforcementOptions = useMemo(() => (['keep', 'on', 'off'] as const).map((value) => ({
    value,
    label: t(`usage_stats.api_key_quota_enforcement_${value}`),
  })), [t]);

  return (
    <Modal
      open
      title={t('usage_stats.api_key_quota_settings_title')}
      onClose={onClose}
      closeDisabled={pending}
      footer={
        <>
          <Button type="button" variant="secondary" onClick={onClose} disabled={pending}>{t('common.cancel')}</Button>
          <Button type="button" loading={pending} onClick={() => void handleSubmit()} data-api-key-quota-settings-submit>{t('common.save')}</Button>
        </>
      }
    >
      <div className={styles.apiKeyPolicyStack}>
        <p className={styles.apiKeyPolicyUsageHint}>{t('usage_stats.api_key_quota_settings_hint')}</p>
        <ApiKeyPicker
          apiKeys={apiKeys}
          selected={selectedKeys}
          onChange={(next) => {
            setSelectedKeys(next);
            setError(null);
            // 选择变化后清空草稿值，下次勾选周期时按新选择重新预填。
            setValues({});
            setWindows(new Set());
          }}
          disabled={pending}
        />
        <PeriodPicker selected={windows} onToggle={handleToggleWindow} disabled={pending || selectedIds.length === 0} />
        {windows.size > 0 && (
          <div className={styles.apiKeyPolicyGrid}>
            {QUOTA_WINDOWS.filter((window) => windows.has(window)).map((window) => {
              const existing = currentLimit(window);
              return (
                <div key={window} className={styles.apiKeyPolicyField}>
                  <Input
                    type="number"
                    inputMode="decimal"
                    step="any"
                    min={0}
                    label={`${t('usage_stats.api_key_policy_limit_cost')} · ${t(`usage_stats.api_key_policy_window_${window}`)}`}
                    placeholder={t('usage_stats.api_key_quota_no_limit_placeholder')}
                    value={values[window] ?? ''}
                    className={`${styles.usagePillControl} ${styles.apiKeyPolicyInput}`.trim()}
                    disabled={pending}
                    onChange={(event) => {
                      setError(null);
                      setValues((draft) => ({ ...draft, [window]: event.target.value }));
                    }}
                    data-api-key-quota-value={window}
                  />
                  {singleKey?.policy && (
                    <div className={styles.apiKeyPolicyUsageHint}>
                      {existing !== undefined
                        ? t('usage_stats.api_key_quota_current_limit', { value: existing.toFixed(4) })
                        : t('usage_stats.api_key_policy_no_limits')}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}
        <label className={styles.apiKeyQuotaEnforcement}>
          <span className={styles.apiKeyQuotaLegend}>{t('usage_stats.api_key_quota_enforcement_label')}</span>
          <Select
            value={enforcement}
            options={enforcementOptions}
            onChange={(value) => { setEnforcement(value as QuotaEnforcementChoice); setError(null); }}
            ariaLabel={t('usage_stats.api_key_quota_enforcement_label')}
            fullWidth={false}
            disabled={pending || selectedIds.length === 0}
          />
        </label>
        {error === 'keys' && <p className={styles.apiKeyPolicyError}>{t('usage_stats.api_key_quota_select_keys_required')}</p>}
        {error === 'periods' && <p className={styles.apiKeyPolicyError}>{t('usage_stats.api_key_quota_select_periods_required')}</p>}
        {error === 'value' && <p className={styles.apiKeyPolicyError}>{t('usage_stats.api_key_policy_invalid_value')}</p>}
        <p className={styles.apiKeyPolicyUsageHint}>{t('usage_stats.api_key_policy_window_hint')}</p>
        <p className={styles.apiKeyPolicyUsageHint}>{t('usage_stats.api_key_policy_cost_requires_pricing')}</p>
        {singleKey && <SingleKeyEnforcementLogs apiKeyId={singleKey.id} />}
      </div>
    </Modal>
  );
}
