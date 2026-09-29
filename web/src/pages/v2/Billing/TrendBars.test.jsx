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
import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';

// Mirror i18next's en behaviour: return the English defaultValue (2nd arg).
vi.mock('react-i18next', () => ({
  initReactI18next: { type: '3rdParty', init: () => {} },
  useTranslation: () => ({
    t: (key, fallback) => (typeof fallback === 'string' ? fallback : key),
  }),
}));

import TrendBars, { buildTrendSlots } from './TrendBars';

const fmtCNY = (v) => `¥${v.toFixed(2)}`;

describe('buildTrendSlots', () => {
  it('builds 6 trailing YYYY-MM keys ending at `now`, in chronological order', () => {
    const now = new Date(2026, 8, 29); // 2026-09-29 (month is 0-indexed)
    const slots = buildTrendSlots([], now);
    expect(slots.map((s) => s.key)).toEqual([
      '2026-04',
      '2026-05',
      '2026-06',
      '2026-07',
      '2026-08',
      '2026-09',
    ]);
  });

  // January must walk back across the year boundary into August of the
  // previous year — the bug class this integer getFullYear()/getMonth()
  // arithmetic (not toLocale*/string parsing) exists to avoid.
  it('crosses the year boundary correctly when `now` is January', () => {
    const now = new Date(2027, 0, 15); // 2027-01-15
    const slots = buildTrendSlots([], now);
    expect(slots.map((s) => s.key)).toEqual([
      '2026-08',
      '2026-09',
      '2026-10',
      '2026-11',
      '2026-12',
      '2027-01',
    ]);
  });

  it('aligns invoices onto their matching month key and leaves the rest null', () => {
    const now = new Date(2026, 8, 29);
    const invoices = [
      { month: '2026-09', amount_cny: 16.84 },
      { month: '2026-08', amount_cny: 19.68 },
    ];
    const slots = buildTrendSlots(invoices, now);
    const byKey = Object.fromEntries(slots.map((s) => [s.key, s.invoice]));
    expect(byKey['2026-09'].amount_cny).toBe(16.84);
    expect(byKey['2026-08'].amount_cny).toBe(19.68);
    expect(byKey['2026-07']).toBeNull();
    expect(byKey['2026-04']).toBeNull();
  });
});

describe('TrendBars', () => {
  const now = new Date(2026, 8, 29);

  it('renders 6 slots total; a single invoice produces 1 filled bar and 5 empty slots', () => {
    render(
      <TrendBars
        invoices={[{ month: '2026-09', amount_cny: 16.84 }]}
        formatCNY={fmtCNY}
        now={now}
      />,
    );
    const slots = screen.getAllByTestId('billing-trend-slot');
    expect(slots).toHaveLength(6);
    const filled = slots.filter((s) => s.dataset.filled === 'true');
    const empty = slots.filter((s) => s.dataset.filled === 'false');
    expect(filled).toHaveLength(1);
    expect(empty).toHaveLength(5);
    expect(screen.getAllByTestId('billing-trend-bar')).toHaveLength(1);
    expect(screen.getAllByTestId('billing-trend-bar-empty')).toHaveLength(5);
  });

  it('renders 6 bars, in ascending chronological order, when 6 invoices are present', () => {
    const invoices = [
      { month: '2026-04', amount_cny: 1 },
      { month: '2026-05', amount_cny: 2 },
      { month: '2026-06', amount_cny: 3 },
      { month: '2026-07', amount_cny: 4 },
      { month: '2026-08', amount_cny: 5 },
      { month: '2026-09', amount_cny: 6 },
    ];
    render(<TrendBars invoices={invoices} formatCNY={fmtCNY} now={now} />);
    const slots = screen.getAllByTestId('billing-trend-slot');
    expect(slots).toHaveLength(6);
    expect(slots.every((s) => s.dataset.filled === 'true')).toBe(true);
    expect(screen.getAllByTestId('billing-trend-bar')).toHaveLength(6);
    // month labels render left→right in ascending order.
    const labels = slots.map((s) => s.textContent);
    expect(labels[0]).toContain('04');
    expect(labels[5]).toContain('09');
  });

  it('renders an amount label per filled bar, using the given formatter', () => {
    render(
      <TrendBars
        invoices={[{ month: '2026-09', amount_cny: 16.84 }]}
        formatCNY={fmtCNY}
        now={now}
      />,
    );
    expect(screen.getByTestId('billing-trend-amount').textContent).toBe(
      '¥16.84',
    );
  });

  it('gives an empty slot a "no invoice" title, not a fabricated bar', () => {
    render(
      <TrendBars
        invoices={[{ month: '2026-09', amount_cny: 16.84 }]}
        formatCNY={fmtCNY}
        now={now}
      />,
    );
    const slots = screen.getAllByTestId('billing-trend-slot');
    const emptySlot = slots.find((s) => s.dataset.filled === 'false');
    expect(emptySlot.title).toBe('no invoice');
    expect(
      emptySlot.querySelector('[data-testid="billing-trend-bar"]'),
    ).toBeNull();
  });

  it('keeps each slot a fixed 1/6 width regardless of how many carry data', () => {
    render(
      <TrendBars
        invoices={[{ month: '2026-09', amount_cny: 16.84 }]}
        formatCNY={fmtCNY}
        now={now}
      />,
    );
    const slots = screen.getAllByTestId('billing-trend-slot');
    for (const s of slots) {
      expect(s.style.width).toBe(`${100 / 6}%`);
    }
  });
});
