import { describe, expect, it } from 'vitest';

import { formToLimits, limitsToForm } from '../ApiKeyPolicyModal';

describe('limits form conversion', () => {
  it('limitsToForm keys inputs by type:window', () => {
    const form = limitsToForm([
      { type: 'tokens', window: 'daily', value: 1000 },
      { type: 'cost', window: 'monthly', value: 5.5 },
    ]);
    expect(form['tokens:daily']).toBe('1000');
    expect(form['cost:monthly']).toBe('5.5');
  });

  it('formToLimits drops blank entries and parses numbers', () => {
    const result = formToLimits({ 'requests:daily': '10', 'tokens:daily': '', 'cost:monthly': '2.5' });
    expect(result.error).toBeNull();
    expect(result.limits).toEqual([
      { type: 'requests', window: 'daily', value: 10 },
      { type: 'cost', window: 'monthly', value: 2.5 },
    ]);
  });

  it('formToLimits rejects zero, negative, and non-numeric values', () => {
    expect(formToLimits({ 'tokens:daily': '0' }).error).toBeTruthy();
    expect(formToLimits({ 'tokens:daily': '-3' }).error).toBeTruthy();
    expect(formToLimits({ 'tokens:daily': 'abc' }).error).toBeTruthy();
  });
});
