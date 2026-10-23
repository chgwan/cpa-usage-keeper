import { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { Modal } from '@/components/ui/Modal';
import { fetchCpaApiKeyEnforcementLogs } from '@/lib/api';
import type { ApiKeyEnforcementLogEntry, ApiKeyLimitWindow, CpaApiKeySettingsItem } from '@/lib/types';
import styles from '@/pages/UsagePage.module.scss';

export const QUOTA_WINDOWS: readonly ApiKeyLimitWindow[] = ['daily', 'weekly', 'monthly'];
// 后端 enforcement-logs 的 limit 上限是 200；单 key 审阅 50 条足够。
const ENFORCEMENT_LOG_FETCH_LIMIT = 50;
// 临时调整徽章用真正的减号（U+2212），而不是连字符。
const MINUS_SIGN = '−';

export interface QuotaResetRequest {
  allKeys: boolean;
  apiKeyIds: string[];
  windows: ApiKeyLimitWindow[];
}

export interface QuotaPolicyUpdateRequest {
  apiKeyIds: string[];
  limits: Partial<Record<ApiKeyLimitWindow, number | null>>;
}

export interface QuotaAdjustmentRequest {
  apiKeyIds: string[];
  windows: ApiKeyLimitWindow[];
  deltaUsd: number;
}

export interface QuotaAdjustmentResult {
  applied: { apiKeyId: string; window: ApiKeyLimitWindow }[];
  skipped: { apiKeyId: string; window: ApiKeyLimitWindow; reason: 'no_limit' }[];
}

// 徽章格式：正数带 +，负数用真正的减号；金额统一两位小数。
export function formatQuotaAdjustmentAmount(value: number): string {
  const sign = value < 0 ? MINUS_SIGN : '+';
  return `${sign}$${Math.abs(value).toFixed(2)}`;
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

// 单 key 面板里的重置请求：永远只针对这一个 key，不走批量弹窗“全选即 allKeys”的判断——
// 否则系统里恰好只有这一个 key 时，单 key 重置会被误判成全局 allKeys，连未来新建的 key 也会被影响。
export function buildSingleKeyQuotaResetRequest(
  apiKeyId: string,
  windows: Iterable<ApiKeyLimitWindow>,
): { request: QuotaResetRequest | null; error: 'periods' | null } {
  const selectedWindows = orderedWindows(windows);
  if (selectedWindows.length === 0) return { request: null, error: 'periods' };
  return { request: { allKeys: false, apiKeyIds: [apiKeyId], windows: selectedWindows }, error: null };
}

// 只提交所选窗口：空输入表示移除该窗口限额，未选窗口保持各 key 原值；必须至少选一个周期。
export function buildQuotaPolicyUpdateRequest(
  selectedIds: readonly string[],
  windows: Iterable<ApiKeyLimitWindow>,
  values: Partial<Record<ApiKeyLimitWindow, string>>,
): { request: QuotaPolicyUpdateRequest | null; error: 'keys' | 'periods' | 'value' | null } {
  if (selectedIds.length === 0) return { request: null, error: 'keys' };
  const selectedWindows = orderedWindows(windows);
  if (selectedWindows.length === 0) return { request: null, error: 'periods' };
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
  return { request: { apiKeyIds: [...selectedIds], limits }, error: null };
}

// 临时调整金额：支持 +500 / -200 / 500（无符号视为正数），拒绝空值、非数字与 0。
// QUOTA_ADJUSTMENT_MAX_ABS_DELTA_USD 和后端 quotaAdjustmentMaxAbsDeltaUSD 保持一致：
// 这里提前拦住超出上限的输入，避免往返一次请求才告诉用户超限。
const QUOTA_ADJUSTMENT_MAX_ABS_DELTA_USD = 1e9;

export function buildQuotaAdjustmentRequest(
  selectedIds: readonly string[],
  windows: Iterable<ApiKeyLimitWindow>,
  rawDelta: string,
): { request: QuotaAdjustmentRequest | null; error: 'keys' | 'periods' | 'value' | null } {
  if (selectedIds.length === 0) return { request: null, error: 'keys' };
  const selectedWindows = orderedWindows(windows);
  if (selectedWindows.length === 0) return { request: null, error: 'periods' };
  const trimmed = rawDelta.trim();
  if (!trimmed) return { request: null, error: 'value' };
  const deltaUsd = Number(trimmed);
  if (!Number.isFinite(deltaUsd) || deltaUsd === 0) return { request: null, error: 'value' };
  if (Math.abs(deltaUsd) > QUOTA_ADJUSTMENT_MAX_ABS_DELTA_USD) return { request: null, error: 'value' };
  return { request: { apiKeyIds: [...selectedIds], windows: selectedWindows, deltaUsd }, error: null };
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

// 导出：单 key 管理面板的「重置配额」区块复用同一个周期选择器，而不是另起一份。
export function PeriodPicker({ selected, onToggle, disabled = false }: PeriodPickerProps) {
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

export type QuotaDialogMode = 'normal' | 'temporary';

export interface ApiKeyQuotaSettingsModalProps {
  apiKeys: CpaApiKeySettingsItem[];
  onClose: () => void;
  onSubmit: (request: QuotaPolicyUpdateRequest) => Promise<boolean>;
  onAdjust: (request: QuotaAdjustmentRequest) => Promise<QuotaAdjustmentResult | null>;
  onClearAdjustments: (request: { apiKeyIds: string[]; windows: ApiKeyLimitWindow[] }) => Promise<boolean>;
  /** Task 6 的单 key 面板用它固定选中一个 key。 */
  initialSelectedIds?: string[];
  /** 为真时隐藏 key 多选器，配合 initialSelectedIds 固定为单 key 表单。 */
  hideKeyPicker?: boolean;
  /** 供可视化排查工具直接打开临时调整模式。 */
  initialMode?: QuotaDialogMode;
}

interface UseApiKeyQuotaFormOptions {
  apiKeys: CpaApiKeySettingsItem[];
  onSubmit: (request: QuotaPolicyUpdateRequest) => Promise<boolean>;
  onAdjust: (request: QuotaAdjustmentRequest) => Promise<QuotaAdjustmentResult | null>;
  onClearAdjustments: (request: { apiKeyIds: string[]; windows: ApiKeyLimitWindow[] }) => Promise<boolean>;
  initialSelectedIds?: string[];
  initialMode?: QuotaDialogMode;
  /** 提交/应用调整/清除调整成功后的收尾：弹窗版借它关闭整个 Modal；内嵌到单 key 面板时不传，表单原地保留。 */
  onDone?: () => void;
}

interface ApiKeyQuotaFormState {
  mode: QuotaDialogMode;
  onModeChange: (next: QuotaDialogMode) => void;
  selectedKeys: Set<string>;
  onKeysChange: (next: Set<string>) => void;
  windows: Set<ApiKeyLimitWindow>;
  onToggleWindow: (window: ApiKeyLimitWindow) => void;
  values: Partial<Record<ApiKeyLimitWindow, string>>;
  onValueChange: (window: ApiKeyLimitWindow, value: string) => void;
  adjustmentAmount: string;
  onAdjustmentAmountChange: (value: string) => void;
  adjustmentResult: QuotaAdjustmentResult | null;
  error: 'keys' | 'periods' | 'value' | null;
  pending: boolean;
  selectedIds: string[];
  singleKey: CpaApiKeySettingsItem | null;
  currentLimit: (window: ApiKeyLimitWindow) => number | undefined;
  currentAdjustment: (window: ApiKeyLimitWindow) => number | undefined;
  labelForId: (id: string) => string;
  handleSubmit: () => Promise<void>;
  handleApplyAdjustment: () => Promise<void>;
  handleClearAdjustments: () => Promise<void>;
}

// 抽成 hook 是为了让弹窗版（ApiKeyQuotaSettingsModal）与单 key 面板内嵌版（ApiKeyQuotaSection）
// 共用同一套状态与校验逻辑：外层容器不同（Modal+悬浮按钮 vs. 内嵌区块），逻辑不应该分叉成两份。
// 无任何默认：必须先选 key，再选要修改的周期；只选一个 key 时预填其常规限额（不含临时调整）。
function useApiKeyQuotaForm({
  apiKeys,
  onSubmit,
  onAdjust,
  onClearAdjustments,
  initialSelectedIds,
  initialMode = 'normal',
  onDone,
}: UseApiKeyQuotaFormOptions): ApiKeyQuotaFormState {
  const [mode, setMode] = useState<QuotaDialogMode>(initialMode);
  const [selectedKeys, setSelectedKeys] = useState<Set<string>>(() => new Set(initialSelectedIds ?? []));
  const [windows, setWindows] = useState<Set<ApiKeyLimitWindow>>(() => new Set());
  const [values, setValues] = useState<Partial<Record<ApiKeyLimitWindow, string>>>({});
  const [adjustmentAmount, setAdjustmentAmount] = useState('');
  const [adjustmentResult, setAdjustmentResult] = useState<QuotaAdjustmentResult | null>(null);
  const [error, setError] = useState<'keys' | 'periods' | 'value' | null>(null);
  const [pending, setPending] = useState(false);

  const selectedIds = useMemo(() => apiKeys.filter((item) => selectedKeys.has(item.id)).map((item) => item.id), [apiKeys, selectedKeys]);
  const singleKey = selectedIds.length === 1 ? apiKeys.find((item) => item.id === selectedIds[0]) ?? null : null;
  // 预填必须用常规限额（baseValue），而不是叠加临时调整后的生效值，否则保存会把调整焊死进常规限额。
  const currentLimit = useCallback((window: ApiKeyLimitWindow) => {
    const limit = singleKey?.policy?.limits?.find((entry) => entry.window === window);
    return limit ? limit.baseValue ?? limit.value : undefined;
  }, [singleKey]);

  // 单 key + 临时模式下用来展示"这个周期已经有调整了吗"：adjustmentUsd 缺失就是没有生效的调整。
  const currentAdjustment = useCallback((window: ApiKeyLimitWindow) => {
    const limit = singleKey?.policy?.limits?.find((entry) => entry.window === window);
    return limit?.adjustmentUsd;
  }, [singleKey]);

  const labelForId = useCallback((id: string) => {
    const found = apiKeys.find((item) => item.id === id);
    return found ? keyLabel(found) : id;
  }, [apiKeys]);

  const onToggleWindow = useCallback((window: ApiKeyLimitWindow) => {
    setError(null);
    const next = toggleWindow(windows, window);
    // 单 key 编辑时勾选周期即预填当前值，避免误把已有限额清空。
    if (next.has(window) && !windows.has(window)) {
      const existing = currentLimit(window);
      setValues((draft) => ({ ...draft, [window]: draft[window] ?? (existing !== undefined ? String(existing) : '') }));
    }
    setWindows(next);
  }, [currentLimit, windows]);

  const onModeChange = useCallback((next: QuotaDialogMode) => {
    setMode(next);
    setError(null);
    setAdjustmentResult(null);
  }, []);

  const onKeysChange = useCallback((next: Set<string>) => {
    setSelectedKeys(next);
    setError(null);
    // 选择变化后清空草稿值，下次勾选周期时按新选择重新预填。
    setValues({});
    setWindows(new Set());
    setAdjustmentResult(null);
  }, []);

  const onValueChange = useCallback((window: ApiKeyLimitWindow, value: string) => {
    setError(null);
    setValues((draft) => ({ ...draft, [window]: value }));
  }, []);

  const onAdjustmentAmountChange = useCallback((value: string) => {
    setError(null);
    setAdjustmentAmount(value);
  }, []);

  const handleSubmit = useCallback(async () => {
    const built = buildQuotaPolicyUpdateRequest(selectedIds, windows, values);
    setError(built.error);
    if (!built.request) return;
    setPending(true);
    try {
      if (await onSubmit(built.request)) onDone?.();
    } finally {
      setPending(false);
    }
  }, [onDone, onSubmit, selectedIds, values, windows]);

  const handleApplyAdjustment = useCallback(async () => {
    const built = buildQuotaAdjustmentRequest(selectedIds, windows, adjustmentAmount);
    setError(built.error);
    if (!built.request) return;
    setPending(true);
    try {
      const result = await onAdjust(built.request);
      if (!result) return;
      setAdjustmentResult(result);
      // 全部应用成功才收尾；有跳过项时留在原地让用户看清楚哪些 key/周期没有常规限额。
      if (result.skipped.length === 0) onDone?.();
    } finally {
      setPending(false);
    }
  }, [adjustmentAmount, onAdjust, onDone, selectedIds, windows]);

  const handleClearAdjustments = useCallback(async () => {
    if (selectedIds.length === 0) {
      setError('keys');
      return;
    }
    const selectedWindows = orderedWindows(windows);
    if (selectedWindows.length === 0) {
      setError('periods');
      return;
    }
    setPending(true);
    try {
      if (await onClearAdjustments({ apiKeyIds: selectedIds, windows: selectedWindows })) onDone?.();
    } finally {
      setPending(false);
    }
  }, [onClearAdjustments, onDone, selectedIds, windows]);

  return {
    mode,
    onModeChange,
    selectedKeys,
    onKeysChange,
    windows,
    onToggleWindow,
    values,
    onValueChange,
    adjustmentAmount,
    onAdjustmentAmountChange,
    adjustmentResult,
    error,
    pending,
    selectedIds,
    singleKey,
    currentLimit,
    currentAdjustment,
    labelForId,
    handleSubmit,
    handleApplyAdjustment,
    handleClearAdjustments,
  };
}

interface ApiKeyQuotaFormFieldsProps {
  apiKeys: CpaApiKeySettingsItem[];
  hideKeyPicker?: boolean;
  form: ApiKeyQuotaFormState;
}

// 常规/临时调整表单的共享内容：弹窗版与单 key 面板内嵌版渲染同一份 JSX，避免分叉维护两套。
function ApiKeyQuotaFormFields({ apiKeys, hideKeyPicker = false, form }: ApiKeyQuotaFormFieldsProps) {
  const { t } = useTranslation();
  const {
    mode, onModeChange, selectedKeys, onKeysChange, windows, onToggleWindow, values, onValueChange,
    adjustmentAmount, onAdjustmentAmountChange, adjustmentResult, error, pending, selectedIds, singleKey,
    currentLimit, currentAdjustment, labelForId,
  } = form;

  return (
    <div className={styles.apiKeyPolicyStack}>
      <div className={styles.apiKeyQuotaModeSwitch} role="group" aria-label={t('usage_stats.api_key_quota_mode_switch_label')}>
        {(['normal', 'temporary'] as const).map((option) => (
          <button
            key={option}
            type="button"
            className={`${styles.apiKeyQuotaModeOption} ${mode === option ? styles.apiKeyQuotaModeOptionActive : ''}`.trim()}
            aria-pressed={mode === option}
            disabled={pending}
            onClick={() => onModeChange(option)}
            data-api-key-quota-mode={option}
          >
            {t(`usage_stats.api_key_quota_mode_${option}`)}
          </button>
        ))}
      </div>
      <p className={styles.apiKeyPolicyUsageHint}>
        {/* 隐藏了 key 选择器（单 key 面板）时提示语不能再提"选择 API Key"，这里切一份单 key 文案。 */}
        {t(hideKeyPicker
          ? (mode === 'normal' ? 'usage_stats.api_key_quota_settings_hint_single_key' : 'usage_stats.api_key_quota_adjustment_hint_single_key')
          : (mode === 'normal' ? 'usage_stats.api_key_quota_settings_hint' : 'usage_stats.api_key_quota_adjustment_hint'))}
      </p>
      {!hideKeyPicker && (
        <ApiKeyPicker apiKeys={apiKeys} selected={selectedKeys} onChange={onKeysChange} disabled={pending} />
      )}
      <PeriodPicker selected={windows} onToggle={onToggleWindow} disabled={pending || selectedIds.length === 0} />
      {mode === 'normal' && windows.size > 0 && (
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
                  onChange={(event) => onValueChange(window, event.target.value)}
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
      {mode === 'temporary' && (
        <div className={styles.apiKeyPolicyField}>
          <Input
            inputMode="decimal"
            label={t('usage_stats.api_key_quota_adjustment_amount_label')}
            placeholder="+500"
            value={adjustmentAmount}
            className={`${styles.usagePillControl} ${styles.apiKeyPolicyInput}`.trim()}
            disabled={pending}
            onChange={(event) => onAdjustmentAmountChange(event.target.value)}
            data-api-key-quota-adjustment-amount
          />
        </div>
      )}
      {mode === 'temporary' && hideKeyPicker && singleKey && windows.size > 0 && (
        <div className={styles.apiKeyPolicyGrid}>
          {/* 单 key 面板里没有别的地方能看到"这个周期是不是已经调整过了"，这里按已选周期逐条列出。 */}
          {QUOTA_WINDOWS.filter((window) => windows.has(window)).map((window) => {
            const adjustment = currentAdjustment(window);
            return (
              <p key={window} className={styles.apiKeyPolicyUsageHint} data-api-key-quota-current-adjustment={window}>
                {adjustment !== undefined
                  ? t('usage_stats.api_key_quota_adjustment_current', {
                      window: t(`usage_stats.api_key_policy_window_${window}`),
                      value: formatQuotaAdjustmentAmount(adjustment),
                    })
                  : t('usage_stats.api_key_quota_adjustment_none')}
              </p>
            );
          })}
        </div>
      )}
      {mode === 'temporary' && adjustmentResult && adjustmentResult.skipped.length > 0 && (
        <div className={styles.apiKeyQuotaAdjustmentSkipped}>
          <p className={styles.apiKeyPolicyError}>{t('usage_stats.api_key_quota_adjustment_skipped_title')}</p>
          <ul className={styles.apiKeyQuotaAdjustmentSkippedList}>
            {adjustmentResult.skipped.map((entry) => (
              <li key={`${entry.apiKeyId}-${entry.window}`} className={styles.apiKeyQuotaAdjustmentSkippedItem}>
                {t('usage_stats.api_key_quota_adjustment_skipped_item', {
                  key: labelForId(entry.apiKeyId),
                  window: t(`usage_stats.api_key_policy_window_${entry.window}`),
                })}
              </li>
            ))}
          </ul>
        </div>
      )}
      {error === 'keys' && <p className={styles.apiKeyPolicyError}>{t('usage_stats.api_key_quota_select_keys_required')}</p>}
      {error === 'periods' && <p className={styles.apiKeyPolicyError}>{t('usage_stats.api_key_quota_select_periods_required')}</p>}
      {error === 'value' && (
        <p className={styles.apiKeyPolicyError}>
          {t(mode === 'normal' ? 'usage_stats.api_key_policy_invalid_value' : 'usage_stats.api_key_quota_adjustment_invalid_value')}
        </p>
      )}
      {mode === 'normal' && (
        <>
          <p className={styles.apiKeyPolicyUsageHint}>{t('usage_stats.api_key_policy_window_hint')}</p>
          <p className={styles.apiKeyPolicyUsageHint}>{t('usage_stats.api_key_policy_cost_requires_pricing')}</p>
        </>
      )}
      {singleKey && <SingleKeyEnforcementLogs apiKeyId={singleKey.id} />}
    </div>
  );
}

// 无任何默认：必须先选 key，再选要修改的周期；只选一个 key 时预填其常规限额（不含临时调整）。
export function ApiKeyQuotaSettingsModal({
  apiKeys,
  onClose,
  onSubmit,
  onAdjust,
  onClearAdjustments,
  initialSelectedIds,
  hideKeyPicker = false,
  initialMode = 'normal',
}: ApiKeyQuotaSettingsModalProps) {
  const { t } = useTranslation();
  const form = useApiKeyQuotaForm({ apiKeys, onSubmit, onAdjust, onClearAdjustments, initialSelectedIds, initialMode, onDone: onClose });

  return (
    <Modal
      open
      title={t('usage_stats.api_key_quota_settings_title')}
      onClose={onClose}
      closeDisabled={form.pending}
      footer={form.mode === 'normal' ? (
        <>
          <Button type="button" variant="secondary" onClick={onClose} disabled={form.pending}>{t('common.cancel')}</Button>
          <Button type="button" loading={form.pending} onClick={() => void form.handleSubmit()} data-api-key-quota-settings-submit>{t('common.save')}</Button>
        </>
      ) : (
        <>
          <Button type="button" variant="secondary" onClick={onClose} disabled={form.pending}>{t('common.cancel')}</Button>
          <Button type="button" variant="secondary" loading={form.pending} onClick={() => void form.handleClearAdjustments()} data-api-key-quota-adjustment-clear>
            {t('usage_stats.api_key_quota_adjustment_clear')}
          </Button>
          <Button type="button" loading={form.pending} onClick={() => void form.handleApplyAdjustment()} data-api-key-quota-adjustment-apply>
            {t('usage_stats.api_key_quota_adjustment_apply')}
          </Button>
        </>
      )}
    >
      <ApiKeyQuotaFormFields apiKeys={apiKeys} hideKeyPicker={hideKeyPicker} form={form} />
    </Modal>
  );
}

export interface ApiKeyQuotaSectionProps {
  /** 固定管理的单个 key：面板不提供多选器，永远只针对它。 */
  item: CpaApiKeySettingsItem;
  onSubmit: (request: QuotaPolicyUpdateRequest) => Promise<boolean>;
  onAdjust: (request: QuotaAdjustmentRequest) => Promise<QuotaAdjustmentResult | null>;
  onClearAdjustments: (request: { apiKeyIds: string[]; windows: ApiKeyLimitWindow[] }) => Promise<boolean>;
  initialMode?: QuotaDialogMode;
}

// 单 key 管理面板内嵌的配额表单：复用与弹窗版完全相同的状态 hook 与字段 JSX，
// 只是不再包一层 Modal（per-key 面板本身已经是一个 Modal，避免 Modal 套 Modal），
// 操作按钮改成内嵌在内容区而不是弹窗底部的 footer。
export function ApiKeyQuotaSection({ item, onSubmit, onAdjust, onClearAdjustments, initialMode }: ApiKeyQuotaSectionProps) {
  const { t } = useTranslation();
  const apiKeys = useMemo(() => [item], [item]);
  const form = useApiKeyQuotaForm({
    apiKeys,
    onSubmit,
    onAdjust,
    onClearAdjustments,
    initialSelectedIds: apiKeys.map((entry) => entry.id),
    initialMode,
  });

  return (
    <div className={styles.apiKeyQuotaSectionStack}>
      <ApiKeyQuotaFormFields apiKeys={apiKeys} hideKeyPicker form={form} />
      <div className={styles.apiKeyManageActions}>
        {form.mode === 'normal' ? (
          <Button type="button" loading={form.pending} onClick={() => void form.handleSubmit()} data-api-key-quota-settings-submit>
            {t('common.save')}
          </Button>
        ) : (
          <>
            <Button type="button" variant="secondary" loading={form.pending} onClick={() => void form.handleClearAdjustments()} data-api-key-quota-adjustment-clear>
              {t('usage_stats.api_key_quota_adjustment_clear')}
            </Button>
            <Button type="button" loading={form.pending} onClick={() => void form.handleApplyAdjustment()} data-api-key-quota-adjustment-apply>
              {t('usage_stats.api_key_quota_adjustment_apply')}
            </Button>
          </>
        )}
      </div>
    </div>
  );
}
