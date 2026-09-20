import { describe, expect, it } from 'vitest';
import i18n from '../index';

// fork 独有的 API Key 限额文案；上游删除了 i18n/index.test.ts，这里单独保留。
describe('API key quota translations', () => {
  it('localizes the api key quota progress label in every language', () => {
    expect(i18n.getResource('en', 'translation', 'usage_stats.api_key_policy_progress_label')).toBe('Usage quota progress');
    expect(i18n.getResource('zh', 'translation', 'usage_stats.api_key_policy_progress_label')).toBe('用量配额进度');
    expect(i18n.getResource('zh-TW', 'translation', 'usage_stats.api_key_policy_progress_label')).toBe('用量限額進度');
  });
});
