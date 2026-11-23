import { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { Modal } from '@/components/ui/Modal';
import { IconCheck, IconChevronDown, IconRefreshCw, IconX } from '@/components/ui/icons';
import type {
  ApiKeyModelAccessRule,
  ApiKeyModelAccessState,
  ApiKeyModelAccessUpdateRequest,
  CpaApiKeySettingsItem,
} from '@/lib/types';
import { ApiKeyPicker } from './ApiKeyQuotaDialogs';
import styles from '@/pages/UsagePage.module.scss';

// Mirrors the common family presets offered by LTbinglingfeng/key-model-access Web UI.
export const COMMON_MODEL_WILDCARDS = [
  'gpt-*',
  'claude-*',
  'gemini-*',
  'qwen-*',
  'deepseek-*',
  'grok-*',
  'kimi-*',
  'glm-*',
  'minimax-*',
] as const;

export function isWildcardModelPattern(pattern: string): boolean {
  return pattern.includes('*') || pattern.includes('?');
}

// Mirrors key-model-access wildcardMatch: '*' matches any chars including '/', '?' matches 1 char, case-sensitive.
export function modelPatternMatches(pattern: string, model: string): boolean {
  const escaped = pattern.replace(/[.+^${}()|[\]\\]/g, '\\$&').replace(/\*/g, '.*').replace(/\?/g, '.');
  try {
    return new RegExp(`^${escaped}$`).test(model);
  } catch {
    return false;
  }
}

export function normalizeModelPatterns(patterns: readonly string[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of patterns) {
    const trimmed = raw.trim();
    if (!trimmed || seen.has(trimmed)) continue;
    seen.add(trimmed);
    out.push(trimmed);
  }
  return out;
}

export function buildModelAccessUpdateRequest(
  selectedIds: readonly string[],
  allowModels: readonly string[],
  denyModels: readonly string[],
): { request: ApiKeyModelAccessUpdateRequest | null; error: 'keys' | null } {
  if (selectedIds.length === 0) {
    return { request: null, error: 'keys' };
  }
  return {
    request: {
      apiKeyIds: [...selectedIds],
      allowModels: normalizeModelPatterns(allowModels),
      denyModels: normalizeModelPatterns(denyModels),
    },
    error: null,
  };
}

export function hasModelAccessRules(rule: ApiKeyModelAccessRule | undefined): boolean {
  return Boolean(rule && (rule.allowModels.length > 0 || rule.denyModels.length > 0));
}

export function formatModelAccessTooltip(
  rule: ApiKeyModelAccessRule,
  allowLabel: string,
  denyLabel: string,
): string {
  const parts: string[] = [];
  if (rule.allowModels.length > 0) {
    parts.push(`${allowLabel}: ${rule.allowModels.join(', ')}`);
  }
  if (rule.denyModels.length > 0) {
    parts.push(`${denyLabel}: ${rule.denyModels.join(', ')}`);
  }
  return parts.join(' · ');
}

interface ModelRulePickerProps {
  kind: 'allow' | 'deny';
  title: string;
  description: string;
  selectedModels: string[];
  catalog: string[];
  modelsError?: string;
  disabled?: boolean;
  onChange: (next: string[]) => void;
}

function ModelRulePicker({
  kind,
  title,
  description,
  selectedModels,
  catalog,
  modelsError,
  disabled = false,
  onChange,
}: ModelRulePickerProps) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [customPattern, setCustomPattern] = useState('');

  const deny = kind === 'deny';
  const selectedSet = useMemo(() => new Set(selectedModels), [selectedModels]);
  const wildcardRules = useMemo(() => selectedModels.filter(isWildcardModelPattern), [selectedModels]);
  const wildcardAll = selectedSet.has('*');

  const matchedRuleFor = useCallback(
    (model: string) => wildcardRules.find((rule) => modelPatternMatches(rule, model)) ?? '',
    [wildcardRules],
  );

  const effectiveCatalogCount = useMemo(
    () => catalog.filter((model) => selectedSet.has(model) || Boolean(matchedRuleFor(model))).length,
    [catalog, matchedRuleFor, selectedSet],
  );

  const visiblePresets = useMemo(
    () => COMMON_MODEL_WILDCARDS.filter(
      (rule) => selectedSet.has(rule) || catalog.some((model) => modelPatternMatches(rule, model)),
    ),
    [catalog, selectedSet],
  );

  const normalizedQuery = query.trim().toLowerCase();
  const filteredWildcardAll = !normalizedQuery || '* all models 全部模型 wildcard 通配符'.includes(normalizedQuery);
  const filteredPresets = useMemo(
    () => visiblePresets.filter((rule) => !normalizedQuery || `${rule} wildcard 通配符`.toLowerCase().includes(normalizedQuery)),
    [normalizedQuery, visiblePresets],
  );
  const filteredCatalog = useMemo(
    () => catalog.filter((model) => !normalizedQuery || model.toLowerCase().includes(normalizedQuery)),
    [catalog, normalizedQuery],
  );

  const toggleModel = useCallback(
    (pattern: string) => {
      if (disabled) return;
      if (selectedSet.has(pattern)) {
        onChange(selectedModels.filter((item) => item !== pattern));
      } else {
        onChange(normalizeModelPatterns([...selectedModels, pattern]));
      }
    },
    [disabled, onChange, selectedModels, selectedSet],
  );

  const removeModel = useCallback(
    (pattern: string) => {
      if (disabled) return;
      onChange(selectedModels.filter((item) => item !== pattern));
    },
    [disabled, onChange, selectedModels],
  );

  const handleAddCustom = useCallback(() => {
    const trimmed = customPattern.trim();
    if (!trimmed || disabled) return;
    onChange(normalizeModelPatterns([...selectedModels, trimmed]));
    setCustomPattern('');
  }, [customPattern, disabled, onChange, selectedModels]);

  const summaryText = wildcardAll
    ? t('usage_stats.api_key_model_access_all_wildcard')
    : catalog.length > 0
      ? effectiveCatalogCount > 0
        ? t('usage_stats.api_key_model_access_matched', { matched: effectiveCatalogCount, total: catalog.length })
        : t('usage_stats.api_key_model_access_choose_from', { total: catalog.length })
      : selectedModels.length > 0
        ? t('usage_stats.api_key_model_access_rules_count', { count: selectedModels.length })
        : t('usage_stats.api_key_model_access_catalog_unavailable');

  return (
    <div
      className={`${styles.apiKeyModelRuleEditor} ${deny ? styles.apiKeyModelRuleEditorDeny : ''}`.trim()}
      data-model-access-picker={kind}
    >
      <div className={styles.apiKeyModelRuleHead}>
        <div className={styles.apiKeyModelRuleTitleGroup}>
          <strong className={styles.apiKeyModelRuleTitle}>
            <span className={`${styles.apiKeyModelKindDot} ${deny ? styles.apiKeyModelKindDotDeny : ''}`.trim()} aria-hidden="true" />
            {title}
          </strong>
          <span className={styles.apiKeyModelRuleDesc}>{description}</span>
        </div>
        <span className={styles.apiKeyModelRuleCount}>
          {t('usage_stats.api_key_model_access_rules_count', { count: selectedModels.length })}
        </span>
      </div>

      <div className={styles.apiKeyModelChips}>
        {selectedModels.length === 0 ? (
          <span className={styles.apiKeyModelEmptyChips}>
            {t(deny ? 'usage_stats.api_key_model_access_no_denied' : 'usage_stats.api_key_model_access_unrestricted')}
          </span>
        ) : (
          selectedModels.map((model) => {
            const wildcard = isWildcardModelPattern(model);
            const outsideCatalog = !wildcard && catalog.length > 0 && !catalog.includes(model);
            return (
              <span
                key={model}
                className={`${styles.apiKeyModelChip} ${deny ? styles.apiKeyModelChipDeny : ''}`.trim()}
                data-model-access-chip={`${kind}:${model}`}
              >
                <span className={styles.apiKeyModelChipLabel}>{model}</span>
                {wildcard && <small className={styles.apiKeyModelChipTag}>{t('usage_stats.api_key_model_access_wildcard')}</small>}
                {outsideCatalog && <small className={styles.apiKeyModelChipTag}>{t('usage_stats.api_key_model_access_outside_catalog')}</small>}
                <button
                  type="button"
                  className={styles.apiKeyModelChipRemove}
                  onClick={() => removeModel(model)}
                  disabled={disabled}
                  aria-label={t('usage_stats.api_key_model_access_remove_rule', { model })}
                >
                  <IconX size={12} />
                </button>
              </span>
            );
          })
        )}
      </div>

      <div className={styles.apiKeyModelCustomRow}>
        <Input
          value={customPattern}
          onChange={(event) => setCustomPattern(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Enter') {
              event.preventDefault();
              handleAddCustom();
            }
          }}
          placeholder={t('usage_stats.api_key_model_access_custom_placeholder')}
          aria-label={t('usage_stats.api_key_model_access_custom_aria', { title })}
          className={`${styles.usagePillControl} ${styles.apiKeyModelCustomInput}`.trim()}
          disabled={disabled}
          data-model-access-custom-input={kind}
        />
        <Button
          type="button"
          variant="secondary"
          size="sm"
          onClick={handleAddCustom}
          disabled={disabled || !customPattern.trim()}
          data-model-access-custom-add={kind}
        >
          {t('usage_stats.api_key_model_access_add_pattern')}
        </Button>
      </div>

      <button
        type="button"
        className={`${styles.apiKeyModelTrigger} ${open ? styles.apiKeyModelTriggerOpen : ''}`.trim()}
        onClick={() => setOpen((current) => !current)}
        aria-expanded={open}
        disabled={disabled}
        data-model-access-toggle={kind}
      >
        <span>{summaryText}</span>
        <IconChevronDown size={14} className={styles.apiKeyModelTriggerChevron} />
      </button>

      {open && (
        <div className={styles.apiKeyModelPanel} data-model-access-panel={kind}>
          {catalog.length > 0 && (
            <div className={styles.apiKeyModelSearch}>
              <Input
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder={t('usage_stats.api_key_model_access_search_placeholder')}
                aria-label={t('usage_stats.api_key_model_access_search_placeholder')}
                className={`${styles.usagePillControl} ${styles.apiKeyModelSearchInput}`.trim()}
                disabled={disabled}
                data-model-access-search={kind}
              />
            </div>
          )}

          <div className={styles.apiKeyModelList} role="listbox" aria-multiselectable="true">
            {filteredWildcardAll && (
              <button
                type="button"
                role="option"
                aria-selected={wildcardAll}
                className={`${styles.apiKeyModelOption} ${wildcardAll ? styles.apiKeyModelOptionSelected : ''}`.trim()}
                onClick={() => toggleModel('*')}
                disabled={disabled}
                data-model-access-option={`${kind}:*`}
              >
                <span className={styles.apiKeyModelCheckbox} aria-hidden="true">
                  {wildcardAll && <IconCheck size={12} />}
                </span>
                <span className={styles.apiKeyModelOptionCopy}>
                  <strong>{t('usage_stats.api_key_model_access_all_models')}</strong>
                  <small>{t(deny ? 'usage_stats.api_key_model_access_all_deny_hint' : 'usage_stats.api_key_model_access_all_allow_hint')}</small>
                </span>
                <span className={styles.apiKeyModelOptionBadge}>{t('usage_stats.api_key_model_access_wildcard')}</span>
              </button>
            )}

            {filteredPresets.map((rule) => {
              const explicit = selectedSet.has(rule);
              const derived = wildcardAll && !explicit;
              const checked = explicit || derived;
              const matchCount = catalog.filter((model) => modelPatternMatches(rule, model)).length;
              return (
                <button
                  key={rule}
                  type="button"
                  role="option"
                  aria-selected={checked}
                  aria-disabled={derived}
                  className={`${styles.apiKeyModelOption} ${checked ? styles.apiKeyModelOptionSelected : ''} ${derived ? styles.apiKeyModelOptionDerived : ''}`.trim()}
                  onClick={() => { if (!derived) toggleModel(rule); }}
                  disabled={disabled || derived}
                  data-model-access-option={`${kind}:${rule}`}
                >
                  <span className={styles.apiKeyModelCheckbox} aria-hidden="true">
                    {checked && <IconCheck size={12} />}
                  </span>
                  <span className={styles.apiKeyModelOptionCopy}>
                    <strong>{rule}</strong>
                    <small>{t('usage_stats.api_key_model_access_preset_hint', { count: matchCount })}</small>
                  </span>
                  <span className={styles.apiKeyModelOptionBadge}>{t('usage_stats.api_key_model_access_wildcard')}</span>
                </button>
              );
            })}

            {filteredCatalog.map((model) => {
              const explicit = selectedSet.has(model);
              const matchedRule = explicit ? '' : matchedRuleFor(model);
              const derived = Boolean(matchedRule);
              const checked = explicit || derived;
              return (
                <button
                  key={model}
                  type="button"
                  role="option"
                  aria-selected={checked}
                  aria-disabled={derived}
                  className={`${styles.apiKeyModelOption} ${checked ? styles.apiKeyModelOptionSelected : ''} ${derived ? styles.apiKeyModelOptionDerived : ''}`.trim()}
                  onClick={() => { if (!derived) toggleModel(model); }}
                  disabled={disabled || derived}
                  data-model-access-option={`${kind}:${model}`}
                >
                  <span className={styles.apiKeyModelCheckbox} aria-hidden="true">
                    {checked && <IconCheck size={12} />}
                  </span>
                  <span className={styles.apiKeyModelOptionCopy}>
                    <strong>{model}</strong>
                    {derived && <small>{t('usage_stats.api_key_model_access_matched_by', { rule: matchedRule })}</small>}
                  </span>
                  {derived && <span className={styles.apiKeyModelOptionBadge}>{t('usage_stats.api_key_model_access_wildcard')}</span>}
                </button>
              );
            })}

            {!filteredWildcardAll && filteredPresets.length === 0 && filteredCatalog.length === 0 && (
              <p className={styles.apiKeyModelNoMatch}>{t('usage_stats.api_key_model_access_no_match')}</p>
            )}
          </div>

          {catalog.length > 0 ? (
            <div className={styles.apiKeyModelPanelFooter}>
              <span>{t('usage_stats.api_key_model_access_matched', { matched: effectiveCatalogCount, total: catalog.length })}</span>
              <div className={styles.apiKeyModelPanelFooterActions}>
                <button
                  type="button"
                  className={styles.apiKeyModelFooterLink}
                  onClick={() => onChange(normalizeModelPatterns([...selectedModels, ...catalog]))}
                  disabled={disabled}
                >
                  {t('usage_stats.api_key_model_access_select_all')}
                </button>
                <button
                  type="button"
                  className={styles.apiKeyModelFooterLink}
                  onClick={() => onChange([])}
                  disabled={disabled}
                >
                  {t('usage_stats.api_key_model_access_clear')}
                </button>
              </div>
            </div>
          ) : (
            modelsError && (
              <p className={styles.apiKeyModelCatalogNotice}>{modelsError}</p>
            )
          )}
        </div>
      )}
    </div>
  );
}

