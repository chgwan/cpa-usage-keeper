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
import type {
  ApiKeyLimitWindow,
  ApiKeyModelAccessState,
  ApiKeyModelAccessUpdateRequest,
  CreatedApiKey,
  CpaApiKeySettingsItem,
} from '@/lib/types';
import {
  ApiKeyModelAccessModal,
  formatModelAccessTooltip,
  hasModelAccessRules,
} from './ApiKeyModelAccessDialogs';
import {
  ApiKeyQuotaResetModal,
  ApiKeyQuotaSettingsModal,
  formatQuotaAdjustmentAmount,
  type QuotaAdjustmentRequest,
  type QuotaAdjustmentResult,
  type QuotaPolicyUpdateRequest,
  type QuotaResetRequest,
} from './ApiKeyQuotaDialogs';
import { ApiKeyManageDialog } from './ApiKeyManageDialog';
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

export type ApiKeySortKey = 'name' | 'status' | 'quota_ratio' | 'quota_spend' | 'quota_limit';

export type ApiKeySortOrder = 'sequential' | 'reverse';

export interface ApiKeySortPreference {
  key: ApiKeySortKey;
  order: ApiKeySortOrder;
}

export const API_KEY_SORT_KEYS: readonly ApiKeySortKey[] = ['name', 'status', 'quota_ratio', 'quota_spend', 'quota_limit'];

export const DEFAULT_API_KEY_SORT: ApiKeySortKey = 'name';

export const DEFAULT_API_KEY_SORT_ORDER: ApiKeySortOrder = 'sequential';

export const DEFAULT_API_KEY_SORT_PREFERENCE: ApiKeySortPreference = {
  key: DEFAULT_API_KEY_SORT,
  order: DEFAULT_API_KEY_SORT_ORDER,
};

const API_KEY_SETTINGS_SORT_V3_STORAGE_KEY = 'cpa-usage-keeper-api-key-settings-sort-v3';
// v2 只存单一排序键（含 name_asc / name_desc）；首次读取时迁移到 v3 的 {key, order}。
const API_KEY_SETTINGS_SORT_V2_STORAGE_KEY = 'cpa-usage-keeper-api-key-settings-sort-v2';
// 更早期存的是 {alphabetical, status} 两个开关；首次读取时迁移为排序字段与方向。
const LEGACY_API_KEY_SETTINGS_SORT_STORAGE_KEY = 'cpa-usage-keeper-api-key-settings-sort';

const isApiKeySortKey = (value: unknown): value is ApiKeySortKey => (
  typeof value === 'string' && (API_KEY_SORT_KEYS as readonly string[]).includes(value)
);

const isApiKeySortOrder = (value: unknown): value is ApiKeySortOrder => (
  value === 'sequential' || value === 'reverse'
);

export function resolveStoredApiKeySort(
  v3Raw: string | null,
  v2Raw: string | null,
  legacyJsonRaw: string | null,
): ApiKeySortPreference {
  if (v3Raw) {
    try {
      const parsed = JSON.parse(v3Raw) as Record<string, unknown> | null;
      if (parsed && !Array.isArray(parsed) && isApiKeySortKey(parsed.key) && isApiKeySortOrder(parsed.order)) {
        return { key: parsed.key, order: parsed.order };
      }
    } catch {
      // 损坏的 v3 值跳过，继续尝试从旧版本恢复。
    }
  }
  if (v2Raw === 'name_asc') {
    return { key: 'name', order: 'sequential' };
  }
  if (v2Raw === 'name_desc') {
    return { key: 'name', order: 'reverse' };
  }
  if (isApiKeySortKey(v2Raw)) {
    return { key: v2Raw, order: 'sequential' };
  }
  if (!legacyJsonRaw) {
    return DEFAULT_API_KEY_SORT_PREFERENCE;
  }
  try {
    const legacy = JSON.parse(legacyJsonRaw) as Record<string, unknown> | null;
    if (legacy?.status === 'asc' || legacy?.status === 'desc') {
      return { key: 'status', order: 'sequential' };
    }
    return legacy?.alphabetical === 'desc'
      ? { key: 'name', order: 'reverse' }
      : DEFAULT_API_KEY_SORT_PREFERENCE;
  } catch {
    return DEFAULT_API_KEY_SORT_PREFERENCE;
  }
}

