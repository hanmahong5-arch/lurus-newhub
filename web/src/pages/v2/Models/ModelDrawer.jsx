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

// The model detail drawer (openrouter.ai model page, in a drawer): prices,
// performance, capabilities with their relay paths, and a copy-paste quick
// start. Split out of Marketplace.jsx, which renders the list.

import React, { useEffect, useState } from 'react';
import HfVendorIcon from '../../../components/hifi/HfVendorIcon';
import { CAPABILITIES, fmtCompact, fmtMs, fmtPct, fmtUsd } from './catalog';

// capability → the relay path a client calls for it.
export const CAPABILITY_PATH = {
  openai: 'POST /v1/chat/completions',
  'openai-response': 'POST /v1/responses',
  anthropic: 'POST /v1/messages',
  gemini: 'POST /v1beta/models/{model}:generateContent',
  embeddings: 'POST /v1/embeddings',
  'image-generation': 'POST /v1/images/generations',
  'jina-rerank': 'POST /v1/rerank',
  'openai-video': 'POST /v1/video/generations',
};

export const capLabel = (tr, c) => {
  const [key, fallback] = CAPABILITIES[c] || [null, c];
  return key ? tr(`console.models.market.${key}`, fallback) : c;
};

export const CallableBadge = ({ e, tr }) =>
  e.routable ? (
    <span className='tag ok' data-testid={`model-callable-${e.id}`}>
      {tr('console.models.market.callable', 'callable')}
    </span>
  ) : (
    <span className='tag' data-testid={`model-not-callable-${e.id}`}>
      {tr('console.models.market.not_callable', 'not in your groups')}
    </span>
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
        {(e.p50Ms != null || e.errorRate != null) && (
          <div
            style={{
              display: 'grid',
              gridTemplateColumns: 'repeat(3, 1fr)',
              gap: 10,
              marginTop: 10,
            }}
            data-testid='model-drawer-perf'
          >
            <Stat label='p50' value={e.enoughSamples ? fmtMs(e.p50Ms) : '—'} />
            <Stat label='p95' value={e.enoughSamples ? fmtMs(e.p95Ms) : '—'} />
            <Stat
              label={tr('console.models.perf.error_rate', 'errors')}
              value={e.enoughSamples ? fmtPct(e.errorRate) : '—'}
              sub={
                e.enoughSamples
                  ? tr('console.models.perf.window', 'last 24h, your tenant')
                  : tr(
                      'console.models.perf.thin',
                      'too little traffic to measure',
                    )
              }
            />
          </div>
        )}
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

export default ModelDrawer;