interface ModelAccessStatusBarProps {
  modelAccess: ApiKeyModelAccessState;
  reloading: boolean;
  onReload?: () => Promise<void>;
}

function ModelAccessStatusBar({ modelAccess, reloading, onReload }: ModelAccessStatusBarProps) {
  const { t } = useTranslation();
  const status = modelAccess.status;
  if (!status) return null;

  return (
    <div className={styles.apiKeyModelStatusBar} data-model-access-status>
      <div className={styles.apiKeyModelStatusRow}>
        <span className={styles.apiKeyModelStatusMeta} title={status.policyFile || undefined}>
          {status.persistentUpdates
            ? t('usage_stats.api_key_model_access_status_persistent', { revision: status.revision, path: status.policyFile })
            : t('usage_stats.api_key_model_access_status_memory', { revision: status.revision })}
        </span>
        {status.persistentUpdates && onReload && (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className={styles.apiKeyModelReloadButton}
            loading={reloading}
            onClick={() => void onReload()}
            data-model-access-reload
          >
            <IconRefreshCw size={12} />
            <span>{t('usage_stats.api_key_model_access_reload')}</span>
          </Button>
        )}
      </div>
      {!status.persistentUpdates && status.persistenceSetupError && (
        <p className={styles.apiKeyPolicyError}>
          {t('usage_stats.api_key_model_access_persist_failed', { error: status.persistenceSetupError })}
        </p>
      )}
      {(status.failClosed || status.lastError) && (
        <p className={styles.apiKeyPolicyError}>
          {status.failClosed
            ? t('usage_stats.api_key_model_access_fail_closed', { error: status.lastError || 'unknown' })
            : t('usage_stats.api_key_model_access_last_error', { error: status.lastError })}
        </p>
      )}
    </div>
  );
}

