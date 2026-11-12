import { describe, expect, it } from 'vitest';
import i18n from '../index';
import { RANKING_METRICS, RANKING_PERIODS } from '@/features/ranking/types';

describe('Ranking translations', () => {
  it('labels the local ranking tab and page in every language', () => {
    expect(i18n.t('usage_stats.tab_ranking', { lng: 'en' })).toBe('Ranking');
    expect(i18n.t('usage_stats.tab_ranking', { lng: 'zh' })).toBe('排名');
    expect(i18n.t('usage_stats.tab_ranking', { lng: 'zh-TW' })).toBe('排名');
    expect(i18n.t('ranking.local_title', { lng: 'en' })).toBe('API Key Ranking');
  });

  it('translates every period and metric column the page renders', () => {
    for (const lng of ['en', 'zh', 'zh-TW']) {
      for (const period of RANKING_PERIODS) {
        expect(i18n.exists(`ranking.period_${period}`, { lng })).toBe(true);
        expect(i18n.exists(`ranking.period_trigger_${period}`, { lng })).toBe(true);
      }
      for (const metric of RANKING_METRICS) {
        expect(i18n.exists(`ranking.metric_short_${metric}`, { lng })).toBe(true);
      }
    }
  });

  it('drops the community participation and overall score copy', () => {
    for (const key of ['ranking.metric_short_overall', 'ranking.score', 'ranking.join', 'ranking.scope_community']) {
      expect(i18n.exists(key, { lng: 'en' })).toBe(false);
    }
  });

  it('uses API Key consistently in localized profile copy', () => {
    expect(i18n.t('ranking.local_profile_edit', { lng: 'zh' })).toBe('编辑本地 API Key 资料');
    expect(i18n.t('ranking.local_profile_alias', { lng: 'zh' })).toBe('API Key 别名');
    expect(i18n.t('ranking.local_profile_save_failed', { lng: 'zh' })).toBe('暂时无法更新此本地 API Key 资料。');
    expect(i18n.t('ranking.local_profile_edit', { lng: 'zh-TW' })).toBe('編輯本地 API Key 資料');
    expect(i18n.t('ranking.local_profile_alias', { lng: 'zh-TW' })).toBe('API Key 別名');
    expect(i18n.t('ranking.local_profile_save_failed', { lng: 'zh-TW' })).toBe('暫時無法更新此本地 API Key 資料。');
  });
});
