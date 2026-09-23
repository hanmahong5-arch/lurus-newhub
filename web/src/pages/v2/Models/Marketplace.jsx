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

import React, { useEffect, useMemo, useState } from 'react';
import HfVendorIcon from '../../../components/hifi/HfVendorIcon';
import {
  CAPABILITIES,
  capabilityFacets,
  filterCatalog,
  fmtCompact,
  fmtUsd,
  sortCatalog,
  vendorFacets,
} from './catalog';
import { CompareBar, CompareDrawer, MAX_COMPARE } from './Compare';

// capability → the relay path a client calls for it.
const CAPABILITY_PATH = {
  openai: 'POST /v1/chat/completions',
  'openai-response': 'POST /v1/responses',
  anthropic: 'POST /v1/messages',
  gemini: 'POST /v1beta/models/{model}:generateContent',
  embeddings: 'POST /v1/embeddings',
  'image-generation': 'POST /v1/images/generations',
  'jina-rerank': 'POST /v1/rerank',
  'openai-video': 'POST /v1/video/generations',
};

const capLabel = (tr, c) => {
  const [key, fallback] = CAPABILITIES[c] || [null, c];
  return key ? tr(`console.models.market.${key}`, fallback) : c;
};

const PriceLine = ({ e, tr }) => {
  if (!e.priced) {
    return (
      <span className='muted'>
        {tr('console.models.market.unpriced', 'no price configured')}
      </span>
    );
  }
  if (e.quotaType === 1) {
    return (
      <span>
        <b className='mono'>{fmtUsd(e.perCall)}</b>{' '}
        {tr('console.models.market.per_call', 'per call')}
      </span>
    );
  }
  return (
    <span style={{ display: 'inline-flex', gap: 14, flexWrap: 'wrap' }}>
      <span>
        <b className='mono'>{fmtUsd(e.inputPerM)}</b>
        {tr('console.models.market.per_m_input', '/M input')}
      </span>
      <span>
        <b className='mono'>{fmtUsd(e.outputPerM)}</b>
        {tr('console.models.market.per_m_output', '/M output')}
      </span>
      {e.cacheReadPerM != null && (
        <span className='muted'>
          {tr('console.models.market.cache_read', 'cache read')}{' '}
          <span className='mono'>{fmtUsd(e.cacheReadPerM)}</span>
          {tr('console.models.market.per_m', '/M')}
        </span>
      )}
    </span>
  );
};

