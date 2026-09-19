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

/*
 * v2 admin — live routing health. Wired to GET /api/v2/admin/gateway/health,
 * which reports per-channel circuit-breaker state as the relay sees it now.
 *
 * The page states both caveats the API carries rather than implying a
 * completeness it does not have:
 *   - breakers register lazily, so a channel with no traffic since boot is
 *     ABSENT, not healthy;
 *   - breaker state is per-replica, so this is one pod's view.
 * A dashboard that quietly showed "all green" for an untouched channel would be
 * worse than no dashboard during an incident.
 *
 * Cycle 12 L3 closed the third way it could say "all green" without knowing:
 * a failed fetch. 403/401 are a permission state, everything else (502, a
 * dropped connection, a 200 carrying success:false) is an ERROR state with a
 * retry, never `data === null` rendered as openCount 0 and an empty table.
 */

const POLL_MS = 5000;

const stateClass = (s) =>
  s === 'open' ? 'tag error' : s === 'half_open' ? 'tag' : 'tag ok';

const fmtCountdown = (unix) => {
  if (!unix) return null;
  const secs = unix - Math.floor(Date.now() / 1000);
  return secs > 0 ? `${secs}s` : null;
};

const V2AdminGateway = () => {
  const { t: tr } = useTranslation();
  const [data, setData] = useState(null);
  const [loading, setLoading] = useState(true);
  const [forbidden, setForbidden] = useState(false);
  const [live, setLive] = useState(true);
  // null when the last fetch succeeded; otherwise the backend message, or ''
  // when there was none. Distinct from `forbidden`: one means "you may not
  // see this", the other means "nobody can see this right now".
  const [error, setError] = useState(null);

  const fetchHealth = useCallback(async () => {
    try {
      const res = await API.get('/api/v2/admin/gateway/health');
      if (res?.data?.success) {
        setData(res.data.data);
        setError(null);
      } else {
        setError(res?.data?.message ?? '');
      }
    } catch (err) {
      const status = err?.response?.status;
      if (status === 403 || status === 401) {
        setForbidden(true);
        setError(null);
      } else {
        setError(err?.response?.data?.message ?? '');
      }
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchHealth();
  }, [fetchHealth]);

  // Polling stops in the error state as well as the forbidden one: five
  // seconds of retries against a dead endpoint adds load and hides the
  // failure behind a flicker. The retry button below is the way back.
  useEffect(() => {
    if (!live || forbidden || error !== null) return undefined;
    const id = setInterval(fetchHealth, POLL_MS);
    return () => clearInterval(id);
  }, [live, forbidden, error, fetchHealth]);

  const retry = useCallback(() => {
    setError(null);
    setLoading(true);
    fetchHealth();
  }, [fetchHealth]);

  const routes = data?.routes ?? [];
  const openCount = data?.open ?? 0;

  return (
    <HFShell
      active='admin-gateway'
      crumbs={[
        tr('console.admin.gateway.crumb_admin', 'operations & insights'),
        tr('console.admin.gateway.crumb', 'gateway'),
      ]}
      actions={
        !forbidden &&
        error === null && (
          <button
            type='button'
            className='btn ghost'
            data-testid='gateway-live-toggle'
            onClick={() => setLive((v) => !v)}
          >
            {live
              ? tr('console.admin.gateway.pause', '❚❚ pause')
              : tr('console.admin.gateway.resume', '▶ live')}
          </button>
        )
      }
    >
      <div className='hf-page-head'>
        <div>
          <div className='lbl' style={{ marginBottom: 6 }}>
            {tr('console.admin.gateway.heading_lbl', 'routing health')}
          </div>
          <h1 data-testid='gateway-headline'>
            {loading
              ? '…'
              : forbidden
                ? tr(
                    'console.admin.gateway.forbidden_title',
                    'Admin access required',
                  )
                : error !== null
                  ? tr(
                      'console.admin.gateway.error_title',
                      'Breaker state unknown',
                    )
                  : openCount > 0
                    ? tr(
                        'console.admin.gateway.open_count',
                        '{{count}} breakers open',
                        { count: openCount },
                      )
                    : tr(
                        'console.admin.gateway.all_closed',
                        'no open breakers',
                      )}
          </h1>
          <div className='sub'>
            {tr(
              'console.admin.gateway.sub',
              'circuit breakers · this replica · lazily registered',
            )}
          </div>
        </div>
      </div>

      {forbidden ? (
        <div style={{ padding: 24 }}>
          <div className='panel' style={{ padding: '20px 24px' }}>
            <div className='strong' style={{ marginBottom: 6 }}>
              {tr(
                'console.admin.gateway.forbidden_title',
                'Admin access required',
              )}
            </div>
            <div className='muted' style={{ fontSize: 12 }}>
              {tr(
                'console.admin.gateway.forbidden_body',
                'You do not have permission to read gateway health. Contact a platform administrator.',
              )}
            </div>
          </div>
        </div>
      ) : error !== null ? (
        <div style={{ padding: 24 }}>
          <div
            className='panel'
            style={{ padding: '20px 24px' }}
            data-testid='gateway-error'
          >
            <div className='strong' style={{ marginBottom: 6 }}>
              {tr('console.admin.gateway.error_title', 'Breaker state unknown')}
            </div>
            <div className='muted' style={{ fontSize: 12, marginBottom: 12 }}>
              {tr(
                'console.admin.gateway.error_body',
                'The gateway health endpoint did not answer. This is not a report that every channel is healthy — breaker state is simply unknown until the call succeeds.',
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
              data-testid='gateway-retry'
              onClick={retry}
            >
              {tr('console.admin.gateway.retry', 'retry')}
            </button>
          </div>
        </div>
      ) : (
        <div style={{ padding: 24 }}>
          {/* Stated up front, not in a footnote: an absent channel is unknown,
              not healthy. */}
          <div
            className='panel'
            style={{ padding: '12px 16px', marginBottom: 16 }}
            data-testid='gateway-caveat'
          >
            <div className='muted' style={{ fontSize: 11, lineHeight: 1.7 }}>
              {tr(
                'console.admin.gateway.caveat',
                'Breakers are created on a channel’s first request, so channels with no traffic since this replica started are not listed — absent means unknown, not healthy. State is per-replica.',
              )}
            </div>
          </div>

          <div className='panel'>
            {loading ? (
              <div
                className='muted'
                style={{ padding: '20px 24px', fontSize: 12 }}
              >
                {tr('console.common.loading', 'Loading…')}
              </div>
            ) : routes.length === 0 ? (
              <div
                className='muted'
                style={{ padding: '20px 24px', fontSize: 12 }}
                data-testid='gateway-empty'
              >
                {tr(
                  'console.admin.gateway.empty',
                  'No channel has served traffic on this replica yet.',
                )}
              </div>
            ) : (
              <table className='t'>
                <thead>
                  <tr>
                    <th>{tr('console.admin.gateway.th_channel', 'channel')}</th>
                    <th>
                      {tr('console.admin.gateway.th_provider', 'provider')}
                    </th>
                    <th>{tr('console.admin.gateway.th_state', 'breaker')}</th>
                    <th>
                      {tr(
                        'console.admin.gateway.th_fails',
                        'consecutive fails',
                      )}
                    </th>
                    <th>
                      {tr('console.admin.gateway.th_probe', 'next probe')}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {routes.map((r) => (
                    <tr
                      key={r.channel_id}
                      data-testid={`gateway-row-${r.channel_id}`}
                    >
                      <td>
                        <div className='strong'>
                          {r.channel_name || `#${r.channel_id}`}
                        </div>
                        <div className='faint mono' style={{ fontSize: 10 }}>
                          #{r.channel_id}
                        </div>
                      </td>
                      <td className='mono muted'>{r.provider || '—'}</td>
                      <td>
                        <span
                          className={stateClass(r.state)}
                          data-testid={`gateway-state-${r.channel_id}`}
                        >
                          {tr(
                            `console.admin.gateway.state_${r.state}`,
                            r.state,
                          )}
                        </span>
                      </td>
                      <td className='mono'>
                        {r.consecutive_fails ?? 0}/{r.threshold ?? '—'}
                      </td>
                      <td className='mono muted'>
                        {r.state === 'open'
                          ? (fmtCountdown(r.probe_eligible_unix) ??
                            tr('console.admin.gateway.probe_due', 'due'))
                          : '—'}
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

export default V2AdminGateway;
