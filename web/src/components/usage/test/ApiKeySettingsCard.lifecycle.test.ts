// @vitest-environment happy-dom
import { describe, expect, it } from 'vitest';

import { nextRevealState, type RevealState } from '../ApiKeySettingsCard';

describe('nextRevealState', () => {
  it('moves idle to revealed with the one-time key', () => {
    const state: RevealState = { phase: 'idle' };
    expect(nextRevealState(state, { id: '1', key: 'sk-once', keyAlias: 'a' })).toEqual({
      phase: 'revealed',
      key: 'sk-once',
      id: '1',
    });
  });

  it('replaces a previous reveal without keeping the old key', () => {
    const state: RevealState = { phase: 'revealed', id: '1', key: 'sk-old' };
    expect(nextRevealState(state, { id: '2', key: 'sk-new', keyAlias: 'b' })).toEqual({
      phase: 'revealed',
      id: '2',
      key: 'sk-new',
    });
  });

  it('dismiss return to idle and forgets the key', () => {
    const state: RevealState = { phase: 'revealed', id: '1', key: 'sk-once' };
    expect(nextRevealState(state, null)).toEqual({ phase: 'idle' });
  });
});
