import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import { Card } from '@/components/ui/Card';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { Modal } from '@/components/ui/Modal';
import { IconCheck, IconCopy, IconEye, IconEyeOff, IconMaximize2, IconMinimize2 } from '@/components/ui/icons';
import { useScrollBoundaryContainment } from '@/hooks/useScrollBoundaryContainment';
import { resolveQuotaProgress } from '@/lib/quotaProgress';
import type { CreatedApiKey, CpaApiKeySettingsItem } from '@/lib/types';
import { ApiKeyQuotaResetModal, ApiKeyQuotaSettingsModal, type QuotaPolicyUpdateRequest, type QuotaResetRequest } from './ApiKeyQuotaDialogs';
import { ApiKeySortMenu } from './ApiKeySortMenu';
import styles from '@/pages/UsagePage.module.scss';

type ClipboardWriter = Pick<Clipboard, 'writeText'>;
type CopyTextArea = {
  value: string;
  readOnly: boolean;
  style: {
    position?: string;
    opacity?: string;
    pointerEvents?: string;
    top?: string;
    left?: string;
  };
  setAttribute: (name: string, value: string) => void;
  focus: () => void;
  select: () => void;
  remove?: () => void;
};
type CopyDocument = {
  activeElement?: { isConnected: boolean; focus: (options?: FocusOptions) => void } | null;
  body?: {
    appendChild: (node: CopyTextArea) => unknown;
    removeChild?: (node: CopyTextArea) => unknown;
  };
  createElement?: (tagName: 'textarea') => CopyTextArea;
  execCommand?: (command: string) => boolean;
};
type CopyContext = {
  clipboard?: ClipboardWriter;
  document?: CopyDocument;
};

export type ApiKeySortKey = 'name_asc' | 'name_desc' | 'status' | 'quota_ratio' | 'quota_spend';

export const API_KEY_SORT_KEYS: readonly ApiKeySortKey[] = ['name_asc', 'name_desc', 'status', 'quota_ratio', 'quota_spend'];

export const DEFAULT_API_KEY_SORT: ApiKeySortKey = 'name_asc';

const API_KEY_SETTINGS_SORT_STORAGE_KEY = 'cpa-usage-keeper-api-key-settings-sort-v2';
// 旧版存的是 {alphabetical, status} 两个开关；首次读取时迁移为单一排序键。
const LEGACY_API_KEY_SETTINGS_SORT_STORAGE_KEY = 'cpa-usage-keeper-api-key-settings-sort';

const isApiKeySortKey = (value: unknown): value is ApiKeySortKey => (
  typeof value === 'string' && (API_KEY_SORT_KEYS as readonly string[]).includes(value)
);

export function resolveStoredApiKeySort(v2Raw: string | null, legacyJsonRaw: string | null): ApiKeySortKey {
  if (isApiKeySortKey(v2Raw)) {
    return v2Raw;
  }
  if (!legacyJsonRaw) {
    return DEFAULT_API_KEY_SORT;
  }
  try {
    const legacy = JSON.parse(legacyJsonRaw) as Record<string, unknown> | null;
    if (legacy?.status === 'asc' || legacy?.status === 'desc') {
      return 'status';
    }
    return legacy?.alphabetical === 'desc' ? 'name_desc' : DEFAULT_API_KEY_SORT;
  } catch {
    return DEFAULT_API_KEY_SORT;
  }
}

const getInitialSort = (): ApiKeySortKey => {
  try {
    if (typeof window === 'undefined') {
      return DEFAULT_API_KEY_SORT;
    }
    return resolveStoredApiKeySort(
      window.localStorage.getItem(API_KEY_SETTINGS_SORT_STORAGE_KEY),
      window.localStorage.getItem(LEGACY_API_KEY_SETTINGS_SORT_STORAGE_KEY),
    );
  } catch {
    return DEFAULT_API_KEY_SORT;
  }
};

const apiKeySortCollator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' });

// 状态排序：可用 → 手动停用 → 限额停用 → 状态未知。
const API_KEY_STATUS_RANK: Record<string, number> = {
  active: 0,
  disabled_manual: 1,
  disabled_by_quota: 2,
};

const apiKeyStatusRank = (item: CpaApiKeySettingsItem) => (
  API_KEY_STATUS_RANK[item.policy?.enforcementState ?? ''] ?? 3
);

