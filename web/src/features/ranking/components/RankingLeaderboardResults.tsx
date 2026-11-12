import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { formatEntryMetric, metricValue } from '../format';
import { DEFAULT_RANKING_METRIC, RANKING_METRICS, type RankingLeaderboardEntry, type RankingMetric } from '../types';
import styles from '../RankingPage.module.scss';
import { RankingAvatar } from './RankingAvatar';

type RankingSortDirection = 'descending' | 'ascending';
export interface RankingSort {
  column: RankingMetric;
  direction: RankingSortDirection;
}

// 默认按 Token 总量降序，与后端榜单原序一致。
export const DEFAULT_RANKING_SORT: RankingSort = { column: DEFAULT_RANKING_METRIC, direction: 'descending' };

// 排序只重排表格行，缺值的行不论方向都沉底；领奖台和名次徽标仍按榜单原序。
export const sortEntries = (
  entries: readonly RankingLeaderboardEntry[],
  sort: RankingSort,
): Array<{ entry: RankingLeaderboardEntry; position: number }> => {
  const rows = entries.map((entry, index) => ({ entry, position: index + 1 }));
  const factor = sort.direction === 'descending' ? -1 : 1;
  return rows.sort((left, right) => {
    const leftValue = metricValue(left.entry, sort.column);
    const rightValue = metricValue(right.entry, sort.column);
    if (leftValue === undefined || rightValue === undefined) {
      if (leftValue === rightValue) return left.position - right.position;
      return leftValue === undefined ? 1 : -1;
    }
    return (leftValue - rightValue) * factor || left.position - right.position;
  });
};

// 同一列在降序与升序间切换；换列时从降序重新开始。
export const nextSort = (current: RankingSort, column: RankingMetric): RankingSort => {
  if (current.column !== column) return { column, direction: 'descending' };
  return { column, direction: current.direction === 'descending' ? 'ascending' : 'descending' };
};

export interface RankingLeaderboardResultsProps {
  entries: readonly RankingLeaderboardEntry[];
  onEditLocalProfile?: (entry: RankingLeaderboardEntry) => void;
}

export function RankingLeaderboardResults({ entries, onEditLocalProfile }: RankingLeaderboardResultsProps) {
  const { t } = useTranslation();
  const podium = entries.slice(0, 3);
  const [sort, setSort] = useState<RankingSort>(DEFAULT_RANKING_SORT);
  const rows = useMemo(() => sortEntries(entries, sort), [entries, sort]);
  const toggleSort = (column: RankingMetric) => setSort((current) => nextSort(current, column));

  return (
    <div className={styles.leaderboardResults} data-ranking-results>
      <div className={styles.podiumGrid} aria-label={`${t('ranking.rank')} 1–3`} data-ranking-podium>
        {podium.map((entry, index) => (
          <PodiumCard
            key={entry.participant_id}
            entry={entry}
            position={index + 1}
            onEditLocalProfile={onEditLocalProfile}
          />
        ))}
      </div>
      <div className={styles.tableScroll}>
        <table className={styles.table}>
          <thead>
            <tr>
              <th className={styles.rankColumn} data-ranking-rank-column>{t('ranking.rank')}</th>
              <th className={styles.participantColumn} data-ranking-participant-column>{t('ranking.api_key')}</th>
              {RANKING_METRICS.map((metric) => (
                <SortableHeader key={metric} column={metric} label={t(`ranking.metric_short_${metric}`)} sort={sort} onSort={toggleSort} />
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map(({ entry, position }) => (
              <tr key={entry.participant_id} data-ranking-row>
                <td className={styles.rankColumn} data-ranking-rank-column>
                  <span className={styles.rankBadge} data-ranking-position>{position}</span>
                </td>
                <td className={styles.participantColumn} data-ranking-participant-column>
                  <div className={styles.participantCell}>
                    <LeaderboardEntryAvatar entry={entry} className={styles.tableAvatar} onEditLocalProfile={onEditLocalProfile} />
                    <strong>{entry.display_name}</strong>
                  </div>
                </td>
                {RANKING_METRICS.map((metric) => (
                  <td key={metric} className={styles.numberCell} data-ranking-metric-cell={metric}>{formatEntryMetric(entry, metric)}</td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function SortableHeader({ column, label, sort, onSort }: {
  column: RankingMetric;
  label: string;
  sort: RankingSort;
  onSort: (column: RankingMetric) => void;
}) {
  const direction = sort.column === column ? sort.direction : null;
  return (
    <th className={styles.numberCell} aria-sort={direction ?? 'none'}>
      <button
        type="button"
        className={`${styles.sortButton} ${direction ? styles.sortButtonActive : ''}`.trim()}
        onClick={() => onSort(column)}
        data-ranking-sort={column}
      >
        <span>{label}</span>
        <span className={styles.sortIndicator} aria-hidden="true">
          {direction === 'descending' ? '▼' : direction === 'ascending' ? '▲' : '↕'}
        </span>
      </button>
    </th>
  );
}

function PodiumCard({ entry, position, onEditLocalProfile }: {
  entry: RankingLeaderboardEntry;
  position: number;
  onEditLocalProfile?: (entry: RankingLeaderboardEntry) => void;
}) {
  const { t } = useTranslation();
  const value = formatEntryMetric(entry, DEFAULT_RANKING_METRIC);
  const valueSizeClass = value.length >= 11
    ? styles.podiumValueCompact
    : value.length >= 8
      ? styles.podiumValueMedium
      : '';

  return (
    <article
      className={`${styles.podiumCard} ${styles[`podiumCard${position}` as keyof typeof styles]}`.trim()}
      data-ranking-podium-rank={position}
    >
      <div className={styles.podiumRank}>
        <span>{t('ranking.rank')}</span>
        <strong>{String(position).padStart(2, '0')}</strong>
      </div>
      <LeaderboardEntryAvatar entry={entry} className={styles.podiumAvatar} onEditLocalProfile={onEditLocalProfile} />
      <strong className={styles.podiumName}>{entry.display_name}</strong>
      <span className={`${styles.podiumValue} ${valueSizeClass}`.trim()}>{value}</span>
    </article>
  );
}

function LeaderboardEntryAvatar({ entry, className, onEditLocalProfile }: {
  entry: RankingLeaderboardEntry;
  className: string;
  onEditLocalProfile?: (entry: RankingLeaderboardEntry) => void;
}) {
  const { t } = useTranslation();
  if (!onEditLocalProfile) {
    return <RankingAvatar avatarID={entry.avatar_id} name={entry.display_name} className={className} decorative />;
  }
  return (
    <button
      type="button"
      className={`${styles.localProfileAvatarButton} ${className}`.trim()}
      aria-label={t('ranking.local_profile_edit_label', { name: entry.display_name })}
      onClick={() => onEditLocalProfile(entry)}
      data-ranking-local-profile-edit={entry.participant_id}
    >
      <RankingAvatar avatarID={entry.avatar_id} name={entry.display_name} decorative />
    </button>
  );
}
