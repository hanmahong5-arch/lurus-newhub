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

/**
 * HfUsageTrendChart — dense SVG bar chart for a per-day quota series.
 *
 * `days` must already be a fully-populated, chronologically sorted series
 * (one entry per LOCAL calendar day in the requested window, `quota: 0` for
 * days with no rows, `day` = that local day's midnight as unix seconds) — the
 * caller (Dashboard) is responsible for that densification from the sparse
 * `/api/data/self/` rows, this component only draws it. `day` must be local
 * midnight, not UTC midnight, because the label below is rendered with
 * `toLocaleDateString` (local) — a UTC-keyed `day` would render a correct
 * label for the wrong bucket boundary.
 *
 * Each bar carries `data-nonzero` so a test can assert a real value rendered
 * without depending on computed pixel geometry (jsdom does not lay out SVG).
 *
 * Props:
 *   days         {Array<{day:number, quota:number}>} — day is unix seconds
 *   formatValue  {(quota:number) => string} — value formatter for the bar title
 *   height       {number} — SVG viewBox height in px (default 96)
 */
const HfUsageTrendChart = ({ days = [], formatValue, height = 96 }) => {
  const { t: tr } = useTranslation();
  const max = days.reduce((m, d) => Math.max(m, d.quota || 0), 0);
  const barSlot = days.length > 0 ? 100 / days.length : 0;
  const baseline = height - 14;
  const fmtDay = (ts) => {
    try {
      return new Date(ts * 1000).toLocaleDateString(undefined, {
        month: 'short',
        day: 'numeric',
      });
    } catch (_) {
      return '';
    }
  };
  const fmtVal = typeof formatValue === 'function' ? formatValue : String;

  return (
    <div data-testid='usage-trend-chart'>
      <svg
        viewBox={`0 0 100 ${height}`}
        preserveAspectRatio='none'
        width='100%'
        height={height}
        role='img'
        aria-label={tr(
          'console.dashboard.usage_trend_aria',
          'daily usage over the selected window',
        )}
      >
        {days.map((d, i) => {
          const raw = d.quota || 0;
          const h = max > 0 ? (raw / max) * baseline : 0;
          return (
            <rect
              key={d.day}
              data-testid={`trend-bar-${i}`}
              data-nonzero={raw > 0 ? 'true' : 'false'}
              x={i * barSlot + barSlot * 0.15}
              y={baseline - h}
              width={Math.max(barSlot * 0.7, 0.4)}
              height={Math.max(h, raw > 0 ? 1 : 0)}
              fill={raw > 0 ? 'var(--hf-accent)' : 'var(--hf-rule)'}
            >
              <title>{`${fmtDay(d.day)} · ${fmtVal(raw)}`}</title>
            </rect>
          );
        })}
      </svg>
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          marginTop: 4,
        }}
      >
        <span className='mono muted' style={{ fontSize: 9 }}>
          {days.length > 0 ? fmtDay(days[0].day) : ''}
        </span>
        <span className='mono muted' style={{ fontSize: 9 }}>
          {days.length > 0 ? fmtDay(days[days.length - 1].day) : ''}
        </span>
      </div>
    </div>
  );
};

export default HfUsageTrendChart;
