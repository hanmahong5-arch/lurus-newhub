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
import { API } from '../../../helpers';
import { getQuotaPerUSD } from '../../../helpers/formatting';
import { classifyLoad } from '../../../helpers/loadState';
import HfLoadError from '../../../components/hifi/HfLoadError';

// ─── Stats drawer ─────────────────────────────────────────────────────────────

const usd = (q) => `$${((q || 0) / getQuotaPerUSD()).toFixed(2)}`;
const count = (n) => Number(n || 0).toLocaleString();

// The rows the drawer shows, in reading order, each with a human label and a
// formatter. The endpoint also returns active_subscriptions and
// total_topup_amount, but repo.GetTenantStats never fills them in: they are
// always 0, and a 0 printed here would read as a measured fact. tenant_id is
// already the drawer's subject.
export const statRows = (s, tr) => [
  [
    tr('console.tenant.stat_seats', 'Seats used'),
    s.max_users > 0
      ? `${count(s.user_count)} / ${count(s.max_users)}`
      : count(s.user_count),
  ],
  [tr('console.tenant.stat_tokens', 'API keys'), count(s.token_count)],
  [tr('console.tenant.stat_channels', 'Channels'), count(s.channel_count)],
  [tr('console.tenant.stat_quota_used', 'Quota used'), usd(s.total_quota_used)],
  [
    tr('console.tenant.stat_quota_remaining', 'Quota remaining'),
    usd(s.total_quota_remaining),
  ],
  [
    tr('console.tenant.stat_quota_cap', 'Quota cap'),
    s.max_quota > 0
      ? usd(s.max_quota)
      : tr('console.tenant.stat_unlimited', 'Unlimited'),
  ],
  [
    tr('console.tenant.stat_redemptions', 'Redemption codes'),
    count(s.total_redemptions),
  ],
  [tr('console.tenant.stat_logs', 'Requests logged'), count(s.log_count)],
  [
    tr('console.tenant.stat_last_activity', 'Last activity'),
    s.last_activity_at > 0
      ? new Date(s.last_activity_at * 1000).toLocaleString()
      : tr('console.tenant.stat_never', 'Never'),
  ],
];

const StatsDrawer = ({ tenant, onClose }) => {
  const { t: tr } = useTranslation();
  const [stats, setStats] = useState(null);
  // null while loading, then classifyLoad()'s verdict.
  const [status, setStatus] = useState(null);

  const load = useCallback(async () => {
    setStatus(null);
    let outcome;
    try {
      const res = await API.get(`/api/v2/admin/tenants/${tenant.id}/stats`);
      outcome = classifyLoad(res);
      if (outcome === 'ok') setStats(res.data.data);
    } catch (err) {
      outcome = classifyLoad(err);
    }
    setStatus(outcome);
  }, [tenant.id]);

  useEffect(() => {
    load();
  }, [load]);

  useEffect(() => {
    const onKey = (ev) => ev.key === 'Escape' && onClose();
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  const titleId = `tenant-stats-title-${tenant.id}`;
  return (
    <div
      style={{
        position: 'fixed',
        inset: 0,
        background: 'rgba(0,0,0,0.45)',
        zIndex: 500,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        padding: 16,
      }}
      onClick={(e) => e.target === e.currentTarget && onClose()}
    >
      <div
        role='dialog'
        aria-modal='true'
        aria-labelledby={titleId}
        data-testid='tenant-stats-dialog'
        style={{
          background: 'var(--hf-paper)',
          border: '1px solid var(--hf-rule)',
          borderRadius: 4,
          padding: 28,
          width: 460,
          maxWidth: '100%',
          display: 'flex',
          flexDirection: 'column',
          gap: 14,
        }}
      >
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            gap: 12,
          }}
        >
          <div id={titleId} className='strong' style={{ fontSize: 15 }}>
            {tenant.name} · {tr('console.tenant.stats_title', 'stats')}
          </div>
          <button
            type='button'
            className='btn ghost sm'
            onClick={onClose}
            aria-label={tr('console.common.close', 'close')}
          >
            ✕
          </button>
        </div>

        {status === null && (
          <div className='muted' style={{ fontSize: 12 }} aria-live='polite'>
            {tr('console.common.loading', 'Loading…')}
          </div>
        )}

        {status !== null && status !== 'ok' && (
          <HfLoadError
            status={status}
            variant='inset'
            title={tr('console.tenant.stats_load_error', 'Couldn’t load stats')}
            onRetry={load}
            testId='tenant-stats-error'
          />
        )}

        {status === 'ok' && stats && (
          <dl className='panel' style={{ margin: 0 }}>
            {statRows(stats, tr).map(([label, value], i, arr) => (
              <div
                key={label}
                style={{
                  display: 'grid',
                  gridTemplateColumns: 'minmax(120px, 44%) 1fr',
                  padding: '10px 16px',
                  borderBottom:
                    i < arr.length - 1 ? '1px dashed var(--hf-rule)' : 0,
                  fontSize: 12,
                  alignItems: 'center',
                }}
              >
                <dt className='lbl'>{label}</dt>
                <dd
                  className='mono strong'
                  style={{ margin: 0, textAlign: 'right' }}
                >
                  {value}
                </dd>
              </div>
            ))}
          </dl>
        )}
      </div>
    </div>
  );
};

export default StatsDrawer;
