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
import React, { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import HFShell from '../../../../components/hifi/HFShell';
import { API } from '../../../../helpers';

/*
 * v2 admin — Model performance analytics.
 *
 * Read-only. Consumes GET /api/v2/admin/analytics/model-performance
 * (per-model request/error counts, token usage, quota, latency avg/p50/p95
 * aggregated from the logs table — see internal/adapter/repo/analytics.go).
 * Root-only: the route sits behind RootJWTAuth, so a 403 renders the same
 * forbidden panel as the other admin surfaces (CostIntelligence convention).
 */

const QUOTA_PER_USD = 500_000;

// Time-range presets: [id, i18n key, fallback label, window seconds]
const RANGES = [
  ['1h', 'range_1h', 'last 1h', 3600],
  ['24h', 'range_24h', 'last 24h', 24 * 3600],
  ['7d', 'range_7d', 'last 7d', 7 * 24 * 3600],
];

// Sortable columns: id → row accessor. `requests` is the default (desc),
// matching the server-side ordering.
const SORTERS = {
  requests: (m) => m.requests,
  error_rate: (m) => m.error_rate,
  avg_latency_ms: (m) => m.avg_latency_ms,
  p50_latency_ms: (m) => m.p50_latency_ms,
  p95_latency_ms: (m) => m.p95_latency_ms,
};

const fmtInt = (n) => Number(n ?? 0).toLocaleString();

const fmtPct = (r) => `${((r ?? 0) * 100).toFixed(2)}%`;

// Latency cells are honest about missing samples: rows aggregated purely
// from error logs (or pre-latency-column rows) carry latency_samples=0 —
// show a dash instead of a misleading 0ms.
const fmtMs = (v, samples) => (samples > 0 ? `${Math.round(v)}ms` : '—');

const usd = (quota) => `$${(quota / QUOTA_PER_USD).toFixed(2)}`;

const HFModelPerformance = () => {
  const { t: tr } = useTranslation();
  const [range, setRange] = useState('24h');
  const [tenantId, setTenantId] = useState('');
  const [tenants, setTenants] = useState([]);
  const [models, setModels] = useState([]);
  const [window_, setWindow_] = useState(null); // {start_time, end_time} actually applied
  const [loading, setLoading] = useState(true);
  const [forbidden, setForbidden] = useState(false);
  const [sortKey, setSortKey] = useState('requests');
  const [sortDir, setSortDir] = useState('desc');

  // Tenant filter options — same admin listing the shell's TenantSwitcher
  // uses. Failure is non-fatal: the filter just stays empty.
  useEffect(() => {
    let cancelled = false;
    API.get('/api/v2/admin/tenants?page_size=100', { skipErrorHandler: true })
      .then((res) => {
        if (cancelled) return;
        const rows = res?.data?.data?.tenants;
        if (Array.isArray(rows)) setTenants(rows);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setForbidden(false);
    const seconds = (RANGES.find((r) => r[0] === range) || RANGES[1])[3];
    const end = Math.floor(Date.now() / 1000);
    const start = end - seconds;
    const params = new URLSearchParams({
      start: String(start),
      end: String(end),
    });
    if (tenantId) params.set('tenant_id', tenantId);
    API.get(`/api/v2/admin/analytics/model-performance?${params}`, {
      skipErrorHandler: true,
    })
      .then((res) => {
        if (cancelled) return;
        if (res?.data?.success) {
          setModels(res.data.data?.models ?? []);
          setWindow_({
            start_time: res.data.data?.start_time ?? start,
            end_time: res.data.data?.end_time ?? end,
          });
        }
      })
      .catch((err) => {
        if (!cancelled && err?.response?.status === 403) setForbidden(true);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [range, tenantId]);

  // ── Summary (top cards) ────────────────────────────────────────────────────

  const summary = useMemo(() => {
    const totalRequests = models.reduce((s, m) => s + (m.requests || 0), 0);
    const totalErrors = models.reduce((s, m) => s + (m.errors || 0), 0);
    // Weighted average latency: weight each model's avg by its latency
    // sample count (NOT by requests — error rows carry no latency).
    const totalSamples = models.reduce(
      (s, m) => s + (m.latency_samples || 0),
      0,
    );
    const weightedLatency =
      totalSamples > 0
        ? models.reduce(
            (s, m) => s + (m.avg_latency_ms || 0) * (m.latency_samples || 0),
            0,
          ) / totalSamples
        : 0;
    return { totalRequests, totalErrors, totalSamples, weightedLatency };
  }, [models]);

  const sorted = useMemo(() => {
    const acc = SORTERS[sortKey] || SORTERS.requests;
    const dir = sortDir === 'asc' ? 1 : -1;
    return [...models].sort((a, b) => (acc(a) - acc(b)) * dir);
  }, [models, sortKey, sortDir]);

  const toggleSort = (key) => {
    if (sortKey === key) {
      setSortDir((d) => (d === 'desc' ? 'asc' : 'desc'));
    } else {
      setSortKey(key);
      setSortDir('desc');
    }
  };

  const sortMark = (key) =>
    sortKey === key ? (sortDir === 'desc' ? ' ▾' : ' ▴') : '';

  // Direct-download CSV of the underlying usage logs for the same window +
  // tenant filter (GET /api/v2/admin/logs/export — cookie-authenticated,
  // same-origin; same navigation pattern as the Log page export).
  const exportCSV = () => {
    const end = window_?.end_time ?? Math.floor(Date.now() / 1000);
    const start =
      window_?.start_time ??
      end - (RANGES.find((r) => r[0] === range) || RANGES[1])[3];
    const params = new URLSearchParams({
      format: 'csv',
      start_time: String(start),
      end_time: String(end),
    });
    if (tenantId) params.set('tenant_id', tenantId);
    window.location.href = `/api/v2/admin/logs/export?${params}`;
  };

  const selectStyle = {
    fontFamily: 'var(--hf-mono)',
    fontSize: 12,
    padding: '5px 8px',
    border: '1px solid var(--hf-rule)',
    background: 'var(--hf-sunken)',
    color: 'var(--hf-ink)',
    borderRadius: 2,
    outline: 'none',
    cursor: 'pointer',
  };

  const thStyle = {
    padding: '6px 10px',
    borderBottom: '1px solid var(--hf-rule)',
    fontFamily: 'var(--hf-mono)',
    fontSize: 12,
    textAlign: 'left',
    color: 'var(--hf-ink-3)',
    fontWeight: 600,
    whiteSpace: 'nowrap',
  };

  const tdStyle = {
    padding: '6px 10px',
    borderBottom: '1px solid var(--hf-rule)',
    fontFamily: 'var(--hf-mono)',
    fontSize: 12,
    textAlign: 'left',
    whiteSpace: 'nowrap',
  };

  const cards = [
    [
      tr('console.admin.analytics.card_requests', 'total requests'),
      fmtInt(summary.totalRequests),
      tr('console.admin.analytics.in_window', 'in window'),
    ],
    [
      tr('console.admin.analytics.card_error_rate', 'total error rate'),
      summary.totalRequests > 0
        ? fmtPct(summary.totalErrors / summary.totalRequests)
        : '—',
      tr('console.admin.analytics.card_errors_sub', '{{count}} errors', {
        count: fmtInt(summary.totalErrors),
      }),
    ],
    [
      tr('console.admin.analytics.card_latency', 'weighted avg latency'),
      summary.totalSamples > 0
        ? `${Math.round(summary.weightedLatency)}ms`
        : '—',
      tr('console.admin.analytics.card_latency_sub', '{{count}} samples', {
        count: fmtInt(summary.totalSamples),
      }),
    ],
  ];

  return (
    <HFShell
      active='admin-analytics'
      crumbs={[
        tr('console.admin.analytics.crumb_admin', 'platform · admin'),
        tr('console.admin.analytics.crumb', 'model performance'),
      ]}
      actions={
        !forbidden && (
          <button
            type='button'
            className='btn'
            data-testid='perf-export-btn'
            onClick={exportCSV}
          >
            📥 {tr('console.admin.analytics.export_csv', 'export CSV')}
          </button>
        )
      }
    >
      <div className='hf-page-head'>
        <div>
          <div className='lbl' style={{ marginBottom: 6 }}>
            {tr('console.admin.analytics.heading_lbl', 'model performance')}
          </div>
          <h1>{tr('console.admin.analytics.title', 'Model performance')}</h1>
          <div className='sub'>
            {tr(
              'console.admin.analytics.sub',
              'requests · errors · tokens · latency percentiles per model',
            )}
          </div>
        </div>
      </div>

      {forbidden ? (
        <div style={{ padding: 24 }}>
          <div className='panel' style={{ padding: '20px 24px' }}>
            <div className='strong' style={{ marginBottom: 6 }}>
              {tr(
                'console.admin.analytics.forbidden_title',
                'Admin access required',
              )}
            </div>
            <div className='muted' style={{ fontSize: 12 }}>
              {tr(
                'console.admin.analytics.forbidden_body',
                'You do not have permission to view model performance analytics.',
              )}
            </div>
          </div>
        </div>
      ) : (
        <div style={{ padding: 24, overflow: 'auto' }}>
          {/* Filters: time-range presets + optional tenant */}
          <div
            style={{
              display: 'flex',
              gap: 8,
              marginBottom: 18,
              alignItems: 'center',
              flexWrap: 'wrap',
            }}
          >
            {RANGES.map(([id, key, fallback]) => (
              <button
                key={id}
                type='button'
                data-testid={`perf-range-${id}`}
                className={'btn sm' + (range === id ? ' primary' : '')}
                onClick={() => setRange(id)}
              >
                {tr(`console.admin.analytics.${key}`, fallback)}
              </button>
            ))}
            <select
              style={selectStyle}
              data-testid='perf-tenant-filter'
              value={tenantId}
              onChange={(e) => setTenantId(e.target.value)}
            >
              <option value=''>
                {tr('console.admin.analytics.all_tenants', 'all tenants')}
              </option>
              {tenants.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.name || t.slug || t.id}
                </option>
              ))}
            </select>
            {loading && (
              <span className='muted mono' style={{ fontSize: 11 }}>
                {tr('console.common.loading', 'loading…')}
              </span>
            )}
          </div>

          {/* Summary cards */}
          <div
            style={{
              display: 'grid',
              gridTemplateColumns: 'repeat(3, 1fr)',
              gap: 16,
              marginBottom: 20,
            }}
          >
            {cards.map(([label, value, sub]) => (
              <div
                key={label}
                className='panel'
                style={{ padding: '16px 20px' }}
              >
                <div className='lbl' style={{ marginBottom: 6 }}>
                  {label}
                </div>
                <div
                  className='mono strong'
                  style={{ fontSize: 26, lineHeight: 1.1 }}
                >
                  {loading ? '…' : value}
                </div>
                <div className='muted' style={{ fontSize: 11, marginTop: 4 }}>
                  {sub}
                </div>
              </div>
            ))}
          </div>

          {/* Per-model table */}
          <div className='panel' style={{ padding: '14px 16px' }}>
            <div className='lbl' style={{ marginBottom: 10 }}>
              {tr('console.admin.analytics.table_lbl', 'per-model breakdown')}
            </div>
            {!loading && sorted.length === 0 ? (
              <div className='muted' style={{ fontSize: 12 }}>
                {tr(
                  'console.admin.analytics.empty',
                  'no traffic in this window',
                )}
              </div>
            ) : (
              <table style={{ width: '100%', borderCollapse: 'collapse' }}>
                <thead>
                  <tr>
                    <th style={thStyle}>
                      {tr('console.admin.analytics.col_model', 'model')}
                    </th>
                    {[
                      [
                        'requests',
                        tr('console.admin.analytics.col_requests', 'requests'),
                      ],
                      [
                        'error_rate',
                        tr(
                          'console.admin.analytics.col_error_rate',
                          'error rate',
                        ),
                      ],
                    ].map(([key, label]) => (
                      <th
                        key={key}
                        style={{ ...thStyle, cursor: 'pointer' }}
                        data-testid={`perf-sort-${key}`}
                        onClick={() => toggleSort(key)}
                      >
                        {label}
                        {sortMark(key)}
                      </th>
                    ))}
                    <th style={thStyle}>
                      {tr('console.admin.analytics.col_tokens', 'tokens p / c')}
                    </th>
                    <th style={thStyle}>
                      {tr('console.admin.analytics.col_spend', 'spend')}
                    </th>
                    {[
                      [
                        'avg_latency_ms',
                        tr('console.admin.analytics.col_avg', 'avg'),
                      ],
                      [
                        'p50_latency_ms',
                        tr('console.admin.analytics.col_p50', 'p50'),
                      ],
                      [
                        'p95_latency_ms',
                        tr('console.admin.analytics.col_p95', 'p95'),
                      ],
                    ].map(([key, label]) => (
                      <th
                        key={key}
                        style={{ ...thStyle, cursor: 'pointer' }}
                        data-testid={`perf-sort-${key}`}
                        onClick={() => toggleSort(key)}
                      >
                        {label}
                        {sortMark(key)}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {sorted.map((m) => (
                    <tr key={m.model_name} data-testid={`perf-row`}>
                      <td style={{ ...tdStyle, fontWeight: 600 }}>
                        {m.model_name}
                      </td>
                      <td style={tdStyle}>{fmtInt(m.requests)}</td>
                      <td
                        style={{
                          ...tdStyle,
                          color:
                            m.error_rate > 0.05
                              ? 'var(--hf-err)'
                              : m.error_rate > 0.01
                                ? 'var(--hf-warn)'
                                : undefined,
                        }}
                      >
                        {fmtPct(m.error_rate)}
                        {m.errors > 0 && (
                          <span className='faint'> ({fmtInt(m.errors)})</span>
                        )}
                      </td>
                      <td style={tdStyle}>
                        {fmtInt(m.prompt_tokens)} /{' '}
                        {fmtInt(m.completion_tokens)}
                      </td>
                      <td style={tdStyle}>{usd(m.quota || 0)}</td>
                      <td style={tdStyle}>
                        {fmtMs(m.avg_latency_ms, m.latency_samples)}
                      </td>
                      <td style={tdStyle}>
                        {fmtMs(m.p50_latency_ms, m.latency_samples)}
                      </td>
                      <td style={tdStyle}>
                        {fmtMs(m.p95_latency_ms, m.latency_samples)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </div>
      )}
    </HFShell>
  );
};

export default HFModelPerformance;
