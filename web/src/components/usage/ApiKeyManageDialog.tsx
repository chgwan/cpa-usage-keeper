import { useCallback, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Modal } from '@/components/ui/Modal';
import type { ApiKeyLimitWindow, CreatedApiKey, CpaApiKeySettingsItem } from '@/lib/types';
import {
  ApiKeyQuotaSection,
  PeriodPicker,
  buildSingleKeyQuotaResetRequest,
  type QuotaAdjustmentRequest,
  type QuotaAdjustmentResult,
  type QuotaPolicyUpdateRequest,
  type QuotaResetRequest,
} from './ApiKeyQuotaDialogs';
import styles from '@/pages/UsagePage.module.scss';

// 单 key 面板不提供临时调整 / 重置 handler 时的兜底：保持子组件 props 必填，行为上等同什么都没做。
const noopAdjustQuotas = async (): Promise<QuotaAdjustmentResult | null> => null;
const noopClearQuotaAdjustments = async (): Promise<boolean> => false;

const toggleWindow = (current: ReadonlySet<ApiKeyLimitWindow>, window: ApiKeyLimitWindow) => {
  const next = new Set(current);
  if (next.has(window)) next.delete(window);
  else next.add(window);
  return next;
};

interface ResetQuotaSectionProps {
  item: CpaApiKeySettingsItem;
  onResetQuotas: (request: QuotaResetRequest) => Promise<boolean>;
}

// 面板内嵌的重置区块：只对这一个 key 生效，复用批量弹窗同款的周期选择器与请求构造函数。
function ResetQuotaSection({ item, onResetQuotas }: ResetQuotaSectionProps) {
  const { t } = useTranslation();
  const [windows, setWindows] = useState<Set<ApiKeyLimitWindow>>(() => new Set<ApiKeyLimitWindow>(['weekly']));
  const [error, setError] = useState<'periods' | null>(null);
  const [pending, setPending] = useState(false);

  const handleSubmit = useCallback(async () => {
    const built = buildSingleKeyQuotaResetRequest(item.id, windows);
    setError(built.error);
    if (!built.request) return;
    setPending(true);
    try {
      await onResetQuotas(built.request);
    } finally {
      setPending(false);
    }
  }, [item.id, onResetQuotas, windows]);

  return (
    <div data-api-key-manage-reset>
      <p className={styles.apiKeyPolicyUsageHint}>{t('usage_stats.api_key_quota_reset_hint')}</p>
      <PeriodPicker
        selected={windows}
        onToggle={(window) => { setWindows((current) => toggleWindow(current, window)); setError(null); }}
        disabled={pending}
      />
      {error === 'periods' && <p className={styles.apiKeyPolicyError}>{t('usage_stats.api_key_quota_select_periods_required')}</p>}
      <div className={styles.apiKeyManageActions}>
        <Button type="button" variant="danger" loading={pending} onClick={() => void handleSubmit()} data-api-key-quota-reset-submit>
          {t('usage_stats.api_key_quota_reset_submit')}
        </Button>
      </div>
    </div>
  );
}

export interface ApiKeyManageDialogProps {
  item: CpaApiKeySettingsItem;
  onClose: () => void;
  onNotice?: (kind: 'success' | 'info' | 'error', message: string) => void;
  onRegenerateKey?: (id: string) => Promise<CreatedApiKey | null>;
  // 重新生成成功后把一次性 key 交给父级卡片的 reveal 状态展示：面板本身可能因为父级列表刷新而被整体卸载，
  // 自己存 state 会导致这个一次性 key 永久丢失。
  onRegenerated: (created: CreatedApiKey) => void;
  onDeleteKey?: (id: string) => Promise<boolean>;
  onDisableKey?: (id: string) => Promise<boolean>;
  onRestoreKey?: (id: string) => Promise<boolean>;
  onSavePolicies?: (request: QuotaPolicyUpdateRequest) => Promise<boolean>;
  onResetQuotas?: (request: QuotaResetRequest) => Promise<boolean>;
  onAdjustQuotas?: (request: QuotaAdjustmentRequest) => Promise<QuotaAdjustmentResult | null>;
  onClearQuotaAdjustments?: (request: { apiKeyIds: string[]; windows: ApiKeyLimitWindow[] }) => Promise<boolean>;
}

