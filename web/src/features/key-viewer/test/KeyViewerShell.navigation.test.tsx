// @vitest-environment happy-dom

import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { KeyViewerShell } from '../KeyViewerShell';

globalThis.IS_REACT_ACT_ENVIRONMENT = true;

describe('API-key viewer navigation', () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(async () => {
    await i18n.changeLanguage('en');
    container = document.createElement('div');
    document.body.appendChild(container);
    root = createRoot(container);
  });

  afterEach(() => {
    act(() => root.unmount());
    container.remove();
  });

  it.each(['overview', 'analysis'] as const)('offers only Overview and Analysis on the %s page', (activePage) => {
    const onNavigate = vi.fn();

    act(() => root.render(
      <KeyViewerShell activePage={activePage} apiKey={{ display_key: 'sk-***' }} toolbar={null} onNavigate={onNavigate}>
        <p>Key usage</p>
      </KeyViewerShell>,
    ));

    const navigation = container.querySelector('[role="tablist"][aria-label="API Key viewer sections"]');
    expect(navigation).not.toBeNull();
    const tabs = Array.from(navigation!.querySelectorAll<HTMLButtonElement>('[role="tab"]'));
    expect(tabs.map((tab) => tab.textContent)).toEqual(['Overview', 'Analysis']);
    expect(tabs[activePage === 'overview' ? 0 : 1].getAttribute('aria-selected')).toBe('true');

    act(() => tabs[1].click());
    expect(onNavigate).toHaveBeenCalledWith('/key-analysis');
  });
});
