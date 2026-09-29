/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import React from 'react';
import { describe, it, expect } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
// Side-effect import: initializes the real i18next instance with en.json,
// same as helpers/formatting.js does for every other page test — without it
// tr('console.dashboard.usage_trend_window', ...) (no defaultValue arg) has
// no resources to resolve against and renders the bare key.
import '../../i18n/i18n';
import HfActivityChart from './HfActivityChart';

const formatSpend = (q) => `$${(q / 500000).toFixed(4)}`;

// Noon local time N days before a fixed local "today" — same convention as
// activitySeries.test.js.
const today = new Date(2026, 8, 22, 12, 0, 0);
const at = (daysAgo, hour = 12) => {
  const d = new Date(today);
  d.setDate(d.getDate() - daysAgo);
  d.setHours(hour, 0, 0, 0);
  return Math.floor(d.getTime() / 1000);
};
const end = at(0, 23);

const row = (daysAgo, model, quota) => ({
  created_at: at(daysAgo),
  model_name: model,
  quota,
  token_used: 0,
  count: 1,
});

describe('HfActivityChart — sparse-window auto-narrow', () => {
  it('starts at 7D when only the most recent day has traffic in a long window', () => {
    const rows = [row(0, 'model-a', 100000)];
    render(
      <HfActivityChart
        rows={rows}
        end={end}
        formatSpend={formatSpend}
        maxDays={29}
      />,
    );
    expect(screen.getByText('last 7 days')).toBeTruthy();
  });

  it('does not narrow when nonzero days are spread across the window', () => {
    const rows = [row(0, 'model-a', 100000), row(20, 'model-a', 100000)];
    render(
      <HfActivityChart
        rows={rows}
        end={end}
        formatSpend={formatSpend}
        maxDays={29}
      />,
    );
    expect(screen.getByText('last 29 days')).toBeTruthy();
  });

  it('a manual 29D pick after auto-narrowing sticks — the sparse check does not re-intervene', () => {
    const rows = [row(0, 'model-a', 100000)];
    render(
      <HfActivityChart
        rows={rows}
        end={end}
        formatSpend={formatSpend}
        maxDays={29}
      />,
    );
    expect(screen.getByText('last 7 days')).toBeTruthy();

    fireEvent.click(screen.getByTestId('activity-range-29'));
    expect(screen.getByText('last 29 days')).toBeTruthy();

    // Switching metrics re-runs the sparse check (rows/end/maxDays/metric
    // are its deps) — the manual pick must still win.
    fireEvent.click(screen.getByTestId('activity-metric-requests'));
    expect(screen.getByText('last 29 days')).toBeTruthy();
  });

  it('does not auto-narrow when the host page fixes the window (Rankings presets)', () => {
    const rows = [row(0, 'model-a', 100000)];
    render(
      <HfActivityChart
        rows={rows}
        end={end}
        formatSpend={formatSpend}
        maxDays={29}
        fixedWindow={{ days: 29 }}
      />,
    );
    // No range switch at all when fixedWindow is set.
    expect(screen.queryByTestId('activity-range-7')).toBeNull();
    expect(screen.queryByTestId('activity-range-29')).toBeNull();
  });
});

describe('HfActivityChart — allowCumulative', () => {
  const rows = [row(0, 'model-a', 100000), row(1, 'model-a', 200000)];

  it('renders no cumulative toggle when allowCumulative is unset (default false)', () => {
    render(
      <HfActivityChart
        rows={rows}
        end={end}
        formatSpend={formatSpend}
        maxDays={7}
      />,
    );
    expect(screen.queryByTestId('activity-cumulative')).toBeNull();
  });

  it('renders the toggle and redraws bars as a running total when clicked', () => {
    render(
      <HfActivityChart
        rows={rows}
        end={end}
        formatSpend={formatSpend}
        maxDays={2}
        allowCumulative
      />,
    );
    const toggle = screen.getByTestId('activity-cumulative');
    expect(toggle).toBeTruthy();

    fireEvent.click(toggle);
    // Two days: [200000, 100000] chronological -> cumulative [200000, 300000].
    // The bars should still render (no crash from the OTHER-only series) and
    // the chart total is unchanged (it's a running sum, so it still ends at
    // the same grand total).
    expect(screen.getByTestId('activity-total').textContent).toBe('$0.6000');
    const bars = screen.getAllByTestId('activity-bar');
    expect(bars).toHaveLength(2);
  });
});

describe('HfActivityChart — nonzero bars keep a visible minimum height', () => {
  it('a nonzero day never collapses to 0px tall', () => {
    // A single tiny value alongside a much larger one — without a floor the
    // small bar's height% would round to ~0.
    const rows = [row(0, 'model-a', 1000000), row(1, 'model-a', 1)];
    render(
      <HfActivityChart
        rows={rows}
        end={end}
        formatSpend={formatSpend}
        maxDays={2}
      />,
    );
    const bars = screen.getAllByTestId('activity-bar');
    const nonzero = bars.filter(
      (b) => b.getAttribute('data-nonzero') === 'true',
    );
    expect(nonzero).toHaveLength(2);
    for (const bar of nonzero) {
      expect(bar.style.minHeight).toBe('2px');
    }
  });
});
