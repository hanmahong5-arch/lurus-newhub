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
import React, { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import HFShell from '../../../../components/hifi/HFShell';
import { API } from '../../../../helpers';
import { formatTime } from '../../../../helpers/formatting';
import { CHANNEL_OPTIONS } from '../../../../constants/channel.constants';

/*
 * v2 admin — Model pools overview.
 *
 * Read-only. Consumes GET /api/v2/admin/model-pools: one section per routing
 * group (a channel in "default,free" shows up under both), each channel's
 * multi-key state and, per model it serves, 24h request/error counts joined
 * with the active-probe health row (entity.ModelHealth, migration 044,
 * internal/app/modelprobe). Root-only, same failure taxonomy as
 * ModelPerformance next to it in the nav: 403 / 401 / everything-else are
 * three different messages, not one blank table.
 */

const CHANNEL_TYPE_NAMES = new Map(
  CHANNEL_OPTIONS.map((o) => [o.value, o.label]),
);

const fmtInt = (n) => Number(n ?? 0).toLocaleString();

const fmtMs = (ms) => (ms > 0 ? `${Math.round(ms)}ms` : '—');

const truncate = (s, n) => (!s ? '' : s.length > n ? `${s.slice(0, n)}…` : s);

// health: null | {ok, last_probe_at, latency_ms, last_error,
// consecutive_failures, auto_disabled}
//
// `variant` distinguishes "never probed" (no health row yet — unknown, not
// healthy) from "ok" (an actual passing probe): before this, both rendered
// with the same untoned label, so an operator could not tell a model that
// had never been checked from one that was confirmed healthy.
function healthBadge(tr, health) {
  if (!health) {
    return {
      label: tr(
        'console.admin.model_pools.health_never_probed',
        'never probed',
      ),
      tone: undefined,
      variant: 'never_probed',
      title: tr(
        'console.admin.model_pools.never_probed_hint',
        'Not probed yet — health is unknown, not healthy',
      ),
    };
  }
  if (health.auto_disabled) {
    return {
      label: tr(
        'console.admin.model_pools.health_auto_disabled',
        'auto-disabled',
      ),
      tone: 'var(--hf-err)',
      variant: 'auto_disabled',
    };
  }
  if (!health.ok) {
    return {
      label: tr(
        'console.admin.model_pools.health_failing',
        'failing ({{count}})',
        { count: health.consecutive_failures ?? 0 },
      ),
      tone: 'var(--hf-warn)',
      variant: 'failing',
    };
  }
  return {
    label: tr('console.admin.model_pools.health_ok', 'ok'),
    tone: undefined,
    variant: 'ok',
  };
}

const HFModelPools = () => {
  const { t: tr } = useTranslation();
  const [pools, setPools] = useState([]);
  const [loading, setLoading] = useState(true);
  const [forbidden, setForbidden] = useState(false);
  const [signedOut, setSignedOut] = useState(false);
  const [error, setError] = useState(null);
  const [reloadTick, setReloadTick] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setForbidden(false);
    setSignedOut(false);
    setError(null);
    API.get('/api/v2/admin/model-pools', { skipErrorHandler: true })
      .then((res) => {
        if (cancelled) return;
        if (res?.data?.success) {
          setPools(res.data.data?.pools ?? []);
        } else {
          setError(res?.data?.message ?? '');
        }
      })
      .catch((err) => {
        if (cancelled) return;
        const status = err?.response?.status;
        if (status === 403) setForbidden(true);
        else if (status === 401) setSignedOut(true);
        else setError(err?.response?.data?.message ?? '');
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [reloadTick]);

  const retry = useCallback(() => setReloadTick((n) => n + 1), []);

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

  // requests/errors/latency are the pool table's number columns — L1's `num`
  // contract right-aligns + tabular-nums them so a scanning eye can compare
  // magnitudes down the column instead of ragged left-aligned digits.
  const numThStyle = { ...thStyle, textAlign: 'right' };
  const numTdStyle = { ...tdStyle, textAlign: 'right' };

  return (
    <HFShell
      active='admin-model-pools'
      crumbs={[
        tr('console.admin.analytics.crumb_admin', 'operations & insights'),
        tr('console.admin.model_pools.crumb', 'model pools'),
      ]}
    >
      <div className='hf-page-head'>
        <div>
          <div className='lbl' style={{ marginBottom: 6 }}>
            {tr('console.admin.model_pools.heading_lbl', 'model pools')}
          </div>
          <h1 data-testid='pools-headline'>
            {error !== null
              ? tr(
                  'console.admin.model_pools.error_title',
                  'Model pools unknown',
                )
              : tr('console.admin.model_pools.title', 'Model pools')}
          </h1>
          <div className='sub'>
            {tr(
              'console.admin.model_pools.sub',
              'channels grouped by routing pool · keys · active-probe health · 24h usage',
            )}
          </div>
        </div>
      </div>

      {forbidden ? (
        <div style={{ padding: 24 }}>
          <div
            className='panel'
            style={{ padding: '20px 24px' }}
            data-testid='pools-forbidden'
          >
            <div className='strong' style={{ marginBottom: 6 }}>
              {tr(
                'console.admin.model_pools.forbidden_title',
                'Admin access required',
              )}
            </div>
            <div className='muted' style={{ fontSize: 12 }}>
              {tr(
                'console.admin.model_pools.forbidden_body',
                'You do not have permission to view model pools.',
              )}
            </div>
          </div>
        </div>
      ) : signedOut ? (
        <div style={{ padding: 24 }}>
          <div
            className='panel'
            style={{ padding: '20px 24px' }}
            data-testid='pools-signed-out'
          >
            <div className='strong' style={{ marginBottom: 6 }}>
              {tr(
                'console.admin.session_expired_title',
                'Your session has expired',
              )}
            </div>
            <div className='muted' style={{ fontSize: 12, marginBottom: 12 }}>
              {tr(
                'console.admin.session_expired_body',
                'The server no longer recognises this session, so nothing on this page can be read. Sign in again to continue.',
              )}
            </div>
            <a className='btn sm' href='/login' data-testid='pools-sign-in'>
              {tr('console.admin.sign_in_again', 'sign in again')}
            </a>
          </div>
        </div>
      ) : error !== null ? (
        <div style={{ padding: 24 }}>
          <div
            className='panel'
            style={{ padding: '20px 24px' }}
            data-testid='pools-error'
          >
            <div className='strong' style={{ marginBottom: 6 }}>
              {tr(
                'console.admin.model_pools.error_title',
                'Model pools unknown',
              )}
            </div>
            <div className='muted' style={{ fontSize: 12, marginBottom: 12 }}>
              {tr(
                'console.admin.model_pools.error_body',
                'The model-pools endpoint did not answer. This is not a report that no pools exist — the overview simply could not be read.',
              )}
            </div>
            {error ? (
              <div
                className='mono muted'
                style={{ fontSize: 11, marginBottom: 12 }}
              >
                {error}
              </div>
            ) : null}
            <button
              type='button'
              className='btn sm'
              data-testid='pools-retry'
              onClick={retry}
            >
              {tr('console.admin.model_pools.retry', 'retry')}
            </button>
          </div>
        </div>
      ) : (
        <div style={{ padding: 24, overflow: 'auto' }}>
          {loading && (
            <span className='muted mono' style={{ fontSize: 11 }}>
              {tr('console.common.loading', 'loading…')}
            </span>
          )}
          {!loading && pools.length === 0 ? (
            <div
              className='panel'
              style={{ padding: '20px 24px' }}
              data-testid='pools-empty'
            >
              <div className='muted' style={{ fontSize: 12 }}>
                {tr(
                  'console.admin.model_pools.empty',
                  'no routing pools found',
                )}
              </div>
            </div>
          ) : (
            pools.map((pool) => (
              <div
                key={pool.group}
                className='panel'
                style={{ padding: '14px 16px', marginBottom: 20 }}
              >
                <div className='lbl' style={{ marginBottom: 10 }}>
                  {tr(
                    'console.admin.model_pools.pool_lbl',
                    'pool · {{group}}',
                    {
                      group: pool.group,
                    },
                  )}
                </div>
                {(pool.channels ?? []).map((ch) => (
                  <div
                    key={ch.id}
                    style={{
                      marginBottom: 18,
                      borderTop: '1px solid var(--hf-rule)',
                      paddingTop: 10,
                    }}
                  >
                    <div
                      style={{
                        display: 'flex',
                        gap: 16,
                        alignItems: 'baseline',
                        flexWrap: 'wrap',
                        marginBottom: 8,
                      }}
                    >
                      <span className='strong'>{ch.name}</span>
                      <span className='muted mono' style={{ fontSize: 11 }}>
                        {CHANNEL_TYPE_NAMES.get(ch.type) ?? ch.type}
                      </span>
                      <span className='muted mono' style={{ fontSize: 11 }}>
                        {tr(
                          'console.admin.model_pools.channel_status',
                          'status',
                        )}
                        : {ch.status}
                      </span>
                      <span className='muted mono' style={{ fontSize: 11 }}>
                        {tr(
                          'console.admin.model_pools.channel_priority',
                          'priority',
                        )}
                        : {ch.priority}
                      </span>
                      <span className='muted mono' style={{ fontSize: 11 }}>
                        {tr('console.admin.model_pools.keys_lbl', 'keys')}:{' '}
                        {fmtInt(ch.keys?.enabled)}{' '}
                        {tr(
                          'console.admin.model_pools.keys_enabled',
                          'enabled',
                        )}{' '}
                        / {fmtInt(ch.keys?.cooling)}{' '}
                        {tr(
                          'console.admin.model_pools.keys_cooling',
                          'cooling',
                        )}{' '}
                        / {fmtInt(ch.keys?.disabled)}{' '}
                        {tr(
                          'console.admin.model_pools.keys_disabled',
                          'disabled',
                        )}
                      </span>
                      <span className='muted mono' style={{ fontSize: 11 }}>
                        {tr(
                          'console.admin.model_pools.channel_last_test',
                          'last test',
                        )}
                        : {formatTime(ch.test_time)}
                      </span>
                    </div>

                    {(ch.models ?? []).length === 0 ? (
                      <div className='muted' style={{ fontSize: 12 }}>
                        {tr(
                          'console.admin.model_pools.no_models',
                          'no models configured',
                        )}
                      </div>
                    ) : (
                      <table
                        style={{ width: '100%', borderCollapse: 'collapse' }}
                      >
                        <thead>
                          <tr>
                            <th style={thStyle}>
                              {tr(
                                'console.admin.model_pools.col_model',
                                'model',
                              )}
                            </th>
                            <th style={thStyle}>
                              {tr(
                                'console.admin.model_pools.col_health',
                                'health',
                              )}
                            </th>
                            <th className='num' style={numThStyle}>
                              {tr(
                                'console.admin.model_pools.col_latency',
                                'latency',
                              )}
                            </th>
                            <th style={thStyle}>
                              {tr(
                                'console.admin.model_pools.col_last_probe',
                                'last probe',
                              )}
                            </th>
                            <th className='num' style={numThStyle}>
                              {tr(
                                'console.admin.model_pools.col_requests',
                                'requests (24h)',
                              )}
                            </th>
                            <th className='num' style={numThStyle}>
                              {tr(
                                'console.admin.model_pools.col_errors',
                                'errors (24h)',
                              )}
                            </th>
                            <th style={thStyle}>
                              {tr(
                                'console.admin.model_pools.col_last_error',
                                'last error',
                              )}
                            </th>
                          </tr>
                        </thead>
                        <tbody>
                          {ch.models.map((m) => {
                            const badge = healthBadge(tr, m.health);
                            return (
                              <tr key={m.model} data-testid='pool-model-row'>
                                <td style={{ ...tdStyle, fontWeight: 600 }}>
                                  {m.model}
                                </td>
                                <td
                                  style={tdStyle}
                                  data-testid={`pool-health-${m.model}`}
                                  title={
                                    badge.variant === 'never_probed'
                                      ? badge.title
                                      : undefined
                                  }
                                >
                                  <span
                                    className={`hf-health-badge hf-health-badge--${badge.variant}`}
                                    style={
                                      badge.variant === 'never_probed'
                                        ? {
                                            color: 'var(--hf-ink-3)',
                                            border:
                                              '1px dashed var(--hf-ink-3)',
                                            borderRadius: 4,
                                            padding: '1px 6px',
                                          }
                                        : { color: badge.tone }
                                    }
                                  >
                                    {badge.label}
                                  </span>
                                </td>
                                <td className='num' style={numTdStyle}>
                                  {fmtMs(m.health?.latency_ms)}
                                </td>
                                <td style={tdStyle}>
                                  {formatTime(m.health?.last_probe_at)}
                                </td>
                                <td className='num' style={numTdStyle}>
                                  {fmtInt(m.requests_24h)}
                                </td>
                                <td className='num' style={numTdStyle}>
                                  {fmtInt(m.errors_24h)}
                                </td>
                                <td
                                  style={tdStyle}
                                  title={m.health?.last_error || ''}
                                  data-testid={`pool-last-error-${m.model}`}
                                >
                                  {truncate(m.health?.last_error, 40)}
                                </td>
                              </tr>
                            );
                          })}
                        </tbody>
                      </table>
                    )}
                  </div>
                ))}
              </div>
            ))
          )}
        </div>
      )}
    </HFShell>
  );
};

export default HFModelPools;
