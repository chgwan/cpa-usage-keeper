import { describe, expect, it } from 'vitest';
import { isTOTPDisableFormValid } from './TOTPSettingsCard';

describe('TOTPSettingsCard disable form', () => {
  it('requires both password and code', () => {
    expect(isTOTPDisableFormValid('secret', '123456')).toBe(true);
    expect(isTOTPDisableFormValid('', '123456')).toBe(false);
    expect(isTOTPDisableFormValid('secret', '')).toBe(false);
    expect(isTOTPDisableFormValid('  ', '123456')).toBe(false);
  });
});
