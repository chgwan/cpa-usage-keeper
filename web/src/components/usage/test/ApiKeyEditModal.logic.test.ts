import { describe, expect, it } from 'vitest';

import {
  nextApiKeyEditConfirm,
  resolveApiKeyEditLabel,
} from '../ApiKeyEditModal';

describe('nextApiKeyEditConfirm', () => {
  it('enters the requested confirm step from idle', () => {
    expect(nextApiKeyEditConfirm({ type: 'request', kind: 'regenerate' })).toBe('regenerate');
    expect(nextApiKeyEditConfirm({ type: 'request', kind: 'delete' })).toBe('delete');
  });

  it('keeps only one pending confirm step at a time', () => {
    expect(nextApiKeyEditConfirm({ type: 'request', kind: 'delete' })).toBe('delete');
    expect(nextApiKeyEditConfirm({ type: 'request', kind: 'regenerate' })).toBe('regenerate');
  });

  it('ignores new confirm requests while an action is busy', () => {
    expect(nextApiKeyEditConfirm({ type: 'request', kind: 'delete' }, true)).toBe('idle');
  });

  it('returns to idle on cancel and after the action settles', () => {
    expect(nextApiKeyEditConfirm({ type: 'cancel' })).toBe('idle');
    expect(nextApiKeyEditConfirm({ type: 'settled' })).toBe('idle');
  });
});

describe('resolveApiKeyEditLabel', () => {
  it('prefers the trimmed alias over the backend label and masked key', () => {
    expect(resolveApiKeyEditLabel({
      keyAlias: '  Primary  ',
      label: 'sk-*********123456',
      displayKey: 'sk-*********123456',
    })).toBe('Primary');
  });

  it('falls back to the backend label and then the masked key', () => {
    expect(resolveApiKeyEditLabel({ keyAlias: '   ', label: 'Primary', displayKey: 'sk-*********123456' })).toBe('Primary');
    expect(resolveApiKeyEditLabel({ keyAlias: '', label: 'sk-*********123456', displayKey: 'sk-*********123456' })).toBe('sk-*********123456');
    expect(resolveApiKeyEditLabel({ keyAlias: '', label: '', displayKey: 'sk-*********654321' })).toBe('sk-*********654321');
  });
});