interface UseApiKeyModelAccessFormOptions {
  apiKeys: CpaApiKeySettingsItem[];
  modelAccess: ApiKeyModelAccessState;
  onSubmit: (request: ApiKeyModelAccessUpdateRequest) => Promise<boolean>;
  onReload?: () => Promise<boolean>;
  initialSelectedIds?: string[];
  onDone?: () => void;
}

function useApiKeyModelAccessForm({
  apiKeys,
  modelAccess,
  onSubmit,
  onReload,
  initialSelectedIds,
  onDone,
}: UseApiKeyModelAccessFormOptions) {
  const [selectedKeys, setSelectedKeys] = useState<Set<string>>(() => new Set(initialSelectedIds ?? []));
  const initialSingleId = initialSelectedIds?.length === 1 ? initialSelectedIds[0] : null;
  const initialRule = initialSingleId ? modelAccess.policies[initialSingleId] : undefined;
  const [allowModels, setAllowModels] = useState<string[]>(() => [...(initialRule?.allowModels ?? [])]);
  const [denyModels, setDenyModels] = useState<string[]>(() => [...(initialRule?.denyModels ?? [])]);
  const [error, setError] = useState<'keys' | null>(null);
  const [pending, setPending] = useState(false);
  const [reloading, setReloading] = useState(false);

  const selectedIds = useMemo(
    () => apiKeys.filter((item) => selectedKeys.has(item.id)).map((item) => item.id),
    [apiKeys, selectedKeys],
  );

  // Sync form state when a single fixed key's remote policy updates (e.g. after Reload or Save).
  const fixedKeyId = initialSelectedIds?.length === 1 ? initialSelectedIds[0] : null;
  const fixedRule = fixedKeyId ? modelAccess.policies[fixedKeyId] : undefined;
  useEffect(() => {
    if (!fixedKeyId) return;
    setAllowModels([...(fixedRule?.allowModels ?? [])]);
    setDenyModels([...(fixedRule?.denyModels ?? [])]);
  }, [fixedKeyId, fixedRule]);

  const onKeysChange = useCallback((next: Set<string>) => {
    setSelectedKeys(next);
    setError(null);
    if (next.size === 1) {
      const onlyId = Array.from(next)[0];
      const rule = modelAccess.policies[onlyId];
      setAllowModels([...(rule?.allowModels ?? [])]);
      setDenyModels([...(rule?.denyModels ?? [])]);
    } else if (next.size === 0) {
      setAllowModels([]);
      setDenyModels([]);
    }
  }, [modelAccess.policies]);

  const handleSubmit = useCallback(async () => {
    const built = buildModelAccessUpdateRequest(selectedIds, allowModels, denyModels);
    setError(built.error);
    if (!built.request) return;
    setPending(true);
    try {
      if (await onSubmit(built.request)) {
        onDone?.();
      }
    } finally {
      setPending(false);
    }
  }, [allowModels, denyModels, onDone, onSubmit, selectedIds]);

  const handleReload = useCallback(async () => {
    if (!onReload) return;
    setReloading(true);
    try {
      await onReload();
    } finally {
      setReloading(false);
    }
  }, [onReload]);

  return {
    selectedKeys,
    onKeysChange,
    selectedIds,
    allowModels,
    setAllowModels,
    denyModels,
    setDenyModels,
    error,
    pending,
    reloading,
    handleSubmit,
    handleReload: onReload ? handleReload : undefined,
  };
}

