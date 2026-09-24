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
import { API } from '../../../helpers';

// ─── Stats drawer ─────────────────────────────────────────────────────────────

const StatsDrawer = ({ tenant, onClose }) => {
  const { t: tr } = useTranslation();
  const [stats, setStats] = useState(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const res = await API.get(`/api/v2/admin/tenants/${tenant.id}/stats`);
        if (!cancelled && res?.data?.success) {
          setStats(res.data.data);
        }
      } catch (_) {
        // silently handled
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [tenant.id]);

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
      }}
      onClick={(e) => e.target === e.currentTarget && onClose()}
    >
      <div
        style={{
          background: 'var(--hf-paper)',
          border: '1px solid var(--hf-rule)',
          borderRadius: 4,
          padding: 28,
          width: 460,
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
          }}
        >
          <div className='strong' style={{ fontSize: 15 }}>
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

        {loading && (
          <div className='muted' style={{ fontSize: 12 }}>
            {tr('console.common.loading', 'Loading…')}
          </div>
        )}

        {!loading && !stats && (
          <div className='muted' style={{ fontSize: 12 }}>
            {tr('console.tenant.no_stats', 'No stats available.')}
          </div>
        )}

        {!loading && stats && (
          <div className='panel'>
            {Object.entries(stats).map(([k, v], i, arr) => (
              <div
                key={k}
                style={{
                  display: 'grid',
                  gridTemplateColumns: '160px 1fr',
                  padding: '10px 16px',
                  borderBottom:
                    i < arr.length - 1 ? '1px dashed var(--hf-rule)' : 0,
                  fontSize: 12,
                  alignItems: 'center',
                }}
              >
                <span className='lbl'>{k}</span>
                <span className='mono strong'>{String(v)}</span>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
};

export default StatsDrawer;
