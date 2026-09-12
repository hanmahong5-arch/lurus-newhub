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
import HFShell from '../../../components/hifi/HFShell';
import { API } from '../../../helpers';
import { getQuotaPerUSD } from '../../../helpers/formatting';
import { useTenantSlug } from '../../../hooks/common/useTenantSlug';

/*
 * v2 tenant-admin — Model & vendor performance rankings leaderboard.
 *
 * Read-only. Consumes GET /api/v2/:tenant_slug/analytics/rankings
 * (period-over-period rank/trend/share per model or per channel-type
 * vendor — see internal/adapter/repo/analytics.go GetRankings). The
 * tenant-admin gate lives server-side (requireTenantAdmin inside
 * GetTenantRankingsV2); a 403 renders the same forbidden panel the
 * ModelPerformance admin page uses for its own root-only gate.
 */

// hours presets must match the backend's snap targets
// (rankingsHourPresets in v2_analytics_rankings.go) so the UI never asks
// for a window the server silently rounds away from.
const HOUR_PRESETS = [
  [1, 'hours_1', 'last 1h'],
  [6, 'hours_6', 'last 6h'],
  [24, 'hours_24', 'last 24h'],
  [168, 'hours_168', 'last 7d'],
  [720, 'hours_720', 'last 30d'],
];

const fmtInt = (n) => Number(n ?? 0).toLocaleString();

const fmtPct = (v) => `${Number(v ?? 0).toFixed(1)}%`;

const usd = (quota) => `$${(quota / getQuotaPerUSD()).toFixed(2)}`;

// Trend cell: is_new gets a distinct badge (there is no previous-window
// baseline to compare against); otherwise show the signed rank delta.
const Trend = ({ row }) => {
  if (row.is_new) {
    return <span className='muted'>new</span>;
  }
  const d = row.rank_delta ?? 0;
  if (d > 0)
    return <span style={{ color: 'var(--hf-positive, #2a9d5c)' }}>▲{d}</span>;
  if (d < 0)
    return <span style={{ color: 'var(--hf-negative, #d64545)' }}>▼{-d}</span>;
  return <span className='muted'>—</span>;
};

