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
import { render, screen } from '@testing-library/react';
// Side-effect import: real i18next instance with en.json resources — see
// the same note in HfActivityChart.test.jsx.
import '../../../i18n/i18n';
import KpiCards from './KpiCards';

const today = new Date(2026, 8, 22, 12, 0, 0);
const at = (daysAgo) => {
  const d = new Date(today);
  d.setDate(d.getDate() - daysAgo);
  d.setHours(12, 0, 0, 0);
  return Math.floor(d.getTime() / 1000);
};
const end = at(0);

const makeMe = (overrides = {}) => ({
  username: 'testuser',
  display_name: 'Test User',
  used_quota: 250000,
  remaining_quota: 750000,
  token_count: 3,
  request_count: 42,
  ...overrides,
});

const row = (daysAgo, quota, count = 1) => ({
  created_at: at(daysAgo),
  model_name: 'model-a',
  quota,
  count,
});

const baseProps = {
  loading: false,
  me: makeMe(),
  spendUSD: 0.5,
  remainUSD: '$1.50',
  quotaWindow: { start: at(29), end },
  quotaLoaded: true,
};

describe('KpiCards — sparkline + 环比', () => {
  it('shows no delta element when the prior 7-day period has no data (only the last 7 days do)', () => {
    // Traffic only in days 0-6 (the current window); days 7-13 (the prior
    // window) are empty — nothing honest to compare against.
    const quotaRows = [row(0, 100000), row(3, 50000)];
    render(<KpiCards {...baseProps} quotaRows={quotaRows} />);
    expect(screen.queryByTestId('kpi-spend-delta')).toBeNull();
    expect(screen.queryByTestId('kpi-requests-delta')).toBeNull();
  });

  it('shows a signed percentage delta when both periods have data', () => {
    // Prior window (days 7-13): 10 total. Current window (days 0-6): 20
    // total. percentDelta(20, 10) = +100.
    const quotaRows = [row(0, 20000), row(10, 10000)];
    render(<KpiCards {...baseProps} quotaRows={quotaRows} />);
    const delta = screen.getByTestId('kpi-spend-delta');
    expect(delta.textContent).toContain('+');
    expect(delta.textContent).toContain('%');
    expect(delta.textContent).toContain('100');
  });

  it('shows a down-arrow, unsigned-negative percentage when the current period is lower', () => {
    const quotaRows = [row(0, 10000), row(10, 20000)];
    render(<KpiCards {...baseProps} quotaRows={quotaRows} />);
    const delta = screen.getByTestId('kpi-spend-delta');
    expect(delta.textContent).toContain('↓');
    expect(delta.textContent).toContain('-50');
  });

  it('shows no sparkline or delta at all before /api/data/self/ has settled (quotaLoaded=false)', () => {
    const quotaRows = [row(0, 20000), row(10, 10000)];
    render(
      <KpiCards {...baseProps} quotaLoaded={false} quotaRows={quotaRows} />,
    );
    expect(screen.queryByTestId('kpi-spend-delta')).toBeNull();
    expect(screen.queryByTestId('kpi-requests-delta')).toBeNull();
  });

  it('the requests delta uses the count field, independently of the spend delta', () => {
    // Spend up 100% (10000 -> 20000) but request count up only 50%
    // (10 -> 15) — the two badges must not share one computed number.
    const quotaRows = [row(0, 20000, 15), row(10, 10000, 10)];
    render(<KpiCards {...baseProps} quotaRows={quotaRows} />);
    expect(screen.getByTestId('kpi-spend-delta').textContent).toContain('100');
    expect(screen.getByTestId('kpi-requests-delta').textContent).toContain(
      '50',
    );
    expect(screen.getByTestId('kpi-requests-delta').textContent).not.toContain(
      '100',
    );
  });

  it('renders the total-spend and total-requests KPI numbers as all-time totals, unaffected by the trend window', () => {
    render(<KpiCards {...baseProps} quotaRows={[]} />);
    expect(screen.getByText('$0.50')).toBeTruthy();
    expect(screen.getByText('42')).toBeTruthy();
  });

  it('renders "…" for every KPI number while loading, and "—" when there is no me', () => {
    const { rerender } = render(
      <KpiCards {...baseProps} loading me={null} quotaRows={[]} />,
    );
    expect(screen.getAllByText('…').length).toBeGreaterThan(0);

    // remainUSD is null here too — matching what the real caller (Dashboard)
    // computes when `me` is null; a caller-supplied leftover string would
    // not exercise the component's own "—" fallback.
    rerender(
      <KpiCards
        {...baseProps}
        loading={false}
        me={null}
        remainUSD={null}
        quotaRows={[]}
      />,
    );
    expect(screen.getAllByText('—').length).toBeGreaterThan(0);
  });
});

describe('KpiCards — remaining quota renders UsageRing', () => {
  it('renders the UsageRing svg beside the remaining-quota number', () => {
    render(<KpiCards {...baseProps} quotaRows={[]} />);
    // UsageRing renders an svg role="img" with an aria-label built from its
    // label/used/total props — that's the component's own public contract
    // (see components/hifi/UsageRing.jsx), so assert its presence via role
    // rather than reaching into its internals.
    const rings = screen.getAllByRole('img');
    expect(rings.length).toBeGreaterThan(0);
  });

  it('shows the ring alone — no raw quota units under a dollar figure', () => {
    const { container } = render(<KpiCards {...baseProps} quotaRows={[]} />);
    // used_quota/remaining_quota are internal units; the card speaks dollars.
    // A "50k/100.0M"-style caption would put a second, unexplained unit
    // beside the figure.
    expect(container.textContent).not.toMatch(/\d+(\.\d+)?[kM]\//);
  });

  it('does not render UsageRing while loading or with no `me`', () => {
    const { rerender } = render(
      <KpiCards {...baseProps} loading quotaRows={[]} />,
    );
    expect(screen.queryByRole('img')).toBeNull();

    rerender(
      <KpiCards
        {...baseProps}
        loading={false}
        me={null}
        remainUSD={null}
        quotaRows={[]}
      />,
    );
    expect(screen.queryByRole('img')).toBeNull();
  });
});