const apiKeySortLabel = (item: CpaApiKeySettingsItem) => item.keyAlias.trim() || item.displayKey;

const compareApiKeyNames = (left: CpaApiKeySettingsItem, right: CpaApiKeySettingsItem) => (
  apiKeySortCollator.compare(apiKeySortLabel(left), apiKeySortLabel(right))
  || apiKeySortCollator.compare(left.displayKey, right.displayKey)
  || apiKeySortCollator.compare(left.id, right.id)
);

// 限额排序取最紧窗口（与行内进度条一致）；未配置限额的 key 无论如何都排在最后。
const apiKeyQuotaMetric = (sortKey: ApiKeySortKey) => {
  if (sortKey === 'quota_ratio') return (item: CpaApiKeySettingsItem) => item.policy?.tightest?.ratio;
  if (sortKey === 'quota_spend') return (item: CpaApiKeySettingsItem) => item.policy?.tightest?.used;
  return null;
};

// 每次只按一个键排序，名称 A → Z 作为所有排序的兜底次序。
export function sortApiKeySettingsItems(items: CpaApiKeySettingsItem[], sortKey: ApiKeySortKey) {
  const metric = apiKeyQuotaMetric(sortKey);
  return [...items].sort((left, right) => {
    if (sortKey === 'name_desc') {
      return compareApiKeyNames(right, left);
    }
    if (sortKey === 'status') {
      const byStatus = apiKeyStatusRank(left) - apiKeyStatusRank(right);
      if (byStatus !== 0) return byStatus;
    }
    if (metric) {
      const leftValue = metric(left);
      const rightValue = metric(right);
      if (leftValue === undefined || rightValue === undefined) {
        if (leftValue !== rightValue) return leftValue === undefined ? 1 : -1;
      } else if (leftValue !== rightValue) {
        return rightValue - leftValue;
      }
    }
    return compareApiKeyNames(left, right);
  });
}

const formatQuotaUsd = (value: number) => `$${value.toFixed(4)}`;

export function getApiKeySettingsVisibleKey(item: CpaApiKeySettingsItem, showFullApiKeys: boolean) {
  return showFullApiKeys && item.apiKey ? item.apiKey : item.displayKey;
}

export async function copyApiKeyToClipboard(apiKey: string, context: CopyContext = {}) {
  if (!apiKey) {
    return;
  }
  const clipboard = context.clipboard ?? globalThis.navigator?.clipboard;
  if (clipboard) {
    try {
      await clipboard.writeText(apiKey);
      return;
    } catch {
      // HTTP LAN pages can block navigator.clipboard; fall back to a selected textarea copy.
    }
  }
  const documentRef = context.document ?? (typeof document !== 'undefined' ? document as unknown as CopyDocument : undefined);
  const textarea = documentRef?.createElement?.('textarea');
  if (!documentRef?.body || !documentRef.execCommand || !textarea) {
    throw new Error('clipboard is not available');
  }
  textarea.value = apiKey;
  textarea.readOnly = true;
  textarea.setAttribute('aria-hidden', 'true');
  textarea.style.position = 'fixed';
  textarea.style.opacity = '0';
  textarea.style.pointerEvents = 'none';
  textarea.style.top = '0';
  textarea.style.left = '0';
  const previouslyFocused = documentRef.activeElement;
  documentRef.body.appendChild(textarea);
  textarea.focus();
  textarea.select();
  try {
    if (!documentRef.execCommand('copy')) {
      throw new Error('copy command failed');
    }
  } finally {
    if (textarea.remove) {
      textarea.remove();
    } else {
      documentRef.body.removeChild?.(textarea);
    }
    // 兼容复制借用焦点后还给原控件，避免一次复制打断页面的键盘操作。
    if (previouslyFocused?.isConnected) previouslyFocused.focus({ preventScroll: true });
  }
}

// 一次性 key 展示状态机：完整 key 只在 reveal 弹窗存在，关闭即丢弃。
export type RevealState =
  | { phase: 'idle' }
  | { phase: 'revealed'; id: string; key: string };

export function nextRevealState(_current: RevealState, created: CreatedApiKey | null): RevealState {
  if (!created) {
    // 关闭弹窗即丢弃完整 key，后端不会再提供第二次。
    return { phase: 'idle' };
  }
  return { phase: 'revealed', id: created.id, key: created.key };
}