// 单 key 管理面板：把原来分散在行内的限额 / 重置 / 重新生成 / 启停 / 删除全部收进一个弹窗，
// 行内只留别名与保存。配额与执行审计区块直接复用 ApiKeyQuotaSection（它内部已经渲染了
// SingleKeyEnforcementLogs），不单独再起一个日志区块，避免重复挂载同一份审计请求。
export function ApiKeyManageDialog({
  item,
  onClose,
  onNotice,
  onRegenerateKey,
  onRegenerated,
  onDeleteKey,
  onDisableKey,
  onRestoreKey,
  onSavePolicies,
  onResetQuotas,
  onAdjustQuotas,
  onClearQuotaAdjustments,
}: ApiKeyManageDialogProps) {
  const { t } = useTranslation();
  const [confirmKind, setConfirmKind] = useState<'regenerate' | 'delete' | null>(null);
  const [pending, setPending] = useState(false);

  const enforcementState = item.policy?.enforcementState;
  // 非 active（含摘要不可用）时：按钮切到“恢复”，且禁止重新生成。
  const inactive = enforcementState !== 'active';
  const title = item.keyAlias.trim() || item.displayKey;

  const handleRegenerate = useCallback(async () => {
    if (!onRegenerateKey) return;
    setPending(true);
    try {
      const created = await onRegenerateKey(item.id);
      if (created) {
        setConfirmKind(null);
        onRegenerated(created);
      } else {
        onNotice?.('error', t('usage_stats.api_key_settings_action_failed'));
      }
    } finally {
      setPending(false);
    }
  }, [item.id, onNotice, onRegenerateKey, onRegenerated, t]);

  const handleDelete = useCallback(async () => {
    if (!onDeleteKey) return;
    setPending(true);
    try {
      const deleted = await onDeleteKey(item.id);
      if (deleted) {
        setConfirmKind(null);
        // 删除成功后这个 key 已经不存在了，面板没有继续管理的意义，直接关闭。
        onClose();
      } else {
        onNotice?.('error', t('usage_stats.api_key_settings_action_failed'));
      }
    } finally {
      setPending(false);
    }
  }, [item.id, onClose, onDeleteKey, onNotice, t]);

  const handleToggleEnforcement = useCallback(async () => {
    const action = inactive ? onRestoreKey : onDisableKey;
    if (!action) return;
    setPending(true);
    try {
      const ok = await action(item.id);
      if (!ok) onNotice?.('error', t('usage_stats.api_key_settings_action_failed'));
    } finally {
      setPending(false);
    }
  }, [inactive, item.id, onDisableKey, onNotice, onRestoreKey, t]);

  return (
    <>
      <Modal open title={title} onClose={onClose}>
        <div className={styles.apiKeyManageStack}>
          {onSavePolicies && (
            <section className={styles.apiKeyManageSection} data-api-key-manage-quota>
              <h3 className={styles.apiKeyManageSectionTitle}>{t('usage_stats.api_key_quota_settings_title')}</h3>
              <ApiKeyQuotaSection
                item={item}
                onSubmit={onSavePolicies}
                onAdjust={onAdjustQuotas ?? noopAdjustQuotas}
                onClearAdjustments={onClearQuotaAdjustments ?? noopClearQuotaAdjustments}
              />
            </section>
          )}
          {onResetQuotas && (
            <section className={styles.apiKeyManageSection}>
              <h3 className={styles.apiKeyManageSectionTitle}>{t('usage_stats.api_key_quota_reset_title')}</h3>
              <ResetQuotaSection item={item} onResetQuotas={onResetQuotas} />
            </section>
          )}
          {(onRegenerateKey || onDisableKey || onRestoreKey || onDeleteKey) && (
            <section className={styles.apiKeyManageSection}>
              <h3 className={styles.apiKeyManageSectionTitle}>{t('usage_stats.api_key_manage_actions_title')}</h3>
              <div className={styles.apiKeyManageActions}>
                {onRegenerateKey && (
                  <Button
                    type="button"
                    variant="secondary"
                    onClick={() => setConfirmKind('regenerate')}
                    disabled={pending || inactive}
                    title={inactive ? t('usage_stats.api_key_settings_regenerate_blocked') : t('usage_stats.api_key_settings_regenerate')}
                  >
                    {t('usage_stats.api_key_settings_regenerate')}
                  </Button>
                )}
                {(onDisableKey || onRestoreKey) && (
                  <Button
                    type="button"
                    variant="secondary"
                    onClick={() => void handleToggleEnforcement()}
                    disabled={pending}
                  >
                    {inactive ? t('usage_stats.api_key_settings_restore') : t('usage_stats.api_key_settings_disable')}
                  </Button>
                )}
                {onDeleteKey && (
                  <Button
                    type="button"
                    variant="danger"
                    onClick={() => setConfirmKind('delete')}
                    disabled={pending}
                  >
                    {t('usage_stats.api_key_settings_delete')}
                  </Button>
                )}
              </div>
            </section>
          )}
        </div>
      </Modal>

      <Modal
        open={confirmKind !== null}
        title={confirmKind === 'delete' ? t('usage_stats.api_key_settings_delete') : t('usage_stats.api_key_settings_regenerate')}
        onClose={() => setConfirmKind(null)}
        closeDisabled={pending}
      >
        <div className={styles.apiKeyConfirmStack}>
          <p>{confirmKind === 'delete'
            ? t('usage_stats.api_key_settings_delete_confirm')
            : t('usage_stats.api_key_settings_regenerate_confirm')}</p>
          {confirmKind === 'delete' && (
            <Button type="button" fullWidth variant="danger" loading={pending} onClick={() => void handleDelete()}>
              {t('usage_stats.api_key_settings_delete')}
            </Button>
          )}
          {confirmKind === 'regenerate' && (
            <Button type="button" fullWidth loading={pending} onClick={() => void handleRegenerate()}>
              {t('usage_stats.api_key_settings_regenerate')}
            </Button>
          )}
          <Button type="button" fullWidth variant="ghost" onClick={() => setConfirmKind(null)} disabled={pending}>
            {t('common.cancel')}
          </Button>
        </div>
      </Modal>
    </>
  );
}
