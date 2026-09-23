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

// Side-by-side model comparison (openrouter.ai "Compare"): pick up to
// MAX_COMPARE models in the marketplace, see their prices, access,
// capabilities and usage in one table. The cheapest input / output price
// in the set is marked, since that is usually why people compare.

import React, { useEffect } from 'react';
import HfVendorIcon from '../../../components/hifi/HfVendorIcon';
import { CAPABILITIES, fmtCompact, fmtUsd } from './catalog';

export const MAX_COMPARE = 4;

const capLabel = (tr, c) => {
  const [key, fallback] = CAPABILITIES[c] || [null, c];
  return key ? tr(`console.models.market.${key}`, fallback) : c;
};

// Index of the lowest non-null value, or -1 when fewer than two are priced
// (a "cheapest" mark over one number says nothing).
export const cheapestIndex = (values) => {
  let best = -1;
  let priced = 0;
  values.forEach((v, i) => {
    if (v == null) return;
    priced += 1;
    if (best < 0 || v < values[best]) best = i;
  });
  return priced >= 2 ? best : -1;
};

export const CompareBar = ({ ids, tr, onOpen, onClear }) =>
  ids.length === 0 ? null : (
    <div className='hf-compare-bar' data-testid='compare-bar'>
      <span>
        {tr('console.models.compare.selected', '{{n}} selected', {
          n: ids.length,
        })}
        <span className='faint' style={{ marginLeft: 8 }}>
          {tr('console.models.compare.max', 'up to {{n}}', { n: MAX_COMPARE })}
        </span>
      </span>
      <span style={{ flex: 1 }} />
      <button type='button' className='btn sm' onClick={onClear}>
        {tr('console.models.compare.clear', 'clear')}
      </button>
      <button
        type='button'
        className='btn sm primary'
        disabled={ids.length < 2}
        onClick={onOpen}
        data-testid='compare-open'
      >
        {tr('console.models.compare.open', 'compare')} →
      </button>
    </div>
  );

export const CompareDrawer = ({ entries, tr, onClose, onRemove }) => {
  useEffect(() => {
    const onKey = (ev) => ev.key === 'Escape' && onClose();
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  const perM = (e, field) => (e.quotaType === 1 ? null : e[field]);
  const inBest = cheapestIndex(entries.map((e) => perM(e, 'inputPerM')));
  const outBest = cheapestIndex(entries.map((e) => perM(e, 'outputPerM')));
  const best = (
    <span className='tag ok' style={{ marginLeft: 6 }}>
      {tr('console.models.compare.cheapest', 'cheapest')}
    </span>
  );

  const rows = [
    ['vendor', tr('console.models.vendor', 'vendor'), (e) => e.vendor || '—'],
    [
      'access',
      tr('console.models.market.th_access', 'access'),
      (e) =>
        e.routable
          ? tr('console.models.market.callable', 'callable')
          : tr('console.models.market.not_callable', 'not in your groups'),
    ],
    [
      'input',
      tr('console.models.market.th_input', 'input $/M'),
      (e, i) =>
        e.quotaType === 1 ? (
          `${fmtUsd(e.perCall)} ${tr('console.models.market.per_call', 'per call')}`
        ) : (
          <>
            {fmtUsd(e.inputPerM)}
            {i === inBest && best}
          </>
        ),
    ],
    [
      'output',
      tr('console.models.market.th_output', 'output $/M'),
      (e, i) =>
        e.quotaType === 1 ? (
          '—'
        ) : (
          <>
            {fmtUsd(e.outputPerM)}
            {i === outBest && best}
          </>
        ),
    ],
    [
      'cache',
      tr('console.models.market.th_cache', 'cache read $/M'),
      (e) => fmtUsd(e.cacheReadPerM),
    ],
    [
      'caps',
      tr('console.models.market.th_caps', 'capabilities'),
      (e) => e.capabilities.map((c) => capLabel(tr, c)).join(' · ') || '—',
    ],
    [
      'tokens',
      tr('console.models.market.tokens_7d', 'tokens · 7d'),
      (e) => fmtCompact(e.tokens),
    ],
    [
      'desc',
      tr('console.models.compare.description', 'description'),
      (e) => e.description || '—',
    ],
  ];

  return (
    <div className='hf-drawer-backdrop' onClick={onClose}>
      <aside
        className='hf-drawer hf-drawer-wide'
        role='dialog'
        aria-label={tr('console.models.compare.title', 'compare models')}
        data-testid='compare-drawer'
        onClick={(ev) => ev.stopPropagation()}
      >
        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          <div className='display' style={{ fontSize: 24, flex: 1 }}>
            {tr('console.models.compare.title', 'compare models')}
          </div>
          <button
            type='button'
            className='btn sm'
            onClick={onClose}
            aria-label={tr('console.common.close', 'close')}
          >
            ✕
          </button>
        </div>
        <div className='hf-table-scroll' style={{ marginTop: 16 }}>
          <table className='t' data-testid='compare-table'>
            <thead>
              <tr>
                <th />
                {entries.map((e) => (
                  <th key={e.id} style={{ textTransform: 'none' }}>
                    <span
                      style={{
                        display: 'inline-flex',
                        alignItems: 'center',
                        gap: 6,
                      }}
                    >
                      <HfVendorIcon model={e.id} vendor={e.vendor} size={16} />
                      <span className='mono'>{e.id}</span>
                      <button
                        type='button'
                        className='btn sm'
                        style={{ padding: '0 6px' }}
                        aria-label={tr(
                          'console.models.compare.remove',
                          'remove',
                        )}
                        onClick={() => onRemove(e.id)}
                      >
                        ✕
                      </button>
                    </span>
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map(([key, label, cell]) => (
                <tr key={key} data-testid={`compare-row-${key}`}>
                  <td className='muted'>{label}</td>
                  {entries.map((e, i) => (
                    <td
                      key={e.id}
                      className={key === 'desc' ? '' : 'mono'}
                      style={
                        key === 'desc'
                          ? { whiteSpace: 'normal', minWidth: 200 }
                          : undefined
                      }
                    >
                      {cell(e, i)}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </aside>
    </div>
  );
};
