import { type KeyboardEvent, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { LoadingSpinner } from '@/components/ui/LoadingSpinner';
import { Modal } from '@/components/ui/Modal';
import { RankingApiError } from './api';
import { RankingAvatar } from './components/RankingAvatar';
import { RankingLeaderboardResults } from './components/RankingLeaderboardResults';
import { RankingToolbar } from './components/RankingToolbar';
import type {
  LocalRankingProfileRequest,
  LocalRankingProfileResponse,
  RankingLeaderboardEntry,
  RankingLeaderboardResponse,
  RankingPeriod,
} from './types';
import styles from './RankingPage.module.scss';

const AVATAR_IDS = Array.from({ length: 66 }, (_, index) => index + 1);

type AsyncAction = () => Promise<unknown>;
type Translate = (key: string, params?: Record<string, string | number>) => string;

export interface RankingPageProps {
  period: RankingPeriod;
  leaderboard: RankingLeaderboardResponse | null;
  leaderboardLoading: boolean;
  leaderboardError: unknown;
  onRetryLeaderboard: AsyncAction;
  onUpdateLocalProfile: (participantID: string, profile: LocalRankingProfileRequest) => Promise<LocalRankingProfileResponse>;
  onPeriodChange: (period: RankingPeriod) => void;
}

const formatError = (error: unknown, t: Translate): string => {
  if (!(error instanceof RankingApiError)) return t('ranking.error_generic');
  const knownCodes: Record<string, string> = {
    invalid_local_ranking_profile: 'ranking.local_profile_save_failed',
    local_ranking_key_not_found: 'ranking.local_profile_save_failed',
    local_ranking_profile_update_failed: 'ranking.local_profile_save_failed',
  };
  return t(knownCodes[error.code] ?? 'ranking.error_generic');
};

export function RankingPage(props: RankingPageProps) {
  const { t, i18n } = useTranslation();
  const [localProfileEntry, setLocalProfileEntry] = useState<RankingLeaderboardEntry | null>(null);
  const [localProfileAlias, setLocalProfileAlias] = useState('');
  const [localProfileAvatarID, setLocalProfileAvatarID] = useState(1);
  const [localProfileSaving, setLocalProfileSaving] = useState(false);
  const [localProfileError, setLocalProfileError] = useState<unknown>(null);
  // 榜单内容只接受当前周期；上一周期的响应只用于在加载期间稳定标题栏。
  const currentBoard = props.leaderboard?.period === props.period ? props.leaderboard : null;

  const openLocalProfileModal = (entry: RankingLeaderboardEntry) => {
    setLocalProfileEntry(entry);
    setLocalProfileAlias(entry.key_alias ?? '');
    setLocalProfileAvatarID(entry.avatar_id);
    setLocalProfileError(null);
  };

  const closeLocalProfileModal = () => {
    if (localProfileSaving) return;
    setLocalProfileEntry(null);
    setLocalProfileError(null);
  };

  const saveLocalProfile = async () => {
    if (!localProfileEntry || localProfileSaving) return;
    setLocalProfileSaving(true);
    setLocalProfileError(null);
    try {
      await props.onUpdateLocalProfile(localProfileEntry.participant_id, {
        key_alias: localProfileAlias.trim(),
        avatar_id: localProfileAvatarID,
      });
      setLocalProfileEntry(null);
    } catch (error) {
      setLocalProfileError(error);
    } finally {
      setLocalProfileSaving(false);
    }
  };

  return (
    <section className={styles.page} aria-label={t('ranking.local_title')}>
      <LeaderboardCard
        period={props.period}
        board={currentBoard}
        loading={props.leaderboardLoading}
        error={props.leaderboardError}
        onRetry={props.onRetryLeaderboard}
        onEditLocalProfile={openLocalProfileModal}
        onPeriodChange={props.onPeriodChange}
        t={t}
        language={i18n.language}
      />

      <Modal
        open={localProfileEntry !== null}
        title={t('ranking.local_profile_edit')}
        onClose={closeLocalProfileModal}
        closeDisabled={localProfileSaving}
        width={600}
        className={styles.profileModal}
        footer={(
          <>
            <Button variant="secondary" appearance="action" onClick={closeLocalProfileModal} disabled={localProfileSaving}>
              {t('common.cancel')}
            </Button>
            <Button
              appearance="action"
              onClick={() => void saveLocalProfile()}
              loading={localProfileSaving}
              data-ranking-local-profile-save
            >
              {t('ranking.local_profile_save')}
            </Button>
          </>
        )}
      >
        {localProfileEntry ? (
          <div className={styles.profileModalContent}>
            <div className={styles.joinForm}>
              <Input
                name="local-ranking-key-alias"
                value={localProfileAlias}
                onChange={(event) => setLocalProfileAlias(event.target.value)}
                label={t('ranking.local_profile_alias')}
                hint={t('ranking.local_profile_alias_hint')}
                placeholder={localProfileEntry.key_alias ? undefined : localProfileEntry.display_name}
                autoComplete="off"
                disabled={localProfileSaving}
              />
              <div className={styles.avatarField}>
                <span className={styles.fieldLabel}>{t('ranking.avatar')}</span>
                <AvatarPicker
                  value={localProfileAvatarID}
                  onChange={setLocalProfileAvatarID}
                  t={t}
                  disabled={localProfileSaving}
                />
              </div>
            </div>
            {localProfileError ? (
              <div className={styles.errorBox} role="alert" data-ranking-local-profile-error>
                {formatError(localProfileError, t)}
              </div>
            ) : null}
          </div>
        ) : null}
      </Modal>
    </section>
  );
}

function AvatarPicker({ value, onChange, t, disabled = false }: {
  value: number;
  onChange: (value: number) => void;
  t: Translate;
  disabled?: boolean;
}) {
  const handleAvatarKeyDown = (event: KeyboardEvent<HTMLButtonElement>, currentID: number) => {
    const currentIndex = AVATAR_IDS.indexOf(currentID);
    let nextIndex = currentIndex;
    switch (event.key) {
      case 'ArrowRight':
      case 'ArrowDown':
        nextIndex = (currentIndex + 1) % AVATAR_IDS.length;
        break;
      case 'ArrowLeft':
      case 'ArrowUp':
        nextIndex = (currentIndex - 1 + AVATAR_IDS.length) % AVATAR_IDS.length;
        break;
      case 'Home':
        nextIndex = 0;
        break;
      case 'End':
        nextIndex = AVATAR_IDS.length - 1;
        break;
      default:
        return;
    }
    event.preventDefault();
    const nextID = AVATAR_IDS[nextIndex];
    onChange(nextID);
    event.currentTarget.parentElement
      ?.querySelector<HTMLButtonElement>(`[data-ranking-avatar-option="${nextID}"]`)
      ?.focus();
  };

  return (
    <div className={styles.avatarGrid} role="radiogroup" aria-label={t('ranking.avatar')}>
      {AVATAR_IDS.map((id) => (
        <button
          key={id}
          type="button"
          role="radio"
          aria-checked={value === id}
          aria-label={t('ranking.avatar_option', { id })}
          tabIndex={value === id ? 0 : -1}
          className={`${styles.avatarOption} ${value === id ? styles.avatarOptionActive : ''}`.trim()}
          onClick={() => onChange(id)}
          onKeyDown={(event) => handleAvatarKeyDown(event, id)}
          data-ranking-avatar-option={id}
          disabled={disabled}
        >
          <RankingAvatar avatarID={id} name={t('ranking.avatar_option', { id })} decorative />
        </button>
      ))}
    </div>
  );
}

interface LeaderboardCardProps {
  period: RankingPeriod;
  board: RankingLeaderboardResponse | null;
  loading: boolean;
  error: unknown;
  onRetry: AsyncAction;
  onEditLocalProfile: (entry: RankingLeaderboardEntry) => void;
  onPeriodChange: (period: RankingPeriod) => void;
  t: Translate;
  language: string;
}

function LeaderboardCard({
  period,
  board,
  loading,
  error,
  onRetry,
  onEditLocalProfile,
  onPeriodChange,
  t,
  language,
}: LeaderboardCardProps) {
  const rows = useMemo(() => board?.entries.slice(0, 100) ?? [], [board]);
  return (
    <article className={`card ${styles.leaderboardCard}`.trim()} aria-busy={loading}>
      <header className={styles.leaderboardHeader}>
        <div className={styles.leaderboardTitle} data-ranking-header-title>
          <div className="keeper-card-title-track">
            <h2 className={`${styles.metricTitleHeading} keeper-card-title`} data-ranking-title>
              {t('ranking.local_title')}
            </h2>
            <div className={styles.leaderboardHeaderToolbar} data-ranking-header-toolbar>
              <RankingToolbar period={period} onPeriodChange={onPeriodChange} />
            </div>
          </div>
          {board && (
            <div className={styles.boardMeta}>
              {board.stale && <span className={styles.staleBadge} data-ranking-stale>{t('ranking.stale')} · {board.period_key}</span>}
              <span>{t('ranking.updated_at', { time: formatDateTime(board.generated_at, language) })}</span>
            </div>
          )}
        </div>
      </header>
      {loading && !board ? (
        <LoadingState text={t('ranking.leaderboard_loading')} />
      ) : error && !board ? (
        <ErrorState error={error} onRetry={onRetry} t={t} />
      ) : rows.length === 0 ? (
        <EmptyState title={t('ranking.local_empty_title')} description={t('ranking.local_empty_description')} />
      ) : (
        <RankingLeaderboardResults entries={rows} onEditLocalProfile={onEditLocalProfile} />
      )}
    </article>
  );
}

function LoadingState({ text }: { text: string }) {
  return <div className={styles.loadingState}><LoadingSpinner size={20} /><span>{text}</span></div>;
}

function ErrorState({ error, onRetry, t }: { error: unknown; onRetry: AsyncAction; t: Translate }) {
  return (
    <div className={styles.errorState} role="alert">
      <p>{formatError(error, t)}</p>
      <Button variant="secondary" appearance="action" onClick={() => void onRetry()}>{t('common.retry')}</Button>
    </div>
  );
}

function EmptyState({ title, description }: { title: string; description: string }) {
  return <div className={styles.emptyState}><strong>{title}</strong><p>{description}</p></div>;
}

function formatDateTime(value: string, language: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(language, { dateStyle: 'medium', timeStyle: 'short', hour12: false }).format(date);
}
