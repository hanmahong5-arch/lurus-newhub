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

// Activity chart — daily usage stacked by model, after openrouter.ai's
// Activity page: a metric switch (spend / tokens / requests), a range
// switch (7 / 30 days), a legend with totals, and a per-day breakdown on
// hover. Bars are HTML, not a stretched SVG, so labels stay crisp at any
// width. Series maths lives in ./activitySeries.js.

import React, { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { buildActivity, niceCeil, OTHER } from './activitySeries';

const PALETTE = [
  'var(--hf-series-1)',
  'var(--hf-series-2)',
  'var(--hf-series-3)',
  'var(--hf-series-4)',
  'var(--hf-series-5)',
  'var(--hf-series-6)',
];
const OTHER_COLOR = 'var(--hf-series-other)';

const compact = (n) => {
  if (!n) return '0';
  for (const [d, u] of [
    [1e9, 'B'],
    [1e6, 'M'],
    [1e3, 'K'],
  ]) {
    if (n >= d) return `${Number((n / d).toFixed(1))}${u}`;
  }
  return String(Math.round(n));
};

const fmtDay = (ts) =>
  new Date(ts * 1000).toLocaleDateString(undefined, {
    month: 'short',
    day: 'numeric',
  });
const fmtHour = (ts) =>
  new Date(ts * 1000).toLocaleTimeString(undefined, {
    hour: '2-digit',
    minute: '2-digit',
  });

/**
 * @param {object} p
 * @param {Array} p.rows          /api/data/self/ rows
 * @param {number} p.end          window end, unix seconds
 * @param {(quota:number)=>string} p.formatSpend
 * @param {number} [p.maxDays=30] the fetched window; the long range option
 * @param {number} [p.height=180]
 */
const HfActivityChart = ({
  rows,
  end,
  formatSpend,
  maxDays = 30,
  height = 180,
}) => {
  const { t: tr } = useTranslation();
  const [metric, setMetric] = useState('spend');
  const [range, setRange] = useState(maxDays);
  const [hover, setHover] = useState(null);

  // range: a number of days, or 'h24' for the last 24 local hours (the rows
  // are hourly, so this is a re-bucketing of the same fetch).
  const data = useMemo(
    () =>
      buildActivity(
        rows,
        range === 'h24'
          ? { end, hours: 24, metric }
          : { end, days: range, metric },
      ),
    [rows, end, range, metric],
  );
  const fmtBucket = data.unit === 'hour' ? fmtHour : fmtDay;
  const fmt = metric === 'spend' ? formatSpend : compact;
  const top = niceCeil(Math.max(0, ...data.days.map((d) => d.total)));
  const colorOf = (model) => {
    if (model === OTHER) return OTHER_COLOR;
    const i = data.series.findIndex((s) => s.model === model);
    return PALETTE[i % PALETTE.length];
  };
  const label = (model) =>
    model === OTHER ? tr('console.activity.other', 'other') : model;
  const shownDay = hover != null ? data.days[hover] : null;

  const seg = (value, onClick, key, text) => (
    <button
      key={key}
      type='button'
      className={'btn sm' + (value ? ' primary' : '')}
      onClick={onClick}
      data-testid={`activity-${key}`}
    >
      {text}
    </button>
  );

  return (
    <div data-testid='usage-trend-chart'>
      <div
        style={{
          display: 'flex',
          alignItems: 'baseline',
          gap: 10,
          marginBottom: 12,
          flexWrap: 'wrap',
        }}
      >
        <div
          className='display'
          style={{ fontSize: 22 }}
          data-testid='activity-total'
        >
          {fmt(data.total)}
        </div>
        <div className='faint' style={{ fontSize: 12 }}>
          {range === 'h24'
            ? tr('console.activity.last_24h', 'last 24 hours')
            : tr('console.dashboard.usage_trend_window', { days: range })}
        </div>
        <span style={{ flex: 1 }} />
        <div className='hf-seg'>
          {seg(
            metric === 'spend',
            () => setMetric('spend'),
            'metric-spend',
            tr('console.activity.spend', 'spend'),
          )}
          {seg(
            metric === 'tokens',
            () => setMetric('tokens'),
            'metric-tokens',
            tr('console.activity.tokens', 'tokens'),
          )}
          {seg(
            metric === 'requests',
            () => setMetric('requests'),
            'metric-requests',
            tr('console.activity.requests', 'requests'),
          )}
        </div>
        <div className='hf-seg'>
          {seg(range === 'h24', () => setRange('h24'), 'range-24h', '24H')}
          {seg(range === 7, () => setRange(7), 'range-7', '7D')}
          {seg(
            range === maxDays,
            () => setRange(maxDays),
            `range-${maxDays}`,
            `${maxDays}D`,
          )}
        </div>
      </div>

      <div style={{ display: 'flex', gap: 8 }}>
        {/* y axis */}
        <div
          className='mono faint'
          style={{
            height,
            display: 'flex',
            flexDirection: 'column',
            justifyContent: 'space-between',
            fontSize: 10,
            textAlign: 'right',
            minWidth: 44,
          }}
        >
          {top > 0 && (
            <>
              <span>{fmt(top)}</span>
              <span>{fmt(top / 2)}</span>
              <span>0</span>
            </>
          )}
        </div>
        <div style={{ flex: 1, position: 'relative', height }}>
          {[0, 0.5, 1].map((f) => (
            <div
              key={f}
              style={{
                position: 'absolute',
                left: 0,
                right: 0,
                top: `${f * 100}%`,
                borderTop: '1px dashed var(--hf-rule)',
              }}
            />
          ))}
          <div
            style={{
              position: 'absolute',
              inset: 0,
              display: 'flex',
              alignItems: 'flex-end',
              gap: data.days.length > 7 ? 3 : 10,
            }}
            onMouseLeave={() => setHover(null)}
          >
            {data.days.map((d, i) => (
              <div
                key={d.day}
                data-testid='activity-bar'
                data-nonzero={d.total > 0 ? 'true' : 'false'}
                onMouseEnter={() => setHover(i)}
                style={{
                  flex: 1,
                  height: top ? `${(d.total / top) * 100}%` : 0,
                  display: 'flex',
                  flexDirection: 'column-reverse',
                  borderRadius: '3px 3px 0 0',
                  overflow: 'hidden',
                  opacity: hover == null || hover === i ? 1 : 0.45,
                  minHeight: d.total ? 2 : 0,
                }}
                title={`${fmtBucket(d.day)} · ${fmt(d.total)}`}
              >
                {data.series.map((s) =>
                  d.parts[s.model] ? (
                    <div
                      key={s.model}
                      data-model={s.model}
                      style={{
                        flex: d.parts[s.model],
                        background: colorOf(s.model),
                      }}
                    />
                  ) : null,
                )}
              </div>
            ))}
          </div>
          {top === 0 && (
            <div
              className='faint'
              data-testid='activity-empty'
              style={{
                position: 'absolute',
                inset: 0,
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                fontSize: 12,
              }}
            >
              {tr('console.activity.empty', 'No usage in this window.')}
            </div>
          )}
          {shownDay && shownDay.total > 0 && (
            <div
              className='panel'
              data-testid='activity-tooltip'
              style={{
                position: 'absolute',
                top: 4,
                [hover > data.days.length / 2 ? 'left' : 'right']: 4,
                padding: '8px 10px',
                fontSize: 12,
                minWidth: 180,
                boxShadow: '0 6px 18px rgba(10, 9, 8, 0.10)',
                pointerEvents: 'none',
              }}
            >
              <div className='strong' style={{ marginBottom: 4 }}>
                {fmtBucket(shownDay.day)} · {fmt(shownDay.total)}
              </div>
              {data.series
                .filter((s) => shownDay.parts[s.model])
                .map((s) => (
                  <div
                    key={s.model}
                    style={{ display: 'flex', gap: 6, alignItems: 'center' }}
                  >
                    <i
                      style={{
                        width: 8,
                        height: 8,
                        borderRadius: 2,
                        background: colorOf(s.model),
                      }}
                    />
                    <span className='truncate' style={{ flex: 1 }}>
                      {label(s.model)}
                    </span>
                    <span className='mono'>{fmt(shownDay.parts[s.model])}</span>
                  </div>
                ))}
            </div>
          )}
        </div>
      </div>
      <div
        className='mono faint'
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          fontSize: 10,
          marginTop: 6,
          paddingLeft: 52,
        }}
      >
        <span>{data.days.length ? fmtBucket(data.days[0].day) : ''}</span>
        <span>
          {data.days.length
            ? fmtBucket(data.days[data.days.length - 1].day)
            : ''}
        </span>
      </div>

      <div
        data-testid='activity-legend'
        style={{
          display: 'flex',
          flexWrap: 'wrap',
          gap: '6px 16px',
          marginTop: 12,
          fontSize: 12,
        }}
      >
        {data.series.map((s) => (
          <span
            key={s.model}
            style={{ display: 'inline-flex', gap: 6, alignItems: 'center' }}
          >
            <i
              style={{
                width: 10,
                height: 10,
                borderRadius: 2,
                background: colorOf(s.model),
              }}
            />
            <span>{label(s.model)}</span>
            <span className='mono faint'>{fmt(s.total)}</span>
          </span>
        ))}
      </div>
    </div>
  );
};

export default HfActivityChart;