const CallableBadge = ({ e, tr }) =>
  e.routable ? (
    <span className='tag ok' data-testid={`model-callable-${e.id}`}>
      {tr('console.models.market.callable', 'callable')}
    </span>
  ) : (
    <span className='tag' data-testid={`model-not-callable-${e.id}`}>
      {tr('console.models.market.not_callable', 'not in your groups')}
    </span>
  );

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
      <div style={{ textAlign: 'right' }}>
        <div className='mono strong' style={{ fontSize: 13 }}>
          {fmtCompact(e.tokens)}
        </div>
        <div className='faint' style={{ fontSize: 11 }}>
          {tr('console.models.market.tokens_7d', 'tokens · 7d')}
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
      {e.routable && (
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
            <td>
              <CallableBadge e={e} tr={tr} />
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  </div>
);

const Stat = ({ label, value, sub }) => (
  <div className='panel' style={{ padding: '12px 14px' }}>
    <div className='lbl'>{label}</div>
    <div className='mono strong' style={{ fontSize: 18, marginTop: 6 }}>
      {value}
    </div>
    {sub && (
      <div className='faint' style={{ fontSize: 11, marginTop: 2 }}>
        {sub}
      </div>
    )}
  </div>
);

const quickStart = (e, base) => {
  if (
    e.capabilities.includes('embeddings') &&
    !e.capabilities.includes('openai')
  ) {
    return `curl ${base}/v1/embeddings \\
  -H "Authorization: Bearer $LURUS_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"model": "${e.id}", "input": "hello"}'`;
  }
  return `curl ${base}/v1/chat/completions \\
  -H "Authorization: Bearer $LURUS_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"model": "${e.id}", "messages": [{"role": "user", "content": "hello"}]}'`;
};

const quickStartPython = (e, base) => `from openai import OpenAI

client = OpenAI(base_url="${base}/v1", api_key="YOUR_LURUS_API_KEY")
resp = client.chat.completions.create(
    model="${e.id}",
    messages=[{"role": "user", "content": "hello"}],
)
print(resp.choices[0].message.content)`;

const ModelDrawer = ({
  e,
  tr,
  onClose,
  onTry,
  onKeys,
  onCopy,
  base,
  availability,
}) => {
  const [tab, setTab] = useState('curl');
  useEffect(() => {
    const onKey = (ev) => ev.key === 'Escape' && onClose();
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);
  const code = tab === 'curl' ? quickStart(e, base) : quickStartPython(e, base);
  return (
    <div
      className='hf-drawer-backdrop'
      onClick={onClose}
      data-testid='model-drawer-backdrop'
    >
      <aside
        className='hf-drawer'
        role='dialog'
        aria-label={e.id}
        data-testid='model-drawer'
        onClick={(ev) => ev.stopPropagation()}
      >
        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          <HfVendorIcon model={e.id} vendor={e.vendor} size={36} />
          <div style={{ flex: 1, minWidth: 0 }}>
            <div className='display' style={{ fontSize: 24 }}>
              {e.id}
            </div>
            <div className='muted' style={{ fontSize: 12 }}>
              {e.vendor ||
                tr('console.models.unknown_vendor', 'unknown vendor')}
            </div>
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

        <div
          style={{ display: 'flex', gap: 8, marginTop: 12, flexWrap: 'wrap' }}
        >
          <CallableBadge e={e} tr={tr} />
          {e.tags.map((t) => (
            <span key={t} className='tag'>
              {t}
            </span>
          ))}
          {availability}
        </div>

        <p style={{ fontSize: 14, lineHeight: 1.6, marginTop: 14 }}>
          {e.description ||
            tr(
              'console.models.market.no_description',
              'No description yet — an administrator can add one to the model catalogue.',
            )}
        </p>

        <div
          style={{
            display: 'grid',
            gridTemplateColumns: 'repeat(2, 1fr)',
            gap: 10,
            marginTop: 8,
          }}
          data-testid='model-drawer-prices'
        >
          {e.quotaType === 1 ? (
            <Stat
              label={tr(
                'console.models.market.price_per_call',
                'price per call',
              )}
              value={fmtUsd(e.perCall)}
            />
          ) : (
            <>
              <Stat
                label={tr('console.models.market.th_input', 'input $/M')}
                value={fmtUsd(e.inputPerM)}
                sub={tr('console.models.market.per_million', 'per 1M tokens')}
              />
              <Stat
                label={tr('console.models.market.th_output', 'output $/M')}
                value={fmtUsd(e.outputPerM)}
                sub={tr('console.models.market.per_million', 'per 1M tokens')}
              />
              <Stat
                label={tr('console.models.market.th_cache', 'cache read $/M')}
                value={fmtUsd(e.cacheReadPerM)}
              />
            </>
          )}
          <Stat
            label={tr('console.models.market.tokens_7d', 'tokens · 7d')}
            value={fmtCompact(e.tokens)}
            sub={`${fmtCompact(e.requests)} ${tr('console.models.market.requests', 'requests')}`}
          />
        </div>
        <div className='faint' style={{ fontSize: 11, marginTop: 6 }}>
          {tr(
            'console.models.market.price_note',
            'Prices include your group multiplier.',
          )}
        </div>

        <div className='lbl' style={{ marginTop: 18 }}>
          {tr('console.models.market.th_caps', 'capabilities')}
        </div>
        <div style={{ marginTop: 6 }}>
          {e.capabilities.length === 0 && (
            <span className='muted' style={{ fontSize: 13 }}>
              —
            </span>
          )}
          {e.capabilities.map((c) => (
            <div
              key={c}
              style={{
                display: 'flex',
                justifyContent: 'space-between',
                padding: '6px 0',
                borderBottom: '1px solid var(--hf-rule)',
                fontSize: 13,
              }}
            >
              <span>{capLabel(tr, c)}</span>
              <span className='mono muted' style={{ fontSize: 12 }}>
                {CAPABILITY_PATH[c] || ''}
              </span>
            </div>
          ))}
        </div>

        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 6,
            marginTop: 18,
          }}
        >
          <span className='lbl' style={{ flex: 1 }}>
            {tr('console.models.market.quick_start', 'quick start')}
          </span>
          {['curl', 'python'].map((k) => (
            <button
              key={k}
              type='button'
              className={'btn sm' + (tab === k ? ' primary' : '')}
              onClick={() => setTab(k)}
            >
              {k === 'curl' ? 'cURL' : 'Python'}
            </button>
          ))}
          <button
            type='button'
            className='btn sm'
            data-testid='model-drawer-copy-code'
            onClick={() => onCopy(code)}
          >
            {tr('console.common.copy', 'copy')}
          </button>
        </div>
        <pre className='hf-code' data-testid='model-drawer-code'>
          {code}
        </pre>

        <div style={{ display: 'flex', gap: 8, marginTop: 16 }}>
          {e.routable && (
            <button
              type='button'
              className='btn primary'
              data-testid='model-drawer-try'
              onClick={() => onTry(e.id)}
            >
              {tr(
                'console.models.market.open_playground',
                'open in playground',
              )}{' '}
              ↗
            </button>
          )}
          <button type='button' className='btn' onClick={onKeys}>
            {tr('console.models.market.get_key', 'get an API key')}
          </button>
          <button
            type='button'
            className='btn'
            data-testid='model-drawer-copy-id'
            onClick={() => onCopy(e.id)}
          >
            {tr('console.models.market.copy_id', 'copy model id')}
          </button>
        </div>
      </aside>
    </div>
  );
};

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

  return (
    <div className='hf-market'>
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
          </select>
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
