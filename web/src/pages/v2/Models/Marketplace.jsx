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

// Model marketplace view — benchmarked against openrouter.ai/models: a facet
// rail (search, vendor, capability, callable-only), a card list or a dense
// table, and a detail drawer with prices, capabilities, usage and a
// copy-paste quick start. Data comes in already merged (./catalog.js); this
// file only renders and holds view state.

import React, { useMemo, useState } from 'react';
import HfVendorIcon from '../../../components/hifi/HfVendorIcon';
import {
  capabilityFacets,
  entryCanChat,
  filterCatalog,
  fmtCompact,
  fmtMs,
  fmtPct,
  fmtUsd,
  sortCatalog,
  vendorFacets,
} from './catalog';
import { CompareBar, CompareDrawer, MAX_COMPARE } from './Compare';
import ModelDrawer, { CallableBadge, capLabel } from './ModelDrawer';

// Below this many models the facet rail / sort / view-toggle chrome is more
// furniture than filter — a tenant with three models does not need a
// three-column browser to find them. Any active filter, interactive or
// arriving on the URL, still renders it (see hasUrlFilterParams below), so
// this can never trap someone mid-search behind a collapsed rail.
const SMALL_CATALOG_THRESHOLD = 5;

// Known marketplace filter query keys. A deep link that already carries one
// of these (e.g. a saved "vendor=OpenAI" link) means the caller arrived
// wanting to filter, so the small-catalog collapse must not hide the
// controls that would let them see or change it.
const URL_FILTER_PARAM_KEYS = ['q', 'vendor', 'cap', 'routable'];

function hasUrlFilterParams() {
  try {
    if (typeof window === 'undefined') return false;
    const params = new URLSearchParams(window.location.search);
    return URL_FILTER_PARAM_KEYS.some((k) => params.has(k));
  } catch (_) {
    return false;
  }
}

// A row of small equal-height data pills — mono, low-contrast, no
// value-based colour coding (OpenRouter/Groq-style scan strip). Price,
// usage and the "no traffic yet" placeholder all render as one of these so
// a card's stat row keeps a constant height whether or not it has data.
const Pill = ({ children, muted, testId }) => (
  <span
    className='mono'
    data-testid={testId}
    style={{
      display: 'inline-flex',
      alignItems: 'center',
      gap: 4,
      padding: '2px 7px',
      borderRadius: 3,
      background: 'var(--hf-sunken)',
      color: muted ? 'var(--hf-ink-3)' : 'var(--hf-ink-2)',
      fontSize: 11,
      lineHeight: 1.5,
    }}
  >
    {children}
  </span>
);

const PriceLine = ({ e, tr }) => {
  if (!e.priced) {
    return (
      <Pill muted>
        {tr('console.models.market.unpriced', 'no price configured')}
      </Pill>
    );
  }
  if (e.quotaType === 1) {
    return (
      <Pill>
        <b>{fmtUsd(e.perCall)}</b>
        {tr('console.models.market.per_call', 'per call')}
      </Pill>
    );
  }
  return (
    <>
      <Pill>
        <b>{fmtUsd(e.inputPerM)}</b>
        {tr('console.models.market.per_m_input', '/M input')}
      </Pill>
      <Pill>
        <b>{fmtUsd(e.outputPerM)}</b>
        {tr('console.models.market.per_m_output', '/M output')}
      </Pill>
      {e.cacheReadPerM != null && (
        <Pill muted>
          {tr('console.models.market.cache_read', 'cache read')}{' '}
          <b>{fmtUsd(e.cacheReadPerM)}</b>
          {tr('console.models.market.per_m', '/M')}
        </Pill>
      )}
    </>
  );
};

// p50 latency and error rate for this tenant over 24h. A thin sample is
// said to be thin instead of shown as a measurement; no sample at all is
// its own equal-height placeholder rather than an absent element, so a
// grid of cards does not lose a row of height wherever traffic is missing.
export const PerfLine = ({ e, tr }) => {
  if (e.p50Ms == null && e.errorRate == null) {
    return (
      <Pill muted testId={`model-perf-${e.id}`}>
        {tr('console.models.no_traffic_yet', 'no traffic yet')}
      </Pill>
    );
  }
  if (!e.enoughSamples) {
    return (
      <span className='faint' data-testid={`model-perf-${e.id}`}>
        {tr('console.models.perf.thin', 'too little traffic to measure')}
      </span>
    );
  }
  return (
    <span className='muted' data-testid={`model-perf-${e.id}`}>
      p50 <b className='mono'>{fmtMs(e.p50Ms)}</b> ·{' '}
      {tr('console.models.perf.error_rate', 'errors')}{' '}
      <b className='mono'>{fmtPct(e.errorRate)}</b>
    </span>
  );
};

