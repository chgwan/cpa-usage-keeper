import { describe, expect, it } from 'vitest';
import { isAdminLoginFormValid, shouldShowTOTPCodeField } from '../LoginPage';

// fork 独有的管理员 TOTP 登录表单逻辑；与上游 LoginPage 测试分文件。
describe('LoginPage TOTP code field', () => {
  it('shows the code field only for admin mode when the server asked for it', () => {
    expect(shouldShowTOTPCodeField('admin', true)).toBe(true);
    expect(shouldShowTOTPCodeField('admin', false)).toBe(false);
    expect(shouldShowTOTPCodeField('api_key', true)).toBe(false);
  });

  it('requires a code only when the field is visible', () => {
    expect(isAdminLoginFormValid('pw', '123456', true)).toBe(true);
    expect(isAdminLoginFormValid('pw', '', true)).toBe(false);
    expect(isAdminLoginFormValid('pw', '', false)).toBe(true);
    expect(isAdminLoginFormValid('', '123456', true)).toBe(false);
  });
});
