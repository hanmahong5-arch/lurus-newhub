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

// The four always-on KPI cards (total spend / remaining quota / total
// requests / active tokens), extracted from Dashboard/index.jsx (cycle-19)
// so the page's own file stays under its size ceiling. Two of the four get
// a 7-day sparkline + signed 环比 (period-over-period delta) sourced from
// the same /api/data/self/ rows HfActivityChart already fetches — the KPI
// number itself stays the all-time total exactly as before, the sparkline
// is a trend annotation underneath it, not a replacement for it.

import React from 'react';
import { useTranslation } from 'react-i18next';
import HfSparkline from '../../../components/hifi/HfSparkline';
import UsageRing from '../../../components/hifi/UsageRing';
import { dailyTotals, percentDelta } from './kpis';

const TREND_DAYS = 14;
const DELTA_DAYS = 7;

export const KpiFootNote = ({ children }) => (
  <div
    style={{
      display: 'flex',
      justifyContent: 'space-between',
      alignItems: 'flex-end',
      marginTop: 8,
    }}
  >
    <span className='mono muted' style={{ fontSize: 10 }}>
      {children}
    </span>
  </div>
);

// Last DELTA_DAYS vs. the DELTA_DAYS immediately before that, sliced out of
// a TREND_DAYS-long daily series. `series` (exactly DELTA_DAYS long) feeds
// the sparkline; `pct` is percentDelta()'s signed percentage, or null when
// the prior period is 0 — never a fabricated 0%/∞%, per the no-fabrication
// rule this page holds everywhere else.
const computeTrend = (rows, end, field, ready) => {
  if (!ready) return { series: [], pct: null };
  const daily = dailyTotals(rows, { end, days: TREND_DAYS, field });
  const series = daily.slice(-DELTA_DAYS);
  const prior = daily.slice(-DELTA_DAYS * 2, -DELTA_DAYS);
  const sum = (arr) => arr.reduce((s, v) => s + v, 0);
  return { series, pct: percentDelta(sum(series), sum(prior)) };
};

const TrendBadge = ({ pct, positiveColor, testId, deltaTitle }) => {
  if (pct == null) return null;
  const up = pct >= 0;
  return (
    <span
      className='mono hf-tnum'
      data-testid={testId}
      title={deltaTitle}
      style={{
        fontSize: 11,
        color: up ? positiveColor : 'var(--hf-ink-3)',
      }}
    >
      {up ? '↑' : '↓'} {up ? '+' : ''}
      {pct.toFixed(0)}%
    </span>
  );
};

/**
 * @param {object} p
 * @param {boolean} p.loading
 * @param {object|null} p.me            /api/v2/:slug/user/me response
 * @param {number|null} p.spendUSD      me.used_quota converted to USD
 * @param {string|null} p.remainUSD     formatted remaining quota ("$x.xx" / "∞")
 * @param {Array} p.quotaRows           /api/data/self/ rows (up to 29 days)
 * @param {{start:number,end:number}} p.quotaWindow
 * @param {boolean} p.quotaLoaded       true once the /api/data/self/ fetch settled
 */
const KpiCards = ({
  loading,
  me,
  spendUSD,
  remainUSD,
  quotaRows,
  quotaWindow,
  quotaLoaded,
}) => {
  const { t } = useTranslation();
  const end = quotaWindow?.end;

  const spendTrend = computeTrend(quotaRows, end, 'quota', quotaLoaded);
  const requestTrend = computeTrend(quotaRows, end, 'count', quotaLoaded);
  const deltaTitle = t('console.dashboard.delta_vs_prev', {
    days: DELTA_DAYS,
  });

  const remainTotal =
    me && me.remaining_quota != null && me.remaining_quota >= 0
      ? (me.used_quota ?? 0) + me.remaining_quota
      : 0;

  return (
    <>
      {/* ── KPI: Total spend (real) ── */}
      <div className='panel' style={{ gridColumn: 'span 3', padding: 18 }}>
        <div className='lbl'>{t('console.dashboard.total_spend')}</div>
        <div className='display' style={{ fontSize: 32, marginTop: 4 }}>
          {loading ? '…' : me ? `$${spendUSD.toFixed(2)}` : '—'}
        </div>
        {spendTrend.series.some((v) => v > 0) && (
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 6,
              marginTop: 6,
            }}
          >
            <HfSparkline points={spendTrend.series} />
            <TrendBadge
              pct={spendTrend.pct}
              // Rising spend is not a bad thing — neutral ink, not the red
              // "error rate" color other panels use for "up is worse".
              positiveColor='var(--hf-ink-2)'
              testId='kpi-spend-delta'
              deltaTitle={deltaTitle}
            />
          </div>
        )}
        <KpiFootNote>{t('console.dashboard.all_time_quota')}</KpiFootNote>
      </div>

      {/* ── KPI: Remaining quota (real) ── */}
      <div className='panel' style={{ gridColumn: 'span 3', padding: 18 }}>
        <div className='lbl'>{t('console.dashboard.remaining_quota')}</div>
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 12,
            marginTop: 4,
          }}
        >
          <div className='display' style={{ fontSize: 32 }}>
            {loading ? '…' : (remainUSD ?? '—')}
          </div>
          {!loading && me && (
            <UsageRing
              used={me.used_quota ?? 0}
              total={remainTotal}
              label={t('console.dashboard.remaining_quota')}
              size={44}
              compact
            />
          )}
        </div>
        <KpiFootNote>
          {me && me.remaining_quota >= 0
            ? t('console.dashboard.until_topup')
            : t('console.dashboard.unlimited_plan')}
        </KpiFootNote>
      </div>

      {/* ── KPI: Total requests (real) ── */}
      <div className='panel' style={{ gridColumn: 'span 3', padding: 18 }}>
        <div className='lbl'>{t('console.dashboard.total_requests')}</div>
        <div className='display' style={{ fontSize: 32, marginTop: 4 }}>
          {loading ? '…' : me ? (me.request_count ?? 0).toLocaleString() : '—'}
        </div>
        {requestTrend.series.some((v) => v > 0) && (
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 6,
              marginTop: 6,
            }}
          >
            <HfSparkline points={requestTrend.series} />
            <TrendBadge
              pct={requestTrend.pct}
              // More requests is a good thing — the ok/green tone.
              positiveColor='var(--hf-ok)'
              testId='kpi-requests-delta'
              deltaTitle={deltaTitle}
            />
          </div>
        )}
        <KpiFootNote>{t('console.dashboard.all_time')}</KpiFootNote>
      </div>

      {/* ── KPI: Active tokens (real) ── */}
      <div className='panel' style={{ gridColumn: 'span 3', padding: 18 }}>
        <div className='lbl'>{t('console.dashboard.active_tokens')}</div>
        <div className='display' style={{ fontSize: 32, marginTop: 4 }}>
          {loading ? '…' : me ? (me.token_count ?? 0) : '—'}
        </div>
        <KpiFootNote>{t('console.dashboard.in_workspace')}</KpiFootNote>
      </div>
    </>
  );
};

export default KpiCards;
