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
import { useTranslation } from 'react-i18next';

/*
 * Billing spend trend — 6 fixed calendar-month slots ending at the current
 * month, aligned to invoice.month ('YYYY-MM'). Before this, the bar row was
 * `invoices.slice(0, 6)` with each bar at `flex: 1`: a tenant with a single
 * invoice got one bar stretched across the whole row, which read as a broken
 * chart rather than "5 months with no invoice yet". Missing months now
 * render as an honest empty slot (a baseline only, no fabricated bar), and
 * every slot keeps a fixed 1/6-width column regardless of how many carry
 * data.
 */

const SLOT_COUNT = 6;

// Integer Date arithmetic only (getFullYear/getMonth) — no toLocale*String
// or toISOString, which pages/v2/time_single_source.test.js forbids under
// pages/v2. `now` is injectable so the cross-year-boundary case (January
// walking back into August of the previous year) is deterministic in tests.
export function buildTrendSlots(invoices, now = new Date()) {
  const byMonth = new Map((invoices ?? []).map((inv) => [inv.month, inv]));
  let year = now.getFullYear();
  let month = now.getMonth(); // 0-11
  const keys = [];
  for (let i = 0; i < SLOT_COUNT; i++) {
    const mm = String(month + 1).padStart(2, '0');
    keys.unshift(`${year}-${mm}`);
    month -= 1;
    if (month < 0) {
      month = 11;
      year -= 1;
    }
  }
  return keys.map((key) => ({ key, invoice: byMonth.get(key) ?? null }));
}

const TrendBars = ({ invoices, formatCNY, now }) => {
  const { t: tr } = useTranslation();
  const slots = buildTrendSlots(invoices, now);
  const maxAmount = slots.reduce(
    (m, s) => Math.max(m, s.invoice?.amount_cny ?? 0),
    0,
  );

  return (
    <div data-testid='billing-trend-bars'>
      <div
        className='faint mono hf-tnum'
        style={{ fontSize: 9, marginBottom: 4, minHeight: 11 }}
        data-testid='billing-trend-scale-max'
      >
        {maxAmount > 0 ? formatCNY(maxAmount) : ''}
      </div>
      <div
        style={{
          display: 'flex',
          alignItems: 'flex-end',
          gap: 0,
          height: 100,
        }}
      >
        {slots.map((s, i) => {
          const amount = s.invoice?.amount_cny ?? 0;
          const isLast = i === slots.length - 1;
          return (
            <div
              key={s.key}
              data-testid='billing-trend-slot'
              data-filled={s.invoice ? 'true' : 'false'}
              style={{
                width: `${100 / SLOT_COUNT}%`,
                textAlign: 'center',
                padding: '0 4px',
                boxSizing: 'border-box',
              }}
              title={
                s.invoice
                  ? undefined
                  : tr('console.billing.trend_no_invoice', 'no invoice')
              }
            >
              {s.invoice ? (
                <>
                  <div
                    className='hf-tnum'
                    style={{ fontSize: 9, marginBottom: 3 }}
                    data-testid='billing-trend-amount'
                  >
                    {formatCNY(amount)}
                  </div>
                  <div
                    data-testid='billing-trend-bar'
                    style={{
                      height:
                        (maxAmount > 0 ? amount / maxAmount : 0) * 60 + 'px',
                      minHeight: 2,
                      background: isLast
                        ? 'var(--hf-accent)'
                        : 'var(--hf-ink-2)',
                      opacity: isLast ? 1 : 0.6,
                    }}
                  />
                </>
              ) : (
                <div
                  data-testid='billing-trend-bar-empty'
                  style={{
                    height: 1,
                    marginTop: 74,
                    background: 'var(--hf-rule)',
                  }}
                />
              )}
              <div className='faint mono' style={{ fontSize: 9, marginTop: 4 }}>
                {s.key.slice(5)}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
};

export default TrendBars;
