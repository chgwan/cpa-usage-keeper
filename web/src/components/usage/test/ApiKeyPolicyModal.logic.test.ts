import { describe, expect, it } from 'vitest';

import { formToLimits, limitsToForm } from '../ApiKeyPolicyModal';

describe('limits form conversion', () => {
  it('limitsToForm keys inputs by type:window', () => {
    const form = limitsToForm([
      { type: 'cost', window: 'weekly', value: 12 },
      { type: 'cost', window: 'monthly', value: 5.5 },
    ]);
    expect(form['cost:weekly']).toBe('12');
    expect(form['cost:monthly']).toBe('5.5');
  });

  it('formToLimits drops blank entries and parses numbers', () => {
    const result = formToLimits({ 'cost:daily': '10', 'cost:weekly': '', 'cost:monthly': '2.5' });
    expect(result.error).toBeNull();
    expect(result.limits).toEqual([
      { type: 'cost', window: 'daily', value: 10 },
      { type: 'cost', window: 'monthly', value: 2.5 },
    ]);
  });

  it('formToLimits rejects zero, negative, and non-numeric values', () => {
    expect(formToLimits({ 'cost:daily': '0' }).error).toBeTruthy();
    expect(formToLimits({ 'cost:daily': '-3' }).error).toBeTruthy();
    expect(formToLimits({ 'cost:daily': 'abc' }).error).toBeTruthy();
  });
});
