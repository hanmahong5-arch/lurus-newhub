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

import React from 'react';
import { formatUSD } from '../../../helpers/formatting';

const compact = (n) => {
  const v = Number(n) || 0;
  for (const [d, u] of [
    [1e9, 'B'],
    [1e6, 'M'],
    [1e3, 'K'],
  ]) {
    if (v >= d) return `${Number((v / d).toFixed(1))}${u}`;
  }
  return v.toLocaleString();
};

/**
 * Aggregate stat header — GET /logs/stat over the active filters.
 * requests / spend / tokens reflect the full filter window; rpm / tpm are
 * rolling last-60s rates. Every value reads "—" until the first successful
 * fetch rather than a made-up zero.
 *
 * Tokens are shown as input → output plus cache reads, as reported. No hit
 * *ratio* is derived: providers disagree on whether prompt_tokens already
 * includes cached tokens (OpenAI does, Anthropic does not), so one ratio over
 * a mixed window would be wrong for part of it.
 */
const StatHeader = ({ stat, loading, tr }) => {
  const cells = [
    [
      tr('console.log.stat_requests', 'requests'),
      stat ? Number(stat.total_requests ?? 0).toLocaleString() : '—',
      tr('console.log.in_window', 'in window'),
    ],
    [
      tr('console.log.stat_quota', 'quota'),
      stat ? formatUSD(Number(stat.total_quota ?? 0)) : '—',
      tr('console.log.in_window', 'in window'),
    ],
    [
      tr('console.log.stat_tokens', 'tokens in → out'),
      stat
        ? `${compact(stat.prompt_tokens)} → ${compact(stat.completion_tokens)}`
        : '—',
      tr('console.log.in_window', 'in window'),
      'log-stat-tokens',
    ],
    [
      tr('console.log.stat_cache_read', 'cache reads'),
      stat ? compact(stat.cache_read_tokens) : '—',
      tr('console.log.stat_cache_sub', 'tokens served from cache'),
      'log-stat-cache',
    ],
    [
      tr('console.log.stat_rpm', 'rpm'),
      stat ? Number(stat.rpm ?? 0).toLocaleString() : '—',
      tr('console.log.last_60s', 'last 60s'),
    ],
    [
      tr('console.log.stat_tpm', 'tpm'),
      stat ? Number(stat.tpm ?? 0).toLocaleString() : '—',
      tr('console.log.last_60s', 'last 60s'),
    ],
  ];
  return (
    <div
      data-testid='log-stat-header'
      style={{
        display: 'flex',
        gap: 30,
        padding: '10px 28px',
        borderBottom: '1px solid var(--hf-rule)',
        background: 'var(--hf-paper)',
        flexWrap: 'wrap',
      }}
    >
      {cells.map(([l, v, sub, testid]) => (
        <div key={l} data-testid={testid}>
          <div className='lbl'>
            {l}
            <span className='faint' style={{ marginLeft: 5 }}>
              · {sub}
            </span>
          </div>
          <div className='display' style={{ fontSize: 20, marginTop: 2 }}>
            {loading ? '…' : v}
          </div>
        </div>
      ))}
    </div>
  );
};

export default StatHeader;
