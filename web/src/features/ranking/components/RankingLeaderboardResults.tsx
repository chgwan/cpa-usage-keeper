import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { formatLeaderboardValue, formatOverallMetricValue } from '../format';
import type {
  RankingDetailMetric,
  RankingLeaderboardEntry,
  RankingMetric,
  RankingScope,
} from '../types';
import styles from '../RankingPage.module.scss';
import { RankingAvatar } from './RankingAvatar';

const OVERALL_METRICS: RankingDetailMetric[] = [
  'total_tokens',
  'request_count',
  'cache_read_rate',
  'ttft_average',
  'latency_average',
  'peak_tpm',
  'peak_rpm',
];

// 费用是本地榜单独有列，Community 综合分面板保持原列集。
const overallMetricsForScope = (scope: RankingScope): RankingDetailMetric[] => (
  scope === 'local' ? [...OVERALL_METRICS, 'cost'] : OVERALL_METRICS
);

// score 指当前榜单主值列：综合视图下是综合分，单指标视图下是该指标本身。
type RankingSortColumn = 'score' | RankingDetailMetric;
type RankingSortDirection = 'descending' | 'ascending';
interface RankingSort {
  column: RankingSortColumn;
  direction: RankingSortDirection;
}

const sortValue = (entry: RankingLeaderboardEntry, column: RankingSortColumn): number | undefined => (
  column === 'score' ? entry.value : entry.metrics?.[column]
);

// 排序只重排表格行，缺值的行不论方向都沉底；领奖台和名次徽标仍按榜单原序。
const sortEntries = (
  entries: readonly RankingLeaderboardEntry[],
  sort: RankingSort | null,
): Array<{ entry: RankingLeaderboardEntry; position: number }> => {
  const rows = entries.map((entry, index) => ({ entry, position: index + 1 }));
  if (!sort) return rows;
  const factor = sort.direction === 'descending' ? -1 : 1;
  return rows.sort((left, right) => {
    const leftValue = sortValue(left.entry, sort.column);
    const rightValue = sortValue(right.entry, sort.column);
    if (leftValue === undefined || rightValue === undefined) {
      if (leftValue === rightValue) return left.position - right.position;
      return leftValue === undefined ? 1 : -1;
    }
    return (leftValue - rightValue) * factor || left.position - right.position;
  });
};

// 点击依次为降序、升序、恢复榜单原序；换列时从降序重新开始。
const nextSort = (current: RankingSort | null, column: RankingSortColumn): RankingSort | null => {
  if (current?.column !== column) return { column, direction: 'descending' };
  return current.direction === 'descending' ? { column, direction: 'ascending' } : null;
};

export interface RankingLeaderboardResultsProps {
  scope: RankingScope;
  metric: RankingMetric;
  entries: readonly RankingLeaderboardEntry[];
  onEditLocalProfile?: (entry: RankingLeaderboardEntry) => void;
}

export function RankingLeaderboardResults({
  scope,
  metric,
  entries,
  onEditLocalProfile,
}: RankingLeaderboardResultsProps) {
  const { t } = useTranslation();
  const podium = entries.slice(0, 3);
  const overallMetrics = overallMetricsForScope(scope);
  const [selectedSort, setSort] = useState<RankingSort | null>(null);
  // 切到单指标视图后原来的指标列已不在表头，此时不再沿用它的排序。
  const sort = selectedSort && (metric === 'overall' || selectedSort.column === 'score') ? selectedSort : null;
  const rows = useMemo(() => sortEntries(entries, sort), [entries, sort]);
  const toggleSort = (column: RankingSortColumn) => setSort((current) => nextSort(current, column));

  return (
    <div className={styles.leaderboardResults} data-ranking-results>
      <div className={styles.podiumGrid} aria-label={`${t('ranking.rank')} 1–3`} data-ranking-podium>
        {podium.map((entry, index) => (
          <PodiumCard
            key={entry.participant_id}
            entry={entry}
            position={index + 1}
            metric={metric}
            scope={scope}
            onEditLocalProfile={onEditLocalProfile}
          />
        ))}
      </div>
      <div className={styles.tableScroll}>
        <table className={styles.table}>
          <thead>
            <tr>
              <th className={styles.rankColumn} data-ranking-rank-column>{t('ranking.rank')}</th>
              <th className={styles.participantColumn} data-ranking-participant-column>
                {t(scope === 'local' ? 'ranking.api_key' : 'ranking.participant')}
              </th>
              {metric === 'overall' ? (
                <>
                  <SortableHeader column="score" label={t('ranking.score')} sort={sort} onSort={toggleSort} />
                  {overallMetrics.map((item) => (
                    <SortableHeader key={item} column={item} label={t(`ranking.metric_short_${item}`)} sort={sort} onSort={toggleSort} />
                  ))}
                </>
              ) : <SortableHeader column="score" label={t(`ranking.metric_${metric}`)} sort={sort} onSort={toggleSort} />}
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
                    <LeaderboardEntryAvatar
                      entry={entry}
                      scope={scope}
                      className={styles.tableAvatar}
                      onEditLocalProfile={onEditLocalProfile}
                    />
                    <strong>{entry.display_name}</strong>
                  </div>
                </td>
                {metric === 'overall' ? (
                  <>
                    <td className={`${styles.numberCell} ${styles.scoreCell}`.trim()}>{formatLeaderboardValue(metric, entry, scope)}</td>
                    {overallMetrics.map((item) => (
                      <td key={item} className={styles.numberCell}>{formatOverallMetricValue(item, entry)}</td>
                    ))}
                  </>
                ) : <td className={`${styles.numberCell} ${styles.scoreCell}`.trim()}>{formatLeaderboardValue(metric, entry, scope)}</td>}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function SortableHeader({ column, label, sort, onSort }: {
  column: RankingSortColumn;
  label: string;
  sort: RankingSort | null;
  onSort: (column: RankingSortColumn) => void;
}) {
  const direction = sort?.column === column ? sort.direction : null;
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

function PodiumCard({ entry, position, metric, scope, onEditLocalProfile }: {
  entry: RankingLeaderboardEntry;
  position: number;
  metric: RankingMetric;
  scope: RankingScope;
  onEditLocalProfile?: (entry: RankingLeaderboardEntry) => void;
}) {
  const { t } = useTranslation();
  const value = formatLeaderboardValue(metric, entry, scope);
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
      <LeaderboardEntryAvatar
        entry={entry}
        scope={scope}
        className={styles.podiumAvatar}
        onEditLocalProfile={onEditLocalProfile}
      />
      <strong className={styles.podiumName}>{entry.display_name}</strong>
      <span className={`${styles.podiumValue} ${valueSizeClass}`.trim()}>{value}</span>
    </article>
  );
}

function LeaderboardEntryAvatar({ entry, scope, className, onEditLocalProfile }: {
  entry: RankingLeaderboardEntry;
  scope: RankingScope;
  className: string;
  onEditLocalProfile?: (entry: RankingLeaderboardEntry) => void;
}) {
  const { t } = useTranslation();
  if (scope !== 'local' || !onEditLocalProfile) {
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
