import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Card } from '@/components/ui/Card';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { Modal } from '@/components/ui/Modal';
import { IconCheck, IconCopy, IconEye, IconEyeOff } from '@/components/ui/icons';
import { useScrollBoundaryContainment } from '@/hooks/useScrollBoundaryContainment';
import { resolveQuotaProgress } from '@/lib/quotaProgress';
import type { CreatedApiKey, CpaApiKeySettingsItem } from '@/lib/types';
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
  onOpenPolicy?: (id: string) => void;
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
  onOpenPolicy,
}: ApiKeySettingsCardProps) {
  const { t } = useTranslation();
  const [showFullApiKeys, setShowFullApiKeys] = useState(false);
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

  const handleToggleKey = useCallback(async (item: CpaApiKeySettingsItem) => {
    const inactive = item.policy?.enforcementState !== 'active';
    const action = inactive ? onRestoreKey : onDisableKey;
    if (!action) {
      return;
    }
    await runRowAction(item.id, () => action(item.id));
  }, [onDisableKey, onRestoreKey, runRowAction]);

  const confirmPending = confirmAction !== null && pendingId === confirmAction.id;
  const toggleLabel = showFullApiKeys
    ? t('usage_stats.api_key_settings_hide_full')
    : t('usage_stats.api_key_settings_show_full');

  return (
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
      className={`${styles.detailsFixedCard} ${styles.apiKeySettingsCard}`}
    >
      <div ref={apiKeySettingsBodyRef} className={styles.apiKeySettingsBody}>
        {loading && apiKeys.length === 0 ? (
          <div className={styles.hint}>{t('common.loading')}</div>
        ) : apiKeys.length === 0 ? (
          <div className={styles.hint}>{t('usage_stats.api_key_settings_empty')}</div>
        ) : (
          <div className={styles.apiKeySettingsList}>
            {apiKeys.map((item) => {
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
                <div key={item.id} className={styles.apiKeySettingsItem}>
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
                          onClick={() => void handleToggleKey(item)}
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
                      {onOpenPolicy && (
                        <Button
                          variant="ghost"
                          size="sm"
                          appearance="action"
                          onClick={() => onOpenPolicy(item.id)}
                          disabled={rowBusy}
                        >
                          {t('usage_stats.api_key_settings_quota')}
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
}