export interface ApiKeyModelAccessModalProps {
  apiKeys: CpaApiKeySettingsItem[];
  modelAccess: ApiKeyModelAccessState;
  onClose: () => void;
  onSubmit: (request: ApiKeyModelAccessUpdateRequest) => Promise<boolean>;
  onReload?: () => Promise<boolean>;
}

export function ApiKeyModelAccessModal({
  apiKeys,
  modelAccess,
  onClose,
  onSubmit,
  onReload,
}: ApiKeyModelAccessModalProps) {
  const { t } = useTranslation();
  const form = useApiKeyModelAccessForm({
    apiKeys,
    modelAccess,
    onSubmit,
    onReload,
    onDone: onClose,
  });

  const busy = form.pending || form.reloading;

  return (
    <Modal
      open
      title={t('usage_stats.api_key_model_access_title')}
      onClose={onClose}
      closeDisabled={busy}
      footer={
        <>
          <Button type="button" variant="secondary" onClick={onClose} disabled={busy}>
            {t('common.cancel')}
          </Button>
          <Button
            type="button"
            loading={form.pending}
            disabled={form.reloading}
            onClick={() => void form.handleSubmit()}
            data-api-key-model-access-submit
          >
            {t('common.save')}
          </Button>
        </>
      }
    >
      <div className={styles.apiKeyPolicyStack}>
        <ModelAccessStatusBar modelAccess={modelAccess} reloading={form.reloading} onReload={form.handleReload} />
        <p className={styles.apiKeyPolicyUsageHint}>{t('usage_stats.api_key_model_access_hint')}</p>
        <ApiKeyPicker
          apiKeys={apiKeys}
          selected={form.selectedKeys}
          onChange={form.onKeysChange}
          disabled={busy}
          renderBadge={(item) => {
            const rule = modelAccess.policies[item.id];
            if (!hasModelAccessRules(rule)) return null;
            return (
              <span className={styles.apiKeyModelPickerBadge}>
                {t('usage_stats.api_key_model_access_configured_short')}
              </span>
            );
          }}
        />
        <ModelRulePicker
          kind="allow"
          title={t('usage_stats.api_key_model_access_allow_title')}
          description={t('usage_stats.api_key_model_access_allow_desc')}
          selectedModels={form.allowModels}
          catalog={modelAccess.models}
          modelsError={modelAccess.modelsError}
          disabled={busy || form.selectedIds.length === 0}
          onChange={form.setAllowModels}
        />
        <ModelRulePicker
          kind="deny"
          title={t('usage_stats.api_key_model_access_deny_title')}
          description={t('usage_stats.api_key_model_access_deny_desc')}
          selectedModels={form.denyModels}
          catalog={modelAccess.models}
          modelsError={modelAccess.modelsError}
          disabled={busy || form.selectedIds.length === 0}
          onChange={form.setDenyModels}
        />
        {form.error === 'keys' && (
          <p className={styles.apiKeyPolicyError}>{t('usage_stats.api_key_quota_select_keys_required')}</p>
        )}
      </div>
    </Modal>
  );
}

