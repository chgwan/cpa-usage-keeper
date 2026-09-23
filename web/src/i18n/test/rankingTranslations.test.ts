import { describe, expect, it } from 'vitest';
import i18n from '../index';

describe('Ranking translations', () => {
  it('describes joining as a manual retry state and locks the profile on first submission', () => {
    expect(i18n.t('ranking.status_joining', { lng: 'en' })).toBe('Registration pending');
    expect(i18n.t('ranking.join_retry', { lng: 'en' })).toBe('Retry Registration');
    expect(i18n.t('ranking.join_confirm_body', { lng: 'en' })).toContain('first submission');
    expect(i18n.t('ranking.join_confirm_body', { lng: 'en' })).not.toContain('successful registration');
  });

  it('clearly states that pausing stops ranking data uploads', () => {
    expect(i18n.t('ranking.pause_confirm_body', { lng: 'zh' })).toContain('停止同步排名数据');
  });

  it('capitalizes every word in English Ranking page actions', () => {
    const expectedEnglishActions = {
      'usage_stats.back_to_cpa': 'Back To CPA',
      'usage_stats.back_to_cpa_aria': 'Back To CPA Management',
      'ranking.privacy_title': 'Participation Is Optional',
      'ranking.period_current_month': 'This Month',
      'ranking.period_previous_month': 'Last Month',
      'ranking.metric_label': 'Ranking Metric',
      'ranking.join': 'Join Ranking',
      'ranking.profile_action': 'My Ranking',
      'ranking.join_confirm_action': 'Confirm And Join',
      'ranking.join_retry': 'Retry Registration',
      'ranking.sync_now': 'Sync Now',
      'ranking.pause': 'Pause Participation',
      'ranking.resume': 'Resume Participation',
      'ranking.pause_confirm_action': 'Confirm Pause',
      'ranking.exit': 'Exit Ranking',
      'ranking.exit_confirm_action': 'Permanently Exit',
    } as const;

    for (const [key, label] of Object.entries(expectedEnglishActions)) {
      expect(i18n.t(key, { lng: 'en' })).toBe(label);
    }
  });

  it('keeps the localized Ranking action labels unchanged', () => {
    expect(i18n.t('ranking.join', { lng: 'zh' })).toBe('参与排名');
    expect(i18n.t('ranking.join', { lng: 'zh-TW' })).toBe('參與排名');
    expect(i18n.t('ranking.pause', { lng: 'zh' })).toBe('暂停参与');
    expect(i18n.t('ranking.pause', { lng: 'zh-TW' })).toBe('暫停參與');
  });

  it('localizes the compact scope labels', () => {
    expect(i18n.t('ranking.scope_local', { lng: 'en' })).toBe('Local');
    expect(i18n.t('ranking.scope_community', { lng: 'en' })).toBe('Community');
    expect(i18n.t('ranking.scope_local', { lng: 'zh' })).toBe('本地');
    expect(i18n.t('ranking.scope_community', { lng: 'zh' })).toBe('社区');
    expect(i18n.t('ranking.scope_local', { lng: 'zh-TW' })).toBe('本地');
    expect(i18n.t('ranking.scope_community', { lng: 'zh-TW' })).toBe('社群');
  });

  it('uses API Key consistently in localized profile copy', () => {
    expect(i18n.t('ranking.local_profile_edit', { lng: 'zh' })).toBe('编辑本地 API Key 资料');
    expect(i18n.t('ranking.local_profile_alias', { lng: 'zh' })).toBe('API Key 别名');
    expect(i18n.t('ranking.local_profile_save_failed', { lng: 'zh' })).toBe('暂时无法更新此本地 API Key 资料。');
    expect(i18n.t('ranking.local_profile_edit', { lng: 'zh-TW' })).toBe('編輯本地 API Key 資料');
    expect(i18n.t('ranking.local_profile_alias', { lng: 'zh-TW' })).toBe('API Key 別名');
    expect(i18n.t('ranking.local_profile_save_failed', { lng: 'zh-TW' })).toBe('暫時無法更新此本地 API Key 資料。');
  });

  it('capitalizes every word in English Ranking metric options only', () => {
    const expectedEnglishMetrics = {
      'ranking.metric_overall': 'Overall Ranking',
      'ranking.metric_total_tokens': 'Total Token',
      'ranking.metric_request_count': 'Requests',
      'ranking.metric_cache_read_rate': 'Cache Read Rate',
      'ranking.metric_ttft_average': 'Average Time To First Token',
      'ranking.metric_latency_average': 'Average Total Latency',
      'ranking.metric_peak_tpm': 'Peak TPM',
      'ranking.metric_peak_rpm': 'Peak RPM',
      'ranking.metric_cost': 'Cost',
    } as const;

    for (const [key, label] of Object.entries(expectedEnglishMetrics)) {
      expect(i18n.t(key, { lng: 'en' })).toBe(label);
    }
    expect(i18n.t('ranking.metric_cache_read_rate', { lng: 'zh' })).toBe('缓存读取率');
    expect(i18n.t('ranking.metric_cache_read_rate', { lng: 'zh-TW' })).toBe('快取讀取率');
    expect(i18n.t('ranking.metric_cost', { lng: 'zh' })).toBe('费用');
    expect(i18n.t('ranking.metric_cost', { lng: 'zh-TW' })).toBe('費用');
  });
});