const HFRankings = () => {
  const { t: tr } = useTranslation();
  const tenantSlug = useTenantSlug();
  const [by, setBy] = useState('model');
  const [hours, setHours] = useState(24);
  const [rows, setRows] = useState([]);
  const [cachedAt, setCachedAt] = useState(null);
  const [loading, setLoading] = useState(true);
  const [forbidden, setForbidden] = useState(false);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setForbidden(false);
    const params = new URLSearchParams({ by, hours: String(hours) });
    API.get(`/api/v2/${tenantSlug}/analytics/rankings?${params}`, {
      skipErrorHandler: true,
    })
      .then((res) => {
        if (cancelled) return;
        if (res?.data?.success) {
          setRows(res.data.data?.rows ?? []);
          setCachedAt(res.data.data?.cached_at ?? null);
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
  }, [tenantSlug, by, hours]);

  const totalTokens = useMemo(
    () => rows.reduce((s, r) => s + (r.total_tokens || 0), 0),
    [rows],
  );

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

  return (
    <HFShell
      active='admin-rankings'
      crumbs={[
        tr('console.rankings.crumb_admin', 'operations & insights'),
        tr('console.rankings.crumb', 'rankings'),
      ]}
    >
      <div className='hf-page-head'>
        <div>
          <div className='lbl' style={{ marginBottom: 6 }}>
            {tr('console.rankings.heading_lbl', 'rankings')}
          </div>
          <h1>{tr('console.rankings.title', 'Rankings')}</h1>
          <div className='sub'>
            {tr(
              'console.rankings.sub',
              'rank · trend · share vs the previous window, per model or per vendor',
            )}
          </div>
        </div>
      </div>

      {forbidden ? (
        <div style={{ padding: 24 }}>
          <div className='panel' style={{ padding: '20px 24px' }}>
            <div className='strong' style={{ marginBottom: 6 }}>
              {tr('console.rankings.forbidden_title', 'Admin access required')}
            </div>
            <div className='muted' style={{ fontSize: 12 }}>
              {tr(
                'console.rankings.forbidden_body',
                'You do not have permission to view rankings.',
              )}
            </div>
          </div>
        </div>
      ) : (
        <div style={{ padding: 24, overflow: 'auto' }}>
          <div
            style={{
              display: 'flex',
              gap: 8,
              marginBottom: 12,
              alignItems: 'center',
              flexWrap: 'wrap',
            }}
          >
            <button
              type='button'
              data-testid='rankings-by-model'
              className={'btn sm' + (by === 'model' ? ' primary' : '')}
              onClick={() => setBy('model')}
            >
              {tr('console.rankings.by_model', 'by model')}
            </button>
            <button
              type='button'
              data-testid='rankings-by-vendor'
              className={'btn sm' + (by === 'vendor' ? ' primary' : '')}
              onClick={() => setBy('vendor')}
            >
              {tr('console.rankings.by_vendor', 'by vendor')}
            </button>
            {HOUR_PRESETS.map(([h, key, fallback]) => (
              <button
                key={h}
                type='button'
                data-testid={`rankings-hours-${h}`}
                className={'btn sm' + (hours === h ? ' primary' : '')}
                onClick={() => setHours(h)}
              >
                {tr(`console.rankings.${key}`, fallback)}
              </button>
            ))}
          </div>

          {cachedAt ? (
            <div className='muted' style={{ fontSize: 11, marginBottom: 10 }}>
              {tr('console.rankings.cached_at', 'cached at')}{' '}
              {new Date(cachedAt * 1000).toLocaleTimeString()}
            </div>
          ) : null}

          {loading ? (
            <div className='muted' data-testid='rankings-loading'>
              {tr('console.common.loading', 'loading…')}
            </div>
          ) : rows.length === 0 ? (
            <div className='muted' data-testid='rankings-empty'>
              {tr('console.common.no_data', 'no data')}
            </div>
          ) : (
            <table
              className='hf-table'
              style={{ width: '100%', borderCollapse: 'collapse' }}
              data-testid='rankings-table'
            >
              <thead>
                <tr>
                  <th style={thStyle}>
                    {tr('console.rankings.col_rank', 'rank')}
                  </th>
                  <th style={thStyle}>
                    {tr('console.rankings.col_name', 'name')}
                  </th>
                  <th style={thStyle}>
                    {tr('console.rankings.col_trend', 'trend')}
                  </th>
                  <th style={thStyle}>
                    {tr('console.rankings.col_requests', 'requests')}
                  </th>
                  <th style={thStyle}>
                    {tr('console.rankings.col_growth', 'growth')}
                  </th>
                  <th style={thStyle}>
                    {tr('console.rankings.col_tokens', 'tokens')}
                  </th>
                  <th style={thStyle}>
                    {tr('console.rankings.col_token_share', 'token share')}
                  </th>
                  <th style={thStyle}>
                    {tr('console.rankings.col_quota', 'spend')}
                  </th>
                  <th style={thStyle}>
                    {tr('console.rankings.col_quota_share', 'spend share')}
                  </th>
                </tr>
              </thead>
              <tbody>
                {rows.map((r) => (
                  <tr key={r.name} data-testid='rankings-row'>
                    <td style={tdStyle}>{r.rank}</td>
                    <td style={tdStyle}>{r.name}</td>
                    <td style={tdStyle}>
                      <Trend row={r} />
                    </td>
                    <td style={tdStyle}>{fmtInt(r.requests)}</td>
                    <td style={tdStyle}>
                      {r.requests_growth_pct == null
                        ? '—'
                        : `${r.requests_growth_pct >= 0 ? '+' : ''}${r.requests_growth_pct.toFixed(1)}%`}
                    </td>
                    <td style={tdStyle}>{fmtInt(r.total_tokens)}</td>
                    <td style={tdStyle}>{fmtPct(r.token_share_pct)}</td>
                    <td style={tdStyle}>{usd(r.quota)}</td>
                    <td style={tdStyle}>{fmtPct(r.quota_share_pct)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          {rows.length > 0 && (
            <div className='muted' style={{ fontSize: 11, marginTop: 10 }}>
              {tr(
                'console.rankings.total_tokens_in_window',
                'total tokens in window',
              )}
              : {fmtInt(totalTokens)}
            </div>
          )}
        </div>
      )}
    </HFShell>
  );
};

export default HFRankings;