export interface ApiKeyModelAccessSectionProps {
  item: CpaApiKeySettingsItem;
  modelAccess: ApiKeyModelAccessState;
  onSubmit: (request: ApiKeyModelAccessUpdateRequest) => Promise<boolean>;
  onReload?: () => Promise<boolean>;
}

export function ApiKeyModelAccessSection({
  item,
  modelAccess,
  onSubmit,
  onReload,
}: ApiKeyModelAccessSectionProps) {
  const { t } = useTranslation();
  const apiKeys = useMemo(() => [item], [item]);
  const initialIds = useMemo(() => [item.id], [item.id]);
  const form = useApiKeyModelAccessForm({
    apiKeys,
    modelAccess,
    onSubmit,
    onReload,
    initialSelectedIds: initialIds,
  });

  const busy = form.pending || form.reloading;

  return (
    <div className={styles.apiKeyQuotaSectionStack}>
      <ModelAccessStatusBar modelAccess={modelAccess} reloading={form.reloading} onReload={form.handleReload} />
      <p className={styles.apiKeyPolicyUsageHint}>{t('usage_stats.api_key_model_access_hint_single_key')}</p>
      <ModelRulePicker
        kind="allow"
        title={t('usage_stats.api_key_model_access_allow_title')}
        description={t('usage_stats.api_key_model_access_allow_desc')}
        selectedModels={form.allowModels}
        catalog={modelAccess.models}
        modelsError={modelAccess.modelsError}
        disabled={busy}
        onChange={form.setAllowModels}
      />
      <ModelRulePicker
        kind="deny"
        title={t('usage_stats.api_key_model_access_deny_title')}
        description={t('usage_stats.api_key_model_access_deny_desc')}
        selectedModels={form.denyModels}
        catalog={modelAccess.models}
        modelsError={modelAccess.modelsError}
        disabled={busy}
        onChange={form.setDenyModels}
      />
      <div className={styles.apiKeyManageActions}>
        <Button
          type="button"
          loading={form.pending}
          disabled={form.reloading}
          onClick={() => void form.handleSubmit()}
          data-api-key-model-access-submit
        >
          {t('common.save')}
        </Button>
      </div>
    </div>
  );
}
