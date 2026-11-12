// @vitest-environment happy-dom

import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { RankingToolbar } from '../RankingToolbar';

globalThis.IS_REACT_ACT_ENVIRONMENT = true;

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => key,
  }),
}));

describe('RankingToolbar', () => {
  let container: HTMLDivElement;
  let root: ReturnType<typeof createRoot>;

  beforeEach(() => {
    container = document.createElement('div');
    document.body.appendChild(container);
    root = createRoot(container);
  });

  afterEach(() => {
    act(() => root.unmount());
    container.remove();
  });

  it('renders the ranking period as one labelled select instead of four tabs', () => {
    const onPeriodChange = vi.fn();
    act(() => root.render(
      <RankingToolbar
        period="today"
        onPeriodChange={onPeriodChange}
      />,
    ));

    expect(container.querySelector('[data-ranking-toolbar]')).not.toBeNull();
    expect(container.querySelector('[data-ranking-periods]')).toBeNull();
    expect(container.querySelector('[data-ranking-metric]')).toBeNull();
    const periodTrigger = container.querySelector<HTMLButtonElement>('[data-ranking-period] button');
    expect(periodTrigger?.textContent).toContain('ranking.period_trigger_today');
    expect(periodTrigger?.getAttribute('aria-label')).toBe('ranking.period_trigger_today');
    act(() => periodTrigger!.click());
    const listbox = document.querySelector<HTMLElement>('[role="listbox"]');
    const yesterday = Array.from(listbox!.querySelectorAll<HTMLButtonElement>('button'))
      .find((button) => button.textContent?.includes('ranking.period_yesterday'));
    act(() => yesterday!.click());
    expect(onPeriodChange).toHaveBeenCalledWith('yesterday');
  });

  it('offers the day, week and month periods', () => {
    act(() => root.render(<RankingToolbar period="today" onPeriodChange={vi.fn()} />));
    act(() => container.querySelector<HTMLButtonElement>('[data-ranking-period] button')!.click());
    const labels = Array.from(document.querySelectorAll<HTMLButtonElement>('[role="listbox"] button'))
      .map((button) => button.textContent ?? '');
    expect(labels).toEqual([
      'ranking.period_today',
      'ranking.period_yesterday',
      'ranking.period_current_week',
      'ranking.period_previous_week',
      'ranking.period_current_month',
      'ranking.period_previous_month',
    ].map((label) => expect.stringContaining(label)));
  });
});