type ConfirmAction =
  | { kind: 'regenerate'; id: string }
  | { kind: 'delete'; id: string };

export interface ApiKeySettingsCardProps {
  apiKeys: CpaApiKeySettingsItem[];
  loading?: boolean;
  savingId?: string | null;
  onSaveAlias: (id: string, keyAlias: string) => void | Promise<void>;
  onNotice?: (kind: 'success' | 'info' | 'error', message: string) => void;
  onCreateKey?: (alias: string, customKey: string) => Promise<CreatedApiKey | null>;
  onRegenerateKey?: (id: string) => Promise<CreatedApiKey | null>;
  onDeleteKey?: (id: string) => Promise<boolean>;
  onDisableKey?: (id: string) => Promise<boolean>;
  onRestoreKey?: (id: string) => Promise<boolean>;
  /** 批量限额设置：必须选 key 与周期，返回是否成功。 */
  onSavePolicies?: (request: QuotaPolicyUpdateRequest) => Promise<boolean>;
  /** 限额临时清零：默认全部 key + 每周，返回是否成功。 */
  onResetQuotas?: (request: QuotaResetRequest) => Promise<boolean>;
}

export function ApiKeySettingsCard({
  apiKeys,
  loading = false,
  savingId = null,
  onSaveAlias,
  onNotice,
  onCreateKey,
  onRegenerateKey,
  onDeleteKey,
  onDisableKey,
  onRestoreKey,
  onSavePolicies,
  onResetQuotas,
}: ApiKeySettingsCardProps) {
  const { t } = useTranslation();
  const [showFullApiKeys, setShowFullApiKeys] = useState(false);
  const [sortKey, setSortKey] = useState<ApiKeySortKey>(getInitialSort);
  // 展开态把同一张卡片挂到 body 上的全屏遮罩里；状态都在本组件内，切换时草稿不丢。
  const [expanded, setExpanded] = useState(false);
  const sortedApiKeys = useMemo(() => sortApiKeySettingsItems(apiKeys, sortKey), [apiKeys, sortKey]);
  const [copiedId, setCopiedId] = useState<string | null>(null);
  const copyResetTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const apiKeySettingsBodyRef = useRef<HTMLDivElement | null>(null);
  useScrollBoundaryContainment(apiKeySettingsBodyRef);
  const initialAliases = useMemo(
    () => Object.fromEntries(apiKeys.map((item) => [item.id, item.keyAlias])),
    [apiKeys],
  );
  const [draftAliases, setDraftAliases] = useState<Record<string, string>>(initialAliases);

  const [reveal, setReveal] = useState<RevealState>({ phase: 'idle' });
  const [pendingId, setPendingId] = useState<string | null>(null);
  // 行内重新生成 / 删除需要二次确认；同一时刻最多一个动作待确认。
  const [confirmAction, setConfirmAction] = useState<ConfirmAction | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [createPending, setCreatePending] = useState(false);
  const [createAlias, setCreateAlias] = useState('');
  const [createCustomKey, setCreateCustomKey] = useState('');

  useEffect(() => {
    setDraftAliases(initialAliases);
  }, [initialAliases]);

  useEffect(() => () => {
    if (copyResetTimerRef.current) {
      clearTimeout(copyResetTimerRef.current);
    }
  }, []);

  const handleCopyApiKey = useCallback(async (item: CpaApiKeySettingsItem) => {
    try {
      await copyApiKeyToClipboard(item.apiKey);
      setCopiedId(item.id);
      onNotice?.('success', t('usage_stats.api_key_settings_copy_success'));
      if (copyResetTimerRef.current) {
        clearTimeout(copyResetTimerRef.current);
      }
      copyResetTimerRef.current = setTimeout(() => setCopiedId(null), 1600);
    } catch {
      setCopiedId(null);
      onNotice?.('error', t('usage_stats.api_key_settings_copy_failed'));
    }
  }, [onNotice, t]);

  const handleCopyRevealKey = useCallback(async () => {
    if (reveal.phase !== 'revealed') {
      return;
    }
    try {
      await copyApiKeyToClipboard(reveal.key);
      onNotice?.('success', t('usage_stats.api_key_settings_copy_success'));
    } catch {
      onNotice?.('error', t('usage_stats.api_key_settings_copy_failed'));
    }
  }, [onNotice, reveal, t]);

  const closeConfirm = useCallback(() => {
    setConfirmAction(null);
  }, []);

  // 行级动作统一走 pendingId 忙态；处理器返回 false/null 时只提示，不抛错。
  const runRowAction = useCallback(async <T,>(id: string, action: () => Promise<T>): Promise<T | null> => {
    setPendingId(id);
    try {
      const result = await action();
      if (!result) {
        onNotice?.('error', t('usage_stats.api_key_settings_action_failed'));
      }
      return result;
    } finally {
      setPendingId(null);
      closeConfirm();
    }
  }, [closeConfirm, onNotice, t]);

  const handleCreateKey = useCallback(async () => {
    if (!onCreateKey) {
      return;
    }
    setCreatePending(true);
    try {
      const created = await onCreateKey(createAlias.trim(), createCustomKey.trim());
      if (created) {
        setCreateAlias('');
        setCreateCustomKey('');
        setCreateOpen(false);
        setReveal((current) => nextRevealState(current, created));
      } else {
        onNotice?.('error', t('usage_stats.api_key_settings_action_failed'));
      }
    } finally {
      setCreatePending(false);
    }
  }, [createAlias, createCustomKey, onCreateKey, onNotice, t]);

  const handleRegenerateKey = useCallback(async (id: string) => {
    if (!onRegenerateKey) {
      return;
    }
    const created = await runRowAction(id, () => onRegenerateKey(id));
    if (created) {
      // 重新生成成功后立刻进入一次性 reveal 弹窗。
      setReveal((current) => nextRevealState(current, created));
    }
  }, [onRegenerateKey, runRowAction]);

  const handleDeleteKey = useCallback(async (id: string) => {
    if (!onDeleteKey) {
      return;
    }
    await runRowAction(id, () => onDeleteKey(id));
  }, [onDeleteKey, runRowAction]);

  // 行内启停切换：仍走 pendingId 忙态，结果只提示不抛错。
  const handleToggleEnforcement = useCallback(async (id: string, restore: boolean) => {
    const action = restore ? onRestoreKey : onDisableKey;
    if (!action) {
      return;
    }
    await runRowAction(id, () => action(id));
  }, [onDisableKey, onRestoreKey, runRowAction]);

  const confirmPending = confirmAction !== null && pendingId === confirmAction.id;
  const [quotaDialog, setQuotaDialog] = useState<'settings' | 'reset' | null>(null);
  const innerModalOpen = createOpen || reveal.phase === 'revealed' || confirmAction !== null || quotaDialog !== null;

  useEffect(() => {
    if (!expanded) {
      return undefined;
    }
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    return () => {
      document.body.style.overflow = previousOverflow;
    };
  }, [expanded]);

  useEffect(() => {
    // 内层弹窗打开时 Esc 交给弹窗自己处理，避免一次按键同时收起卡片。
    if (!expanded || innerModalOpen) {
      return undefined;
    }
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setExpanded(false);
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [expanded, innerModalOpen]);

  const changeSort = useCallback((next: ApiKeySortKey) => {
    setSortKey(next);
    try {
      window.localStorage.setItem(API_KEY_SETTINGS_SORT_STORAGE_KEY, next);
    } catch {
      // 存储不可用（隐私模式等）时只在本次会话内生效。
    }
  }, []);
  const toggleLabel = showFullApiKeys
    ? t('usage_stats.api_key_settings_hide_full')
    : t('usage_stats.api_key_settings_show_full');
  const expandLabel = expanded
    ? t('usage_stats.api_key_settings_collapse')
    : t('usage_stats.api_key_settings_expand');

  const card = (
    <Card
      title={t('usage_stats.api_key_settings_title')}
      subtitle={t('usage_stats.api_key_settings_subtitle')}
      titleMeta={
        <div className={styles.apiKeySettingsHeaderActions}>
          {onCreateKey && (
            <Button
              type="button"
              variant="primary"
              size="sm"
              appearance="action"
              className={styles.apiKeyCreateButton}
              onClick={() => setCreateOpen(true)}
            >
              {t('usage_stats.api_key_settings_create')}
            </Button>
          )}
          {onSavePolicies && (
            <Button
              type="button"
              variant="secondary"
              size="sm"
              appearance="action"
              onClick={() => setQuotaDialog('settings')}
              disabled={apiKeys.length === 0}
              data-api-key-quota-settings-open
            >
              {t('usage_stats.api_key_quota_settings_button')}
            </Button>
          )}
          {onResetQuotas && (
            <Button
              type="button"
              variant="secondary"
              size="sm"
              appearance="action"
              onClick={() => setQuotaDialog('reset')}
              disabled={apiKeys.length === 0}
              data-api-key-quota-reset-open
            >
              {t('usage_stats.api_key_quota_reset_button')}
            </Button>
          )}
          <ApiKeySortMenu value={sortKey} onChange={changeSort} />
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className={`${styles.apiKeyVisibilityToggle} ${showFullApiKeys ? styles.apiKeyVisibilityToggleActive : ''}`.trim()}
            onClick={() => setShowFullApiKeys((current) => !current)}
            aria-label={toggleLabel}
            aria-pressed={showFullApiKeys}
            title={toggleLabel}
          >
            {showFullApiKeys ? <IconEye size={16} /> : <IconEyeOff size={16} />}
          </Button>
        </div>
      }
      extra={
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className={`${styles.apiKeyVisibilityToggle} ${expanded ? styles.apiKeyVisibilityToggleActive : ''}`.trim()}
          onClick={() => setExpanded((current) => !current)}
          aria-label={expandLabel}
          aria-pressed={expanded}
          title={expandLabel}
        >
          {expanded ? <IconMinimize2 size={15} /> : <IconMaximize2 size={15} />}
        </Button>
      }
      className={`${styles.detailsFixedCard} ${styles.apiKeySettingsCard} ${expanded ? styles.apiKeySettingsCardExpanded : ''}`.trim()}
      role={expanded ? 'dialog' : undefined}
      aria-modal={expanded ? true : undefined}
      aria-label={expanded ? t('usage_stats.api_key_settings_title') : undefined}
    >
      <div ref={apiKeySettingsBodyRef} className={styles.apiKeySettingsBody}>
        {loading && apiKeys.length === 0 ? (
          <div className={styles.hint}>{t('common.loading')}</div>
        ) : apiKeys.length === 0 ? (
          <div className={styles.hint}>{t('usage_stats.api_key_settings_empty')}</div>
        ) : (
          <div className={styles.apiKeySettingsList}>
            {sortedApiKeys.map((item) => {
              const draftAlias = draftAliases[item.id] ?? '';
              const disabled = savingId === item.id;
              const rowBusy = disabled || pendingId === item.id;
              const apiKey = getApiKeySettingsVisibleKey(item, showFullApiKeys);
              const copyLabel = copiedId === item.id ? t('usage_stats.api_key_settings_copied') : t('usage_stats.api_key_settings_copy');
              const enforcementState = item.policy?.enforcementState;
              // 非 active（含 policy 摘要不可用）时：按钮切到“恢复”，且禁止重新生成。
              const inactive = enforcementState !== 'active';
              const stateBadge = enforcementState === 'disabled_by_quota'
                ? t('usage_stats.api_key_settings_disabled_by_quota')
                : enforcementState === 'disabled_manual'
                  ? t('usage_stats.api_key_settings_disabled_manual')
                  : '';
              // 紧凑限额进度条：无限额摘要（tightest 缺失）时不渲染；超限切红色轨道。
              const quota = resolveQuotaProgress(item.policy);
              const quotaPercent = quota === null ? 0 : Math.round(quota.ratio * 100);
              return (
                <div key={item.id} className={styles.apiKeySettingsItem} data-api-key-settings-item={item.id}>
                  <div className={styles.apiKeySettingsSummary}>
                    <span className={styles.apiKeyFieldLabel}>{t('usage_stats.api_key_settings_display_key')}</span>
                    <div className={styles.apiKeySettingsNameRow}>
                      <span className={styles.apiKeySettingsName} title={apiKey}>{apiKey}</span>
                      <button
                        type="button"
                        className={`${styles.apiKeySettingsCopyIconButton} ${copiedId === item.id ? styles.apiKeySettingsCopyIconButtonCopied : ''}`.trim()}
                        onClick={() => void handleCopyApiKey(item)}
                        disabled={!item.apiKey}
                        aria-label={copyLabel}
                        title={copyLabel}
                      >
                        {copiedId === item.id ? <IconCheck size={14} /> : <IconCopy size={14} />}
                      </button>
                    </div>
                    {stateBadge && (
                      <span
                        className={`${styles.apiKeyBadge} ${enforcementState === 'disabled_by_quota' ? styles.apiKeyBadgeBreached : ''}`.trim()}
                      >
                        {stateBadge}
                      </span>
                    )}
                    {quota && (
                      <div
                        className={`${styles.apiKeyQuotaBar} ${quota.breached ? styles.apiKeyQuotaBarBreached : ''}`.trim()}
                        role="progressbar"
                        aria-valuemin={0}
                        aria-valuemax={100}
                        aria-valuenow={quotaPercent}
                        aria-label={t('usage_stats.api_key_policy_progress_label')}
                        title={quota.label}
                      >
                        <div className={styles.apiKeyQuotaBarFill} style={{ width: `${quotaPercent}%` }} />
                      </div>
                    )}
                    {/* 逐条列出已配置的日 / 周 / 月限额；未配置的窗口不显示。 */}
                    {item.policy?.limits && item.policy.limits.length > 0 && (
                      <ul className={styles.apiKeyQuotaWindows} data-api-key-quota-windows>
                        {item.policy.limits.map((limit) => {
                          const breached = limit.ratio >= 1;
                          return (
                            <li
                              key={`${limit.type}-${limit.window}`}
                              className={`${styles.apiKeyQuotaWindow} ${breached ? styles.apiKeyQuotaWindowBreached : ''}`.trim()}
                            >
                              <span className={styles.apiKeyQuotaWindowLabel}>{t(`usage_stats.api_key_policy_window_${limit.window}`)}</span>
                              <span className={styles.apiKeyQuotaWindowValue}>
                                {`${formatQuotaUsd(limit.used)} / ${formatQuotaUsd(limit.value)}`}
                              </span>
                              <span className={styles.apiKeyQuotaWindowPercent}>{`${Math.round(limit.ratio * 100)}%`}</span>
                            </li>
                          );
                        })}
                      </ul>
                    )}
                  </div>
                  <div className={styles.apiKeySettingsForm}>
                    <label className={styles.apiKeyAliasField}>
                      <span className={styles.apiKeyAliasLabel}>{t('usage_stats.api_key_settings_alias')}</span>
                      <Input
                        value={draftAlias}
                        onChange={(event) => setDraftAliases((current) => ({ ...current, [item.id]: event.target.value }))}
                        placeholder={apiKey}
                        aria-label={`${t('usage_stats.api_key_settings_alias')} ${apiKey}`}
                        className={`${styles.usagePillControl} ${styles.apiKeyAliasInput}`.trim()}
                        disabled={disabled}
                      />
                    </label>
                    <div className={styles.apiKeySettingsActions}>
                      <Button
                        variant="primary"
                        size="sm"
                        appearance="action"
                        className={styles.apiKeySettingsSaveButton}
                        onClick={() => onSaveAlias(item.id, draftAlias)}
                        disabled={rowBusy}
                      >
                        {disabled ? t('usage_stats.api_key_settings_saving') : t('common.save')}
                      </Button>
                      {onRegenerateKey && (
                        <Button
                          variant="secondary"
                          size="sm"
                          appearance="action"
                          onClick={() => setConfirmAction({ kind: 'regenerate', id: item.id })}
                          disabled={rowBusy || inactive}
                          title={inactive ? t('usage_stats.api_key_settings_regenerate_blocked') : t('usage_stats.api_key_settings_regenerate')}
                        >
                          {t('usage_stats.api_key_settings_regenerate')}
                        </Button>
                      )}
                      {(onDisableKey || onRestoreKey) && (
                        <Button
                          variant="secondary"
                          size="sm"
                          appearance="action"
                          onClick={() => void handleToggleEnforcement(item.id, inactive)}
                          disabled={rowBusy}
                        >
                          {inactive ? t('usage_stats.api_key_settings_restore') : t('usage_stats.api_key_settings_disable')}
                        </Button>
                      )}
                      {onDeleteKey && (
                        <Button
                          variant="danger"
                          size="sm"
                          appearance="action"
                          onClick={() => setConfirmAction({ kind: 'delete', id: item.id })}
                          disabled={rowBusy}
                        >
                          {t('usage_stats.api_key_settings_delete')}
                        </Button>
                      )}
                    </div>
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </div>

      {quotaDialog === 'settings' && onSavePolicies && (
        <ApiKeyQuotaSettingsModal apiKeys={sortedApiKeys} onClose={() => setQuotaDialog(null)} onSubmit={onSavePolicies} />
      )}
      {quotaDialog === 'reset' && onResetQuotas && (
        <ApiKeyQuotaResetModal apiKeys={sortedApiKeys} onClose={() => setQuotaDialog(null)} onSubmit={onResetQuotas} />
      )}

      <Modal
        open={createOpen}
        title={t('usage_stats.api_key_settings_create')}
        onClose={() => {
          setCreateOpen(false);
          setCreateAlias('');
          setCreateCustomKey('');
        }}
        closeDisabled={createPending}
      >
        <div className={styles.apiKeyCreateForm}>
          <Input
            label={t('usage_stats.api_key_settings_create_alias_label')}
            value={createAlias}
            onChange={(event) => setCreateAlias(event.target.value)}
            maxLength={128}
            disabled={createPending}
          />
          <Input
            label={t('usage_stats.api_key_settings_create_custom_label')}
            value={createCustomKey}
            onChange={(event) => setCreateCustomKey(event.target.value)}
            placeholder="sk-..."
            disabled={createPending}
          />
          <Button
            type="button"
            fullWidth
            loading={createPending}
            onClick={() => void handleCreateKey()}
          >
            {t('usage_stats.api_key_settings_create_submit')}
          </Button>
        </div>
      </Modal>

      <Modal
        open={reveal.phase === 'revealed'}
        title={t('usage_stats.api_key_settings_reveal_title')}
        onClose={() => setReveal({ phase: 'idle' })}
      >
        <div className={styles.apiKeyRevealStack}>
          <p className={styles.apiKeyRevealWarning}>{t('usage_stats.api_key_settings_reveal_warning')}</p>
          {reveal.phase === 'revealed' && (
            <div className={styles.apiKeyRevealKeyRow}>
              <code className={styles.apiKeyRevealKey}>{reveal.key}</code>
              <Button
                type="button"
                variant="secondary"
                size="sm"
                onClick={() => void handleCopyRevealKey()}
              >
                {t('usage_stats.api_key_settings_copy')}
              </Button>
            </div>
          )}
          <Button type="button" fullWidth onClick={() => setReveal({ phase: 'idle' })}>
            {t('usage_stats.api_key_settings_reveal_done')}
          </Button>
        </div>
      </Modal>

      <Modal
        open={confirmAction !== null}
        title={confirmAction?.kind === 'delete'
          ? t('usage_stats.api_key_settings_delete')
          : t('usage_stats.api_key_settings_regenerate')}
        onClose={closeConfirm}
        closeDisabled={confirmPending}
      >
        <div className={styles.apiKeyConfirmStack}>
          <p>{confirmAction?.kind === 'delete'
            ? t('usage_stats.api_key_settings_delete_confirm')
            : t('usage_stats.api_key_settings_regenerate_confirm')}</p>
          {confirmAction?.kind === 'delete' && (
            <Button
              type="button"
              fullWidth
              variant="danger"
              loading={confirmPending}
              onClick={() => void handleDeleteKey(confirmAction.id)}
            >
              {t('usage_stats.api_key_settings_delete')}
            </Button>
          )}
          {confirmAction?.kind === 'regenerate' && (
            <Button
              type="button"
              fullWidth
              loading={confirmPending}
              onClick={() => void handleRegenerateKey(confirmAction.id)}
            >
              {t('usage_stats.api_key_settings_regenerate')}
            </Button>
          )}
          <Button type="button" fullWidth variant="ghost" onClick={closeConfirm} disabled={confirmPending}>
            {t('common.cancel')}
          </Button>
        </div>
      </Modal>
    </Card>
  );

  if (!expanded || typeof document === 'undefined') {
    return card;
  }
  return createPortal(
    <div
      className={styles.apiKeySettingsExpandedOverlay}
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) {
          setExpanded(false);
        }
      }}
    >
      {card}
    </div>,
    document.body,
  );
}
