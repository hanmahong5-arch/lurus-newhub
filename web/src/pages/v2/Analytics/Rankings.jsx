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
import React, { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import HFShell from '../../../components/hifi/HFShell';
import { API, isRoot } from '../../../helpers';
import { getQuotaPerUSD } from '../../../helpers/formatting';
import { useTenantSlug } from '../../../hooks/common/useTenantSlug';
import HfVendorIcon from '../../../components/hifi/HfVendorIcon';

/*
 * v2 tenant-admin — Model / vendor / group performance rankings leaderboard.
 *
 * Read-only. Consumes GET /api/v2/:tenant_slug/analytics/rankings
 * (period-over-period rank/trend/share per model, per channel-type vendor,
 * or per logs.group — see internal/adapter/repo/analytics.go GetRankings). The
 * tenant-admin gate lives server-side (requireTenantAdmin inside
 * GetTenantRankingsV2); a 403 renders the same forbidden panel the
 * ModelPerformance admin page uses for its own root-only gate.
 *
 * Root additionally gets a cross-tenant scope, which reads the platform-wide
 * GET /api/v2/admin/analytics/rankings (GetRankingsV2, RootJWTAuth, optional
 * tenant_id filter). That endpoint shipped with no console consumer at all —
 * the leaderboard a root operator saw was silently scoped to one tenant, with
 * nothing in the UI saying so.
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
  const { t: tr } = useTranslation();
  if (row.is_new) {
    return (
      <span className='muted'>{tr('console.rankings.trend_new', 'new')}</span>
    );
  }
  const d = row.rank_delta ?? 0;
  if (d > 0)
    return <span style={{ color: 'var(--hf-positive, #2a9d5c)' }}>▲{d}</span>;
  if (d < 0)
    return <span style={{ color: 'var(--hf-negative, #d64545)' }}>▼{-d}</span>;
  return (
    <span className='muted'>{tr('console.rankings.trend_flat', '—')}</span>
  );
};

// Share as a bar plus the number (openrouter.ai leaderboard): the column
// is read by comparing rows, which a bare percentage makes you do in your
// head.
const ShareBar = ({ pct }) => (
  <span
    data-testid='rankings-share'
    style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}
  >
    <span
      style={{
        width: 72,
        height: 6,
        borderRadius: 3,
        background: 'var(--hf-sunken)',
        overflow: 'hidden',
      }}
    >
      <span
        style={{
          display: 'block',
          height: '100%',
          width: `${Math.max(0, Math.min(100, pct || 0))}%`,
          background: 'var(--hf-accent)',
        }}
      />
    </span>
    {fmtPct(pct)}
  </span>
);

const HFRankings = () => {
  const { t: tr } = useTranslation();
  const tenantSlug = useTenantSlug();
  const [by, setBy] = useState('model');
  // 'tenant' reads the tenant-scoped route; 'all' reads the platform-wide
  // admin route, which is offered to root alone: every route under
  // /api/v2/admin is mounted behind middleware.RootJWTAuth, whose session
  // path answers 403 PERMISSION_DENIED to an authenticated caller below
  // RoleRootUser and 401 UNAUTHENTICATED to one with no session
  // (middleware/admin_jwt_auth.go, rootSessionAuth + rewriteAsV2Denial). It
  // used to say 401 for both, which was wrong for the case that happens.
  const rootUser = isRoot();
  const [scope, setScope] = useState('tenant');
  const [hours, setHours] = useState(24);
  const [rows, setRows] = useState([]);
  const [cachedAt, setCachedAt] = useState(null);
  const [totalTokens, setTotalTokens] = useState(0);
  const [loading, setLoading] = useState(true);
  const [forbidden, setForbidden] = useState(false);
  // null | 'load_failed' | 'rate_limited' — set on any non-403 fetch
  // failure so a stale table from a previous tab/preset never survives
  // under the newly selected one; see the render branch below.
  const [error, setError] = useState(null);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setForbidden(false);
    setError(null);
    const params = new URLSearchParams({ by, hours: String(hours) });
    const allTenants = rootUser && scope === 'all';
    const url = allTenants
      ? `/api/v2/admin/analytics/rankings?${params}`
      : `/api/v2/${tenantSlug}/analytics/rankings?${params}`;
    API.get(url, {
      skipErrorHandler: true,
    })
      .then((res) => {
        if (cancelled) return;
        if (res?.data?.success) {
          setRows(res.data.data?.rows ?? []);
          setCachedAt(res.data.data?.cached_at ?? null);
          setTotalTokens(res.data.data?.total_tokens ?? 0);
        }
      })
      .catch((err) => {
        if (cancelled) return;
        const status = err?.response?.status;
        if (status === 403) {
          setForbidden(true);
          return;
        }
        // Setting `error` switches the render below off the rows/table
        // branch entirely, so the previous tab/preset's stale rows never
        // stay on screen under the newly selected one.
        setError(status === 429 ? 'rate_limited' : 'load_failed');
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [tenantSlug, by, hours, rootUser, scope]);

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
              'rank · trend · share vs the previous window, per model, per vendor or per group',
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
          {/* Tab/preset controls render above the error branch too: a
              transient 5xx/429 must not strand the tenant admin in a
              dead-end panel with no way to switch tab/preset or retry
              (cycle-7 findings round 2, item 9). */}
          <div
            style={{
              display: 'flex',
              gap: 8,
              marginBottom: 12,
              alignItems: 'center',
              flexWrap: 'wrap',
            }}
          >
            {rootUser && (
              <>
                <button
                  type='button'
                  data-testid='rankings-scope-tenant'
                  className={'btn sm' + (scope === 'tenant' ? ' primary' : '')}
                  onClick={() => setScope('tenant')}
                >
                  {tr('console.rankings.scope_tenant', 'this tenant')}
                </button>
                <button
                  type='button'
                  data-testid='rankings-scope-all'
                  className={'btn sm' + (scope === 'all' ? ' primary' : '')}
                  onClick={() => setScope('all')}
                >
                  {tr('console.rankings.scope_all', 'all tenants')}
                </button>
                <span
                  aria-hidden='true'
                  style={{ opacity: 0.4, padding: '0 4px' }}
                >
                  |
                </span>
              </>
            )}
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
            <button
              type='button'
              data-testid='rankings-by-group'
              className={'btn sm' + (by === 'group' ? ' primary' : '')}
              onClick={() => setBy('group')}
            >
              {tr('console.rankings.by_group', 'by group')}
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

          {error ? (
            <div
              className='panel'
              style={{ padding: '20px 24px' }}
              data-testid='rankings-error'
            >
              <div className='strong' style={{ marginBottom: 6 }}>
                {error === 'rate_limited'
                  ? tr(
                      'console.rankings.rate_limited',
                      'Rate limited, try again shortly.',
                    )
                  : tr(
                      'console.rankings.load_failed',
                      'Failed to load rankings.',
                    )}
              </div>
            </div>
          ) : (
            <>
              {cachedAt ? (
                <div
                  className='muted'
                  style={{ fontSize: 11, marginBottom: 10 }}
                >
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
                        <td style={tdStyle} data-testid='rankings-name'>
                          {/* Requests refused before a model was resolved
                              log an empty model name; they rank as one
                              blank-named row unless it is labelled. */}
                          {r.name ? (
                            <span
                              style={{
                                display: 'inline-flex',
                                alignItems: 'center',
                                gap: 8,
                              }}
                            >
                              {by !== 'group' && (
                                <HfVendorIcon
                                  size={16}
                                  model={by === 'model' ? r.name : undefined}
                                  vendor={by === 'vendor' ? r.name : undefined}
                                />
                              )}
                              {r.name}
                            </span>
                          ) : (
                            <span className='muted'>
                              {tr(
                                'console.rankings.unnamed',
                                'unresolved (request refused before routing)',
                              )}
                            </span>
                          )}
                        </td>
                        <td style={tdStyle}>
                          <Trend row={r} />
                        </td>
                        <td style={tdStyle}>{fmtInt(r.requests)}</td>
                        <td style={tdStyle}>
                          {r.requests_growth_pct == null
                            ? tr('console.rankings.growth_flat', '—')
                            : `${r.requests_growth_pct >= 0 ? '+' : ''}${r.requests_growth_pct.toFixed(1)}%`}
                        </td>
                        <td style={tdStyle}>{fmtInt(r.total_tokens)}</td>
                        <td style={tdStyle}>
                          <ShareBar pct={r.token_share_pct} />
                        </td>
                        <td style={tdStyle}>{usd(r.quota)}</td>
                        <td style={tdStyle}>
                          <ShareBar pct={r.quota_share_pct} />
                        </td>
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
            </>
          )}
        </div>
      )}
    </HFShell>
  );
};

export default HFRankings;
