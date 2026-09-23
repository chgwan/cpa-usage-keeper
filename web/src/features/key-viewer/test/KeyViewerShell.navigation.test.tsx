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

  it.each([
    ['overview', 0],
    ['realtime', 1],
    ['analysis', 2],
  ] as const)('offers Overview, Realtime, and Analysis on the %s page', (activePage, activeIndex) => {
    const onNavigate = vi.fn();

    act(() => root.render(
      <KeyViewerShell activePage={activePage} apiKey={{ display_key: 'sk-***' }} onRefresh={() => {}} onNavigate={onNavigate}>
        <p>Key usage</p>
      </KeyViewerShell>,
    ));

    const navigation = container.querySelector('[role="tablist"][aria-label="Usage sections"]');
    expect(navigation).not.toBeNull();
    const tabs = Array.from(navigation!.querySelectorAll<HTMLAnchorElement>('[role="tab"]'));
    expect(tabs.map((tab) => tab.textContent)).toEqual(['Overview', 'Realtime', 'Analysis']);
    expect(tabs[activeIndex].getAttribute('aria-selected')).toBe('true');

    const paths = ['/key-overview', '/key-realtime', '/key-analysis'];
    tabs.forEach((tab, index) => {
      if (index === activeIndex) return;
      act(() => tab.click());
      expect(onNavigate).toHaveBeenCalledWith(paths[index]);
    });
  });
});
