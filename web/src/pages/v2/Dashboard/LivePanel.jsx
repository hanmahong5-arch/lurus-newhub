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

// The QPS / latency / error-rate realtime tiles, extracted from
// Dashboard/index.jsx (cycle-19) into one titled section. Previously these
// were three always-visible `.panel` cards that showed a row of "—" the
// moment an account had no traffic in the last 5 minutes — the same weight
// on screen as the always-real KPI strip above it, for a state that is
// usually just "nothing happened recently". When there is genuinely nothing
// to show (settled, not still loading) the section folds to a single line;
// with real data it renders the three cards as before.

import React from 'react';
import { useTranslation } from 'react-i18next';
import { KpiCaption, captionText } from './LoadErrorPanel';
import { formatQPS, formatLatencyMs, formatErrorRate } from './kpis';

/**
 * @param {object} p
 * @param {boolean} p.loading
 * @param {'ok'|'forbidden'|'unauthenticated'|'error'|null} p.logsStatus
 * @param {boolean} p.hasRealtimeData   logs.length > 0 for the 5-min window
 * @param {number} p.qps
 * @param {number|null} p.p50
 * @param {number|null} p.p95
 * @param {number|null} p.p99
 * @param {number} p.errorRate
 */
const LivePanel = ({
  loading,
  logsStatus,
  hasRealtimeData,
  qps,
  p50,
  p95,
  p99,
  errorRate,
}) => {
  const { t } = useTranslation();
  const unableText = t('console.dashboard.load_failed_short', 'unable to load');
  const pendingText = t('console.common.loading', 'loading…');
  const settledCaption = (okText) =>
    captionText(logsStatus, okText, unableText, pendingText);

  // Folds only once the fetch has actually settled with nothing to show —
  // while still loading (first mount, or a refresh over previously-real
  // data), the three cards stay up so the reader sees "…"/"loading…"
  // instead of the section flickering shut and back open.
  const folded = !loading && !hasRealtimeData;

  return (
    <div
      className='panel'
      data-testid='live-panel'
      style={{ gridColumn: 'span 12', padding: 18 }}
    >
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 8,
          marginBottom: folded ? 0 : 12,
        }}
      >
        <span className='live-dot' />
        <div className='lbl'>{t('console.dashboard.live_title')}</div>
      </div>

      {folded ? (
        <div
          className='muted'
          data-testid='live-panel-folded'
          style={{ fontSize: 12 }}
        >
          {settledCaption(t('console.dashboard.qps_idle'))}
        </div>
      ) : (
        <div
          style={{
            display: 'grid',
            gridTemplateColumns: 'repeat(3, 1fr)',
            gap: 16,
          }}
        >
          {/* ── QPS ── */}
          <div className='panel-sunken' style={{ padding: 14 }}>
            <div className='lbl'>{t('console.dashboard.qps')}</div>
            <div
              className='display'
              style={{
                fontSize: 32,
                marginTop: 4,
                color: hasRealtimeData ? 'var(--hf-accent)' : 'var(--hf-ink-3)',
              }}
            >
              {loading ? '…' : hasRealtimeData ? formatQPS(qps) : '—'}
            </div>
            <KpiCaption>
              {hasRealtimeData
                ? t('console.dashboard.qps_active')
                : settledCaption(t('console.dashboard.qps_idle'))}
            </KpiCaption>
          </div>

          {/* ── Latency P50/P95/P99 (P99 anchors the SLO) ── */}
          <div className='panel-sunken' style={{ padding: 14 }}>
            <div className='lbl'>{t('console.dashboard.latency_ms')}</div>
            <div
              style={{
                marginTop: 6,
                display: 'grid',
                gridTemplateColumns: '1fr 1fr 1fr',
                gap: 8,
                alignItems: 'baseline',
              }}
            >
              {[
                ['p50', p50, 'var(--hf-ok)'],
                ['p95', p95, 'var(--hf-warn)'],
                ['p99', p99, 'var(--hf-err)'],
              ].map(([label, val, color]) => (
                <div key={label}>
                  <div
                    className='display'
                    style={{
                      fontSize: 22,
                      color: val != null ? color : 'var(--hf-ink-3)',
                    }}
                  >
                    {loading ? '…' : val != null ? formatLatencyMs(val) : '—'}
                  </div>
                  <div
                    className='mono'
                    style={{
                      fontSize: 9,
                      color: 'var(--hf-ink-3)',
                      marginTop: 2,
                    }}
                  >
                    {label}
                  </div>
                </div>
              ))}
            </div>
            <KpiCaption>
              {p99 != null
                ? t('console.dashboard.latency_active')
                : settledCaption(t('console.dashboard.latency_idle'))}
            </KpiCaption>
          </div>

          {/* ── Error rate (derived from log type 5 share) ── */}
          <div className='panel-sunken' style={{ padding: 14 }}>
            <div className='lbl'>{t('console.dashboard.error_rate')}</div>
            <div
              className='display'
              style={{
                fontSize: 32,
                marginTop: 4,
                color: !hasRealtimeData
                  ? 'var(--hf-ink-3)'
                  : errorRate > 0.05
                    ? 'var(--hf-err)'
                    : 'var(--hf-ok)',
              }}
            >
              {loading
                ? '…'
                : hasRealtimeData
                  ? formatErrorRate(errorRate)
                  : '—'}
            </div>
            <KpiCaption>
              {hasRealtimeData
                ? t('console.dashboard.error_rate_active')
                : settledCaption(t('console.dashboard.qps_idle'))}
            </KpiCaption>
          </div>
        </div>
      )}
    </div>
  );
};

export default LivePanel;