const FacetRow = ({ checked, onChange, label, count, icon, testid }) => (
  <label
    data-testid={testid}
    style={{
      display: 'flex',
      alignItems: 'center',
      gap: 8,
      padding: '5px 4px',
      cursor: 'pointer',
      fontSize: 13,
    }}
  >
    <input type='checkbox' checked={checked} onChange={onChange} />
    {icon}
    <span style={{ flex: 1, minWidth: 0 }} className='truncate'>
      {label}
    </span>
    <span className='faint mono' style={{ fontSize: 11 }}>
      {count}
    </span>
  </label>
);

const toggle = (list, v) =>
  list.includes(v) ? list.filter((x) => x !== v) : [...list, v];

const ModelCard = ({
  e,
  tr,
  onOpen,
  onTry,
  availability,
  compared,
  onCompare,
}) => (
  <div
    className='panel hf-model-card'
    role='button'
    tabIndex={0}
    data-testid={`model-card-${e.id}`}
    onClick={() => onOpen(e.id)}
    onKeyDown={(ev) => {
      if (ev.key === 'Enter' || ev.key === ' ') onOpen(e.id);
    }}
    style={{ padding: '16px 18px', cursor: 'pointer' }}
  >
    <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
      <HfVendorIcon model={e.id} vendor={e.vendor} size={28} />
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <span className='strong mono' style={{ fontSize: 15 }}>
            {e.id}
          </span>
          <CallableBadge e={e} tr={tr} />
        </div>
        <div className='muted' style={{ fontSize: 12, marginTop: 2 }}>
          {e.vendor || tr('console.models.unknown_vendor', 'unknown vendor')}
        </div>
      </div>
    </div>
    <div
      className={e.description ? '' : 'faint'}
      style={{
        fontSize: 13,
        lineHeight: 1.55,
        marginTop: 10,
        display: '-webkit-box',
        WebkitLineClamp: 2,
        WebkitBoxOrient: 'vertical',
        overflow: 'hidden',
      }}
    >
      {e.description ||
        tr(
          'console.models.market.no_description',
          'No description yet — an administrator can add one to the model catalogue.',
        )}
    </div>
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 10,
        marginTop: 12,
        flexWrap: 'wrap',
        fontSize: 12,
      }}
    >
      <PriceLine e={e} tr={tr} />
      <Pill>
        <b>{fmtCompact(e.tokens)}</b>
        {tr('console.models.market.tokens_7d', 'tokens · 7d')}
      </Pill>
      <PerfLine e={e} tr={tr} />
      <span style={{ flex: 1 }} />
      {e.capabilities.map((c) => (
        <span key={c} className='tag'>
          {capLabel(tr, c)}
        </span>
      ))}
      {availability}
      <button
        type='button'
        className={'btn sm' + (compared ? ' primary' : '')}
        aria-pressed={compared}
        data-testid={`model-compare-${e.id}`}
        onClick={(ev) => {
          ev.stopPropagation();
          onCompare(e.id);
        }}
      >
        {compared
          ? tr('console.models.compare.added', '✓ comparing')
          : tr('console.models.compare.add', '+ compare')}
      </button>
      {e.routable && entryCanChat(e) && (
        <button
          type='button'
          className='btn sm'
          data-testid={`model-try-${e.id}`}
          onClick={(ev) => {
            ev.stopPropagation();
            onTry(e.id);
          }}
        >
          {tr('console.models.try_btn', 'try')} ↗
        </button>
      )}
    </div>
  </div>
);