const getInitialSort = (): ApiKeySortPreference => {
  try {
    if (typeof window === 'undefined') {
      return DEFAULT_API_KEY_SORT_PREFERENCE;
    }
    return resolveStoredApiKeySort(
      window.localStorage.getItem(API_KEY_SETTINGS_SORT_V3_STORAGE_KEY),
      window.localStorage.getItem(API_KEY_SETTINGS_SORT_V2_STORAGE_KEY),
      window.localStorage.getItem(LEGACY_API_KEY_SETTINGS_SORT_STORAGE_KEY),
    );
  } catch {
    return DEFAULT_API_KEY_SORT_PREFERENCE;
  }
};

// zh 排序规则：英文按 A–Z，中文按拼音；不受浏览器语言影响。
const apiKeySortCollator = new Intl.Collator('zh', { numeric: true, sensitivity: 'base' });
const HAN_START = /^\p{Script=Han}/u;

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

// 中文开头的名称排在其他名称之后（zh 规则默认把中文排在前面）。
const apiKeyNameGroup = (label: string) => (HAN_START.test(label) ? 1 : 0);

const compareApiKeyNames = (left: CpaApiKeySettingsItem, right: CpaApiKeySettingsItem) => (
  apiKeyNameGroup(apiKeySortLabel(left)) - apiKeyNameGroup(apiKeySortLabel(right))
  || apiKeySortCollator.compare(apiKeySortLabel(left), apiKeySortLabel(right))
  || apiKeySortCollator.compare(left.displayKey, right.displayKey)
  || apiKeySortCollator.compare(left.id, right.id)
);

// 限额排序取最紧窗口（与行内进度条一致）；未配置限额的 key 顺序时排最后，倒序时排最前。
const apiKeyQuotaMetric = (sortKey: ApiKeySortKey) => {
  if (sortKey === 'quota_ratio') return (item: CpaApiKeySettingsItem) => item.policy?.tightest?.ratio;
  if (sortKey === 'quota_spend') return (item: CpaApiKeySettingsItem) => item.policy?.tightest?.used;
  // 设定限额取常规值（baseValue），临时调整不影响排序。
  if (sortKey === 'quota_limit') return (item: CpaApiKeySettingsItem) => {
    const tightest = item.policy?.tightest;
    return tightest ? tightest.baseValue ?? tightest.value : undefined;
  };
  return null;
};

// 每次只按一个键排序；倒序翻转主序（含未配置限额 / 未知状态置顶），同组仍按名称 A → Z 兜底。
export function sortApiKeySettingsItems(
  items: CpaApiKeySettingsItem[],
  sortKey: ApiKeySortKey,
  sortOrder: ApiKeySortOrder = DEFAULT_API_KEY_SORT_ORDER,
) {
  const direction = sortOrder === 'reverse' ? -1 : 1;
  const metric = apiKeyQuotaMetric(sortKey);
  return [...items].sort((left, right) => {
    if (sortKey === 'name') {
      return direction * compareApiKeyNames(left, right);
    }
    if (sortKey === 'status') {
      const byStatus = apiKeyStatusRank(left) - apiKeyStatusRank(right);
      if (byStatus !== 0) return direction * byStatus;
    }
    if (metric) {
      const leftValue = metric(left);
      const rightValue = metric(right);
      if (leftValue === undefined || rightValue === undefined) {
        if (leftValue !== rightValue) return direction * (leftValue === undefined ? 1 : -1);
      } else if (leftValue !== rightValue) {
        return direction * (rightValue - leftValue);
      }
    }
    return compareApiKeyNames(left, right);
  });
}

