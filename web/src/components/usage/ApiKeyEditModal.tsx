import { useCallback, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Modal } from '@/components/ui/Modal';
import type { CpaApiKeySettingsItem } from '@/lib/types';
import styles from '@/pages/UsagePage.module.scss';

// 编辑弹窗内联二次确认状态机：同一时刻最多一个动作待确认，执行或取消后回到 idle。
export type ApiKeyEditConfirmStep = 'idle' | 'regenerate' | 'delete';

export type ApiKeyEditConfirmEvent =
  | { type: 'request'; kind: 'regenerate' | 'delete' }
  | { type: 'cancel' }
  | { type: 'settled' };

export function nextApiKeyEditConfirm(event: ApiKeyEditConfirmEvent, busy = false): ApiKeyEditConfirmStep {
  // 动作执行中不接受新的确认请求，避免忙态期间切换确认目标。
  if (event.type === 'request') {
    return busy ? 'idle' : event.kind;
  }
  // cancel / settled 一律回到 idle。
  return 'idle';
}

// 编辑弹窗头部标签：优先别名，其次后端 label，最后回退掩码 key（与配额弹窗解析一致）。
export function resolveApiKeyEditLabel(
  item: Pick<CpaApiKeySettingsItem, 'keyAlias' | 'label' | 'displayKey'>,
): string {
  return item.keyAlias?.trim() || item.label || item.displayKey || '';
}

export interface ApiKeyEditModalProps {
  item: CpaApiKeySettingsItem;
  /** 行级动作忙态（卡片 pendingId）：忙碌时禁用全部动作并阻止关闭。 */
  busy?: boolean;
  onClose: () => void;
  onRegenerateKey?: (id: string) => void;
  onDeleteKey?: (id: string) => void;
  /** restore 为 true 表示当前非 active，走恢复；否则走禁用。 */
  onToggleKey?: (id: string, restore: boolean) => void;
  onOpenPolicy?: (id: string) => void;
}

export function ApiKeyEditModal({
  item,
  busy = false,
  onClose,
  onRegenerateKey,
  onDeleteKey,
  onToggleKey,
  onOpenPolicy,
}: ApiKeyEditModalProps) {
  const { t } = useTranslation();
  const [confirmStep, setConfirmStep] = useState<ApiKeyEditConfirmStep>('idle');

  const requestConfirm = useCallback((kind: 'regenerate' | 'delete') => {
    setConfirmStep(nextApiKeyEditConfirm({ type: 'request', kind }));
  }, []);

  const cancelConfirm = useCallback(() => {
    setConfirmStep(nextApiKeyEditConfirm({ type: 'cancel' }));
  }, []);

  // 动作交给卡片执行（忙态与 reveal 都归卡片管），发起后立即回到 idle 展示。
  const settleConfirm = useCallback(() => {
    setConfirmStep(nextApiKeyEditConfirm({ type: 'settled' }));
  }, []);

  const label = resolveApiKeyEditLabel(item);
  // 非 active（含 policy 摘要不可用）时：按钮切到“恢复”，且禁止重新生成。
  const enforcementState = item.policy?.enforcementState;
  const inactive = enforcementState !== 'active';
  const stateBadge = enforcementState === 'disabled_by_quota'
    ? t('usage_stats.api_key_settings_disabled_by_quota')
    : enforcementState === 'disabled_manual'
      ? t('usage_stats.api_key_settings_disabled_manual')
      : '';

  return (
    <Modal open title={t('usage_stats.api_key_edit_title')} onClose={onClose} closeDisabled={busy}>
      <div className={styles.apiKeyEditStack}>
        <div className={styles.apiKeyEditHeader}>
          <span className={styles.apiKeyPolicyKeyLabel} title={label}>{label}</span>
          {stateBadge && (
            <span
              className={`${styles.apiKeyBadge} ${enforcementState === 'disabled_by_quota' ? styles.apiKeyBadgeBreached : ''}`.trim()}
            >
              {stateBadge}
            </span>
          )}
        </div>

        {onRegenerateKey && (confirmStep === 'regenerate' ? (
          <div className={styles.apiKeyEditConfirmBlock}>
            <p className={styles.apiKeyEditConfirmText}>{t('usage_stats.api_key_settings_regenerate_confirm')}</p>
            <div className={styles.apiKeyEditConfirmRow}>
              <Button
                type="button"
                size="sm"
                loading={busy}
                onClick={() => {
                  onRegenerateKey(item.id);
                  settleConfirm();
                }}
              >
                {t('usage_stats.api_key_settings_regenerate')}
              </Button>
              <Button type="button" size="sm" variant="ghost" disabled={busy} onClick={cancelConfirm}>
                {t('common.cancel')}
              </Button>
            </div>
          </div>
        ) : (
          <Button
            type="button"
            fullWidth
            variant="secondary"
            disabled={busy || inactive}
            title={inactive ? t('usage_stats.api_key_settings_regenerate_blocked') : undefined}
            onClick={() => requestConfirm('regenerate')}
          >
            {t('usage_stats.api_key_settings_regenerate')}
          </Button>
        ))}

        {onOpenPolicy && (
          <Button
            type="button"
            fullWidth
            variant="secondary"
            disabled={busy}
            onClick={() => onOpenPolicy(item.id)}
          >
            {t('usage_stats.api_key_settings_quota')}
          </Button>
        )}

        {onToggleKey && (
          <Button
            type="button"
            fullWidth
            variant="secondary"
            disabled={busy}
            onClick={() => onToggleKey(item.id, inactive)}
          >
            {inactive ? t('usage_stats.api_key_settings_restore') : t('usage_stats.api_key_settings_disable')}
          </Button>
        )}

        {onDeleteKey && (confirmStep === 'delete' ? (
          <div className={styles.apiKeyEditConfirmBlock}>
            <p className={styles.apiKeyEditConfirmText}>{t('usage_stats.api_key_settings_delete_confirm')}</p>
            <div className={styles.apiKeyEditConfirmRow}>
              <Button
                type="button"
                size="sm"
                variant="danger"
                loading={busy}
                onClick={() => {
                  onDeleteKey(item.id);
                  settleConfirm();
                }}
              >
                {t('usage_stats.api_key_settings_delete')}
              </Button>
              <Button type="button" size="sm" variant="ghost" disabled={busy} onClick={cancelConfirm}>
                {t('common.cancel')}
              </Button>
            </div>
          </div>
        ) : (
          <Button
            type="button"
            fullWidth
            variant="danger"
            disabled={busy}
            onClick={() => requestConfirm('delete')}
          >
            {t('usage_stats.api_key_settings_delete')}
          </Button>
        ))}

        <Button type="button" fullWidth variant="ghost" onClick={onClose} disabled={busy}>
          {t('common.close')}
        </Button>
      </div>
    </Modal>
  );
}
