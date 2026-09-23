import { describe, expect, it } from 'vitest';
import { resolveAdminLoginError } from '../App';
import { ApiError } from '../lib/api';

// fork 独有的管理员 TOTP 登录错误分类；与上游 App 测试分文件，避免同步上游时冲突。
describe('App admin login error mapping', () => {
  it('classifies every admin login failure into its error kind', () => {
    expect(resolveAdminLoginError(new ApiError('totp_code_required', 401))).toBe('totp_required');
    expect(resolveAdminLoginError(new ApiError('invalid totp code', 401))).toBe('invalid_totp');
    expect(resolveAdminLoginError(new ApiError('invalid password', 401))).toBe('invalid_password');
    expect(resolveAdminLoginError(new ApiError('boom', 500))).toBe('login_failed');
    expect(resolveAdminLoginError(new Error('x'))).toBe('login_failed');
  });
});