const ModelTable = ({ entries, tr, onOpen }) => (
  <div className='panel hf-table-scroll'>
    <table className='t' data-testid='models-table'>
      <thead>
        <tr>
          <th>{tr('console.models.market.th_model', 'model')}</th>
          <th>{tr('console.models.vendor', 'vendor')}</th>
          <th>{tr('console.models.market.th_input', 'input $/M')}</th>
          <th>{tr('console.models.market.th_output', 'output $/M')}</th>
          <th>{tr('console.models.market.th_cache', 'cache read $/M')}</th>
          <th>{tr('console.models.market.th_caps', 'capabilities')}</th>
          <th>{tr('console.models.market.tokens_7d', 'tokens · 7d')}</th>
          <th>{tr('console.models.perf.th_p50', 'p50 · 24h')}</th>
          <th>{tr('console.models.market.th_access', 'access')}</th>
        </tr>
      </thead>
      <tbody>
        {entries.map((e) => (
          <tr
            key={e.id}
            data-testid={`model-row-${e.id}`}
            onClick={() => onOpen(e.id)}
            style={{ cursor: 'pointer' }}
          >
            <td>
              <span
                style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}
              >
                <HfVendorIcon model={e.id} vendor={e.vendor} size={16} />
                <span className='strong mono'>{e.id}</span>
              </span>
            </td>
            <td className='muted'>{e.vendor || '—'}</td>
            <td className='mono'>
              {e.quotaType === 1
                ? `${fmtUsd(e.perCall)} ${tr('console.models.market.per_call', 'per call')}`
                : fmtUsd(e.inputPerM)}
            </td>
            <td className='mono'>
              {e.quotaType === 1 ? '—' : fmtUsd(e.outputPerM)}
            </td>
            <td className='mono muted'>{fmtUsd(e.cacheReadPerM)}</td>
            <td>
              {e.capabilities.map((c) => capLabel(tr, c)).join(' · ') || '—'}
            </td>
            <td className='mono'>{fmtCompact(e.tokens)}</td>
            <td className='mono'>{e.enoughSamples ? fmtMs(e.p50Ms) : '—'}</td>
            <td>
              <CallableBadge e={e} tr={tr} />
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  </div>
);

/**
 * @param {object} p
 * @param {Array} p.entries   buildCatalog output
 * @param {boolean} p.loading
 * @param {(id:string)=>void} p.onTry
 * @param {()=>void} p.onKeys
 * @param {(text:string)=>void} p.onCopy
 * @param {string} p.base      relay base URL for the quick start
 * @param {(id:string)=>React.ReactNode} [p.availabilityFor] root-only badge
 * @param {Function} p.tr
 */
const Marketplace = ({
  entries,
  loading,
  onTry,
  onKeys,
  onCopy,
  base,
  availabilityFor,
  tr,
}) => {
  const [q, setQ] = useState('');
  const [vendors, setVendors] = useState([]);
  const [caps, setCaps] = useState([]);
  const [routableOnly, setRoutableOnly] = useState(false);
  const [sort, setSort] = useState('popular');
  const [view, setView] = useState('list');
  const [openId, setOpenId] = useState(null);
  const [compareIds, setCompareIds] = useState([]);
  const [comparing, setComparing] = useState(false);
  const toggleCompare = (id) =>
    setCompareIds((l) =>
      l.includes(id)
        ? l.filter((x) => x !== id)
        : l.length >= MAX_COMPARE
          ? l
          : [...l, id],
    );

  const vFacets = useMemo(() => vendorFacets(entries), [entries]);
  const cFacets = useMemo(() => capabilityFacets(entries), [entries]);
  const shown = useMemo(
    () =>
      sortCatalog(
        filterCatalog(entries, {
          q,
          vendors,
          capabilities: caps,
          routableOnly,
        }),
        sort,
      ),
    [entries, q, vendors, caps, routableOnly, sort],
  );
  const open = openId ? entries.find((e) => e.id === openId) : null;
  const filtered = q || vendors.length || caps.length || routableOnly;
  // A small, unfiltered catalog gets the plain single-column layout — see
  // SMALL_CATALOG_THRESHOLD's comment. Any active filter (interactive or on
  // the URL) always keeps the full chrome, so this never hides a control
  // whose state is the reason the page looks the way it does.
  const smallCatalog =
    entries.length < SMALL_CATALOG_THRESHOLD &&
    !filtered &&
    !hasUrlFilterParams();

  return (
    <div
      className='hf-market'
      style={smallCatalog ? { gridTemplateColumns: '1fr' } : undefined}
    >
      {!smallCatalog && (
        <aside className='hf-market-rail' data-testid='models-rail'>
          <input
            className='hf-input'
            type='search'
            placeholder={tr('console.models.market.search', 'search models…')}
            value={q}
            onChange={(ev) => setQ(ev.target.value)}
            data-testid='models-search'
            style={{ width: '100%' }}
          />
          <label
            style={{
              display: 'flex',
              gap: 8,
              alignItems: 'center',
              marginTop: 12,
              fontSize: 13,
            }}
          >
            <input
              type='checkbox'
              checked={routableOnly}
              onChange={() => setRoutableOnly((v) => !v)}
              data-testid='models-callable-only'
            />
            {tr('console.models.market.callable_only', 'callable by me only')}
          </label>

          <div className='lbl' style={{ marginTop: 18, marginBottom: 4 }}>
            {tr('console.models.vendor', 'vendor')}
          </div>
          {vFacets.map(({ vendor, count }) => (
            <FacetRow
              key={vendor || '_'}
              testid={`vendor-facet-${vendor || 'unknown'}`}
              checked={vendors.includes(vendor)}
              onChange={() => setVendors((l) => toggle(l, vendor))}
              icon={<HfVendorIcon vendor={vendor} size={16} />}
              label={
                vendor || tr('console.models.unknown_vendor', 'unknown vendor')
              }
              count={count}
            />
          ))}

          {cFacets.length > 0 && (
            <>
              <div className='lbl' style={{ marginTop: 18, marginBottom: 4 }}>
                {tr('console.models.market.th_caps', 'capabilities')}
              </div>
              {cFacets.map(({ capability, count }) => (
                <FacetRow
                  key={capability}
                  testid={`cap-facet-${capability}`}
                  checked={caps.includes(capability)}
                  onChange={() => setCaps((l) => toggle(l, capability))}
                  label={capLabel(tr, capability)}
                  count={count}
                />
              ))}
            </>
          )}
        </aside>
      )}

      <section className='hf-market-main'>
        <div className='hf-market-toolbar'>
          <span className='muted' style={{ fontSize: 13 }}>
            {filtered
              ? tr('console.models.market.showing', '{{n}} of {{total}}', {
                  n: shown.length,
                  total: entries.length,
                })
              : tr('console.models.market.count', '{{n}} models', {
                  n: entries.length,
                })}
          </span>
          <span style={{ flex: 1 }} />
          {!smallCatalog && (
            <select
              className='hf-input'
              value={sort}
              onChange={(ev) => setSort(ev.target.value)}
              data-testid='models-sort'
            >
              <option value='popular'>
                {tr('console.models.market.sort_popular', 'most used')}
              </option>
              <option value='name'>
                {tr('console.models.market.sort_name', 'name')}
              </option>
              <option value='input_asc'>
                {tr('console.models.market.sort_input', 'input price ↑')}
              </option>
              <option value='output_asc'>
                {tr('console.models.market.sort_output', 'output price ↑')}
              </option>
              <option value='fastest'>
                {tr('console.models.perf.sort_fastest', 'fastest (p50)')}
              </option>
            </select>
          )}
          {!smallCatalog && (
            <div className='hf-seg'>
              {['list', 'table'].map((v) => (
                <button
                  key={v}
                  type='button'
                  className={'btn sm' + (view === v ? ' primary' : '')}
                  onClick={() => setView(v)}
                  data-testid={`models-view-${v}`}
                >
                  {v === 'list'
                    ? tr('console.models.market.view_list', 'list')
                    : tr('console.models.market.view_table', 'table')}
                </button>
              ))}
            </div>
          )}
        </div>

        {loading && entries.length === 0 ? (
          <div
            data-testid='models-loading'
            className='muted'
            style={{ padding: 40 }}
          >
            {tr('console.common.loading', 'loading…')}
          </div>
        ) : shown.length === 0 ? (
          <div
            data-testid='models-empty'
            className='muted'
            style={{ padding: 40, textAlign: 'center' }}
          >
            {entries.length === 0
              ? tr(
                  'console.models.market.empty',
                  'No models yet. Add an upstream channel and its models appear here.',
                )
              : tr(
                  'console.models.market.no_match',
                  'No model matches these filters.',
                )}
          </div>
        ) : view === 'table' ? (
          <ModelTable entries={shown} tr={tr} onOpen={setOpenId} />
        ) : (
          <div style={{ display: 'grid', gap: 12 }}>
            {shown.map((e) => (
              <ModelCard
                key={e.id}
                e={e}
                tr={tr}
                onOpen={setOpenId}
                onTry={onTry}
                availability={availabilityFor?.(e.id)}
                compared={compareIds.includes(e.id)}
                onCompare={toggleCompare}
              />
            ))}
          </div>
        )}
      </section>

      <CompareBar
        ids={compareIds}
        tr={tr}
        onOpen={() => setComparing(true)}
        onClear={() => setCompareIds([])}
      />
      {comparing && compareIds.length > 0 && (
        <CompareDrawer
          tr={tr}
          entries={compareIds
            .map((id) => entries.find((e) => e.id === id))
            .filter(Boolean)}
          onClose={() => setComparing(false)}
          onRemove={toggleCompare}
        />
      )}

      {open && (
        <ModelDrawer
          e={open}
          tr={tr}
          base={base}
          onClose={() => setOpenId(null)}
          onTry={onTry}
          onKeys={onKeys}
          onCopy={onCopy}
          availability={availabilityFor?.(open.id)}
        />
      )}
    </div>
  );
};

export default Marketplace;
