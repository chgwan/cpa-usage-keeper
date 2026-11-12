import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Select } from '@/components/ui/Select';
import { RANKING_PERIODS, type RankingPeriod } from '../types';
import styles from '../RankingPage.module.scss';

export interface RankingToolbarProps {
  period: RankingPeriod;
  onPeriodChange: (period: RankingPeriod) => void;
}

// 触发器保持短名称，展开菜单保留完整周期语义。
export function RankingToolbar({ period, onPeriodChange }: RankingToolbarProps) {
  const { t } = useTranslation();
  const periodOptions = useMemo(
    () => RANKING_PERIODS.map((value) => ({
      value,
      label: t(`ranking.period_${value}`),
      triggerLabel: t(`ranking.period_trigger_${value}`),
    })),
    [t],
  );
  const currentPeriodLabel = periodOptions.find((option) => option.value === period)?.triggerLabel ?? period;

  return (
    <div className={styles.toolbar} data-ranking-toolbar>
      <div className={styles.periodControl} data-ranking-period>
        <Select
          value={period}
          options={periodOptions}
          onChange={(value) => onPeriodChange(value as RankingPeriod)}
          className={styles.periodSelect}
          ariaLabel={currentPeriodLabel}
          dropdownMinWidth={180}
          fullWidth={false}
        />
      </div>
    </div>
  );
}