const formatQuotaUsd = (value: number) => `$${value.toFixed(4)}`;

// 卡片层不提供临时调整 handler 时的兜底：保持弹窗 props 必填，行为上等同什么都没做。
const noopAdjustQuotas = async (): Promise<QuotaAdjustmentResult | null> => null;
const noopClearQuotaAdjustments = async (): Promise<boolean> => false;

// 插件统计身份没有可展示或复制的完整 key，始终只显示脱敏值。
export function getApiKeySettingsVisibleKey(item: CpaApiKeySettingsItem, showFullApiKeys: boolean) {
  return item.sourceType !== 'plugin' && showFullApiKeys && item.apiKey ? item.apiKey : item.displayKey;
}

export async function copyApiKeyToClipboard(apiKey: string | undefined, context: CopyContext = {}) {
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

export interface ApiKeyRevealModalProps {
  apiKey: string | null;
  onClose: () => void;
  onNotice?: (kind: 'success' | 'info' | 'error', message: string) => void;
}

// 一次性 key 展示弹窗：创建流程与管理面板里的“重新生成”流程共用同一个组件，避免分叉两套复制逻辑。
export function ApiKeyRevealModal({ apiKey, onClose, onNotice }: ApiKeyRevealModalProps) {
  const { t } = useTranslation();
  const handleCopy = useCallback(async () => {
    if (!apiKey) return;
    try {
      await copyApiKeyToClipboard(apiKey);
      onNotice?.('success', t('usage_stats.api_key_settings_copy_success'));
    } catch {
      onNotice?.('error', t('usage_stats.api_key_settings_copy_failed'));
    }
  }, [apiKey, onNotice, t]);

  return (
    <Modal open={apiKey !== null} title={t('usage_stats.api_key_settings_reveal_title')} onClose={onClose}>
      <div className={styles.apiKeyRevealStack}>
        <p className={styles.apiKeyRevealWarning}>{t('usage_stats.api_key_settings_reveal_warning')}</p>
        {apiKey !== null && (
          <div className={styles.apiKeyRevealKeyRow}>
            <code className={styles.apiKeyRevealKey}>{apiKey}</code>
            <Button type="button" variant="secondary" size="sm" onClick={() => void handleCopy()}>
              {t('usage_stats.api_key_settings_copy')}
            </Button>
          </div>
        )}
        <Button type="button" fullWidth onClick={onClose}>
          {t('usage_stats.api_key_settings_reveal_done')}
        </Button>
      </div>
    </Modal>
  );
}

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
  /** 本周期内的临时限额调整：返回应用/跳过的明细。 */
  onAdjustQuotas?: (request: QuotaAdjustmentRequest) => Promise<QuotaAdjustmentResult | null>;
  /** 清除所选 key/周期当前生效的临时调整。 */
  onClearQuotaAdjustments?: (request: { apiKeyIds: string[]; windows: ApiKeyLimitWindow[] }) => Promise<boolean>;
  /** CPA key-model-access 插件状态与按 key 模型权限；插件未安装时 available 为 false。 */
  modelAccess?: ApiKeyModelAccessState | null;
  /** 批量或单 key 保存模型权限，返回是否成功。 */
  onSaveModelAccess?: (request: ApiKeyModelAccessUpdateRequest) => Promise<boolean>;
  /** 从 CPA 的 policy_file 重载模型权限策略。 */
  onReloadModelAccess?: () => Promise<boolean>;
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
  onAdjustQuotas,
  onClearQuotaAdjustments,
  modelAccess,
  onSaveModelAccess,
  onReloadModelAccess,
}: ApiKeySettingsCardProps) {
  const { t } = useTranslation();
  const [showFullApiKeys, setShowFullApiKeys] = useState(false);
  const [sortPreference, setSortPreference] = useState<ApiKeySortPreference>(getInitialSort);
  // 展开态把同一张卡片挂到 body 上的全屏遮罩里；状态都在本组件内，切换时草稿不丢。
  const [expanded, setExpanded] = useState(false);
  const sortedApiKeys = useMemo(
    () => sortApiKeySettingsItems(apiKeys, sortPreference.key, sortPreference.order),
    [apiKeys, sortPreference.key, sortPreference.order],
  );
  // 插件统计身份不在 CPA api-keys 里，生命周期与限额操作只面向原生 key。
  const manageableApiKeys = useMemo(() => sortedApiKeys.filter((item) => item.sourceType !== 'plugin'), [sortedApiKeys]);
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
  // 单 key 管理面板：记录当前打开的 key id，null 表示未打开。
  const [manageDialogId, setManageDialogId] = useState<string | null>(null);
  const manageDialogItem = useMemo(
    () => (manageDialogId ? sortedApiKeys.find((entry) => entry.id === manageDialogId) ?? null : null),
    [manageDialogId, sortedApiKeys],
  );
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
    if (item.sourceType === 'plugin') return;
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

  const [quotaDialog, setQuotaDialog] = useState<'settings' | 'reset' | null>(null);
  const [modelAccessOpen, setModelAccessOpen] = useState(false);
  // 单 key 管理面板打开时也算内层弹窗，卡片展开态下 Esc 只关管理面板，不连带收起卡片。
  // 用 manageDialogItem（而不是 manageDialogId）判断：列表刷新失败把 apiKeys 清空后，
  // id 还留着但对应的 item 已经找不到了，这时必须让 Esc 照常收起卡片，不能被一个
  // 已经指向空气的 id 卡住。
  const innerModalOpen = createOpen || reveal.phase === 'revealed' || quotaDialog !== null || modelAccessOpen || manageDialogItem !== null;

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

  const updateSortPreference = useCallback((updater: (current: ApiKeySortPreference) => ApiKeySortPreference) => {
    setSortPreference((current) => {
      const next = updater(current);
      try {
        window.localStorage.setItem(API_KEY_SETTINGS_SORT_V3_STORAGE_KEY, JSON.stringify(next));
      } catch {
        // 存储不可用（隐私模式等）时只在本次会话内生效。
      }
      return next;
    });
  }, []);
  const changeSortKey = useCallback((next: ApiKeySortKey) => {
    updateSortPreference((current) => ({ ...current, key: next }));
  }, [updateSortPreference]);
  const toggleSortOrder = useCallback(() => {
    updateSortPreference((current) => ({
      ...current,
      order: current.order === 'reverse' ? 'sequential' : 'reverse',
    }));
  }, [updateSortPreference]);
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
              disabled={manageableApiKeys.length === 0}
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
              disabled={manageableApiKeys.length === 0}
              data-api-key-quota-reset-open
            >
              {t('usage_stats.api_key_quota_reset_button')}
            </Button>
          )}
          {modelAccess?.available && onSaveModelAccess && (
            <Button
              type="button"
              variant="secondary"
              size="sm"
              appearance="action"
              onClick={() => setModelAccessOpen(true)}
              disabled={manageableApiKeys.length === 0}
              data-api-key-model-access-open
            >
              {t('usage_stats.api_key_model_access_button')}
            </Button>
          )}
          <ApiKeySortMenu
            value={sortPreference.key}
            order={sortPreference.order}
            onChange={changeSortKey}
            onOrderToggle={toggleSortOrder}
          />
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
              const plugin = item.sourceType === 'plugin';
              const apiKey = plugin
                ? t('usage_stats.api_key_settings_plugin_identity', { id: item.id })
                : getApiKeySettingsVisibleKey(item, showFullApiKeys);
              const copyLabel = copiedId === item.id ? t('usage_stats.api_key_settings_copied') : t('usage_stats.api_key_settings_copy');
              const enforcementState = item.policy?.enforcementState;
              const stateBadge = enforcementState === 'disabled_by_quota'
                ? t('usage_stats.api_key_settings_disabled_by_quota')
                : enforcementState === 'disabled_manual'
                  ? t('usage_stats.api_key_settings_disabled_manual')
                  : '';
              // 紧凑限额进度条：无限额摘要（tightest 缺失）时不渲染；超限切红色轨道。
              const quota = resolveQuotaProgress(item.policy);
              const quotaPercent = quota === null ? 0 : Math.round(quota.ratio * 100);
              const modelRule = !plugin && modelAccess?.available ? modelAccess.policies[item.id] : undefined;
              const modelRuleConfigured = hasModelAccessRules(modelRule);
              const modelRuleTooltip = modelRule && modelRuleConfigured
                ? formatModelAccessTooltip(
                  modelRule,
                  t('usage_stats.api_key_model_access_allow_title'),
                  t('usage_stats.api_key_model_access_deny_title'),
                )
                : '';
              return (
                <div key={item.id} className={styles.apiKeySettingsItem} data-api-key-settings-item={item.id}>
                  <div className={styles.apiKeySettingsSummary}>
                    <span className={styles.apiKeyFieldLabel}>{t('usage_stats.api_key_settings_display_key')}</span>
                    <div className={styles.apiKeySettingsNameRow}>
                      {plugin ? (
                        <span className={styles.apiKeySettingsName} title={apiKey}>{apiKey}</span>
                      ) : (
                        <button
                          type="button"
                          className={styles.apiKeySettingsManageOpen}
                          onClick={() => setManageDialogId(item.id)}
                          aria-haspopup="dialog"
                          aria-label={t('usage_stats.api_key_settings_manage_aria', { key: apiKey })}
                          data-api-key-manage-open={item.id}
                        >
                          <span className={styles.apiKeySettingsName} title={apiKey}>{apiKey}</span>
                        </button>
                      )}
                      {!plugin && <button
                        type="button"
                        className={`${styles.apiKeySettingsCopyIconButton} ${copiedId === item.id ? styles.apiKeySettingsCopyIconButtonCopied : ''}`.trim()}
                        onClick={() => void handleCopyApiKey(item)}
                        disabled={!item.apiKey}
                        aria-label={copyLabel}
                        title={copyLabel}
                      >
                        {copiedId === item.id ? <IconCheck size={14} /> : <IconCopy size={14} />}
                      </button>}
                    </div>
                    {(plugin || stateBadge || modelRuleConfigured) && (
                      <div className={styles.apiKeyBadgesRow}>
                        {plugin && (
                          <span className={styles.apiKeyBadge} data-api-key-plugin-source={item.id}>
                            {`${t('usage_stats.api_key_settings_plugin')} · ${t('usage_stats.api_key_settings_source')}: ${item.accessProvider.replace(/^plugin:/, '')}`}
                          </span>
                        )}
                        {stateBadge && (
                          <span
                            className={`${styles.apiKeyBadge} ${enforcementState === 'disabled_by_quota' ? styles.apiKeyBadgeBreached : ''}`.trim()}
                          >
                            {stateBadge}
                          </span>
                        )}
                        {modelRule && modelRuleConfigured && (
                          <span
                            className={`${styles.apiKeyBadge} ${styles.apiKeyModelAccessBadge}`.trim()}
                            title={modelRuleTooltip}
                            data-api-key-model-access-badge={item.id}
                          >
                            {[
                              modelRule.allowModels.length > 0
                                ? t('usage_stats.api_key_model_access_badge_allow', { count: modelRule.allowModels.length })
                                : '',
                              modelRule.denyModels.length > 0
                                ? t('usage_stats.api_key_model_access_badge_deny', { count: modelRule.denyModels.length })
                                : '',
                            ].filter(Boolean).join(' · ')}
                          </span>
                        )}
                      </div>
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
                          // adjustmentUsd 缺失表示该周期没有生效的临时调整，不渲染徽章。
                          const hasAdjustment = limit.adjustmentUsd !== undefined;
                          const baseValue = limit.baseValue ?? limit.value;
                          return (
                            <li
                              key={`${limit.type}-${limit.window}`}
                              className={`${styles.apiKeyQuotaWindow} ${breached ? styles.apiKeyQuotaWindowBreached : ''}`.trim()}
                            >
                              <span className={styles.apiKeyQuotaWindowLabel}>{t(`usage_stats.api_key_policy_window_${limit.window}`)}</span>
                              <span className={styles.apiKeyQuotaWindowValue}>
                                {`${formatQuotaUsd(limit.used)} / ${formatQuotaUsd(limit.value)}`}
                                {hasAdjustment && (
                                  <span
                                    className={styles.apiKeyQuotaWindowAdjustment}
                                    title={t('usage_stats.api_key_quota_adjustment_title', { value: formatQuotaUsd(baseValue) })}
                                  >
                                    {' · '}
                                    {t('usage_stats.api_key_quota_adjustment_badge', { amount: formatQuotaAdjustmentAmount(limit.adjustmentUsd as number) })}
                                  </span>
                                )}
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
                        // 页面层已负责错误提示；这里只吞掉 rejection，避免未处理的 Promise 错误。
                        onClick={() => void Promise.resolve(onSaveAlias(item.id, draftAlias)).catch(() => undefined)}
                        disabled={disabled}
                      >
                        {disabled ? t('usage_stats.api_key_settings_saving') : t('common.save')}
                      </Button>
                    </div>
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </div>

      {quotaDialog === 'settings' && onSavePolicies && (
        <ApiKeyQuotaSettingsModal
          apiKeys={manageableApiKeys}
          onClose={() => setQuotaDialog(null)}
          onSubmit={onSavePolicies}
          onAdjust={onAdjustQuotas ?? noopAdjustQuotas}
          onClearAdjustments={onClearQuotaAdjustments ?? noopClearQuotaAdjustments}
        />
      )}
      {quotaDialog === 'reset' && onResetQuotas && (
        <ApiKeyQuotaResetModal apiKeys={manageableApiKeys} onClose={() => setQuotaDialog(null)} onSubmit={onResetQuotas} />
      )}
      {modelAccessOpen && modelAccess?.available && onSaveModelAccess && (
        <ApiKeyModelAccessModal
          apiKeys={manageableApiKeys}
          modelAccess={modelAccess}
          onClose={() => setModelAccessOpen(false)}
          onSubmit={onSaveModelAccess}
          onReload={onReloadModelAccess}
        />
      )}

      {manageDialogItem && (
        <ApiKeyManageDialog
          item={manageDialogItem}
          onClose={() => setManageDialogId(null)}
          onNotice={onNotice}
          onRegenerateKey={onRegenerateKey}
          // 重新生成的一次性 key 交给卡片级 reveal 状态展示：面板可能因为父级刷新 apiKeys=[] 而被整体卸载，
          // reveal 状态放在卡片层才能在面板消失后依然显示这个 key。
          onRegenerated={(created) => setReveal((current) => nextRevealState(current, created))}
          onDeleteKey={onDeleteKey}
          onDisableKey={onDisableKey}
          onRestoreKey={onRestoreKey}
          onSavePolicies={onSavePolicies}
          onResetQuotas={onResetQuotas}
          onAdjustQuotas={onAdjustQuotas}
          onClearQuotaAdjustments={onClearQuotaAdjustments}
          modelAccess={modelAccess}
          onSaveModelAccess={onSaveModelAccess}
          onReloadModelAccess={onReloadModelAccess}
        />
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

      <ApiKeyRevealModal
        apiKey={reveal.phase === 'revealed' ? reveal.key : null}
        onClose={() => setReveal({ phase: 'idle' })}
        onNotice={onNotice}
      />
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
