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
import React, { useMemo, useState } from 'react';
import HFShell from '../../../components/hifi/HFShell';
import { getServerAddress } from '../../../helpers';

/* HiFi 15 — States: empty / loading / error / mobile / modal. Ported from hifi/hf15-states.jsx. */

const VIEWS = [
  ['empty', 'Empty · first run'],
  ['loading', 'Loading'],
  ['error', 'Error · upstream'],
  ['mobile', 'Mobile · log trace'],
  ['modal', 'Modal · explain query'],
];

const HFStates = () => {
  const [view, setView] = useState('empty');
  // Same rule as the Token and Dashboard pages: never print a hardcoded relay
  // host. This page's empty state used 'https://api.lurus.cn', retired in
  // 2026-04 — a copy-pasteable command that cannot connect.
  const relayBaseUrl = useMemo(
    () => `${getServerAddress().replace(/\/+$/, '')}/v1`,
    [],
  );
  return (
    <HFShell
      active='logs'
      crumbs={['states', VIEWS.find((v) => v[0] === view)[1]]}
      actions={VIEWS.map(([k, l]) => (
        <button
          key={k}
          type='button'
          onClick={() => setView(k)}
          className={'btn ' + (view === k ? 'primary' : '')}
        >
          {l}
        </button>
      ))}
    >
      {view === 'empty' && (
        <div
          style={{
            padding: '80px 28px',
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'center',
            textAlign: 'center',
          }}
        >
          <div
            className='hf-paper-grid'
            style={{
              width: 240,
              height: 160,
              opacity: 0.5,
              marginBottom: 24,
              border: '1px dashed var(--hf-rule-strong)',
            }}
          />
          <div className='lbl'>no logs yet</div>
          <h1
            className='display'
            style={{ fontSize: 36, margin: '8px 0 8px', maxWidth: 480 }}
          >
            Make your first call. Logs land here in seconds.
          </h1>
          <div className='muted' style={{ maxWidth: 460, fontSize: 13 }}>
            everything that flows through Lurus Hub is captured: prompts,
            responses, latency, cost. searchable in &lt;100ms.
          </div>
          <div style={{ display: 'flex', gap: 10, marginTop: 24 }}>
            <button type='button' className='btn primary'>
              create a token →
            </button>
            <button type='button' className='btn'>
              view docs ↗
            </button>
          </div>
          <div
            className='panel-paper mono'
            style={{
              marginTop: 32,
              padding: 16,
              fontSize: 11,
              color: 'var(--hf-ink-2)',
              textAlign: 'left',
              maxWidth: 560,
            }}
          >
            <span className='muted'># test it from your terminal</span>
            {'\n'}
            curl {relayBaseUrl}/chat/completions \\{'\n'}
            {'  '}-H "Authorization: Bearer $LURUS_KEY" \\{'\n'}
            {'  '}-d '{'{'}"model":"gpt-4o","messages":[{'{'}
            "role":"user","content":"hi"{'}'}]{'}'}'
          </div>
        </div>
      )}

      {view === 'loading' && (
        <>
          <div className='hf-page-head'>
            <div>
              <div className='lbl' style={{ marginBottom: 6 }}>
                requests
              </div>
              <div style={{ display: 'flex', gap: 16, alignItems: 'center' }}>
                <div className='display' style={{ fontSize: 36, opacity: 0.4 }}>
                  —
                </div>
                <div className='muted'>
                  streaming · last 1 hour · meilisearch indexed
                </div>
              </div>
            </div>
          </div>
          <div style={{ padding: 24 }}>
            {[0, 1, 2, 3, 4, 5, 6, 7].map((i) => (
              <div
                key={i}
                style={{
                  height: 44,
                  marginBottom: 8,
                  background:
                    'linear-gradient(90deg, var(--hf-paper) 0%, var(--hf-sunken) 50%, var(--hf-paper) 100%)',
                  backgroundSize: '200% 100%',
                  animation: `hf-shimmer 1.4s ${i * 0.08}s infinite linear`,
                  border: '1px solid var(--hf-rule)',
                }}
              />
            ))}
            <style>{`@keyframes hf-shimmer { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }`}</style>
          </div>
        </>
      )}

      {view === 'error' && (
        <div
          style={{
            padding: '60px 28px',
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'center',
            textAlign: 'center',
          }}
        >
          <div
            style={{
              width: 64,
              height: 64,
              border: '2px solid var(--hf-err)',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              fontSize: 28,
              color: 'var(--hf-err)',
              marginBottom: 18,
            }}
          >
            !
          </div>
          <div className='lbl' style={{ color: 'var(--hf-err)' }}>
            upstream unreachable
          </div>
          <h1
            className='display'
            style={{ fontSize: 36, margin: '8px 0 8px', maxWidth: 560 }}
          >
            Can't reach the log indexer right now.
          </h1>
          <div className='muted' style={{ maxWidth: 480, fontSize: 13 }}>
            this affects the live feed only · already-indexed logs are still
            readable below
          </div>
          <div
            className='panel-paper mono'
            style={{
              marginTop: 22,
              padding: 14,
              fontSize: 11,
              color: 'var(--hf-ink-2)',
              textAlign: 'left',
            }}
          >
            <div>
              <span className='muted'>error.code:</span>{' '}
              <span style={{ color: 'var(--hf-err)' }}>ECONNREFUSED</span>
            </div>
            <div>
              <span className='muted'>retry.in:</span> 12s · attempt 3 / ∞
            </div>
            <div>
              <span className='muted'>trace.id:</span> 1f4a-e90c-…
            </div>
          </div>
          <div style={{ display: 'flex', gap: 10, marginTop: 24 }}>
            <button type='button' className='btn primary'>
              retry now
            </button>
            <button type='button' className='btn'>
              view status page ↗
            </button>
            <button type='button' className='btn'>
              read cached logs
            </button>
          </div>
        </div>
      )}

      {view === 'mobile' && (
        <div
          style={{
            padding: '40px 28px',
            display: 'flex',
            justifyContent: 'center',
          }}
        >
          <div
            style={{
              width: 390,
              height: 760,
              background: 'var(--hf-elev)',
              border: '1px solid var(--hf-rule-strong)',
              borderRadius: 28,
              boxShadow: '0 24px 56px rgba(0,0,0,0.18)',
              padding: 14,
              display: 'flex',
              flexDirection: 'column',
            }}
          >
            <div
              style={{
                alignSelf: 'center',
                width: 100,
                height: 24,
                background: 'var(--hf-ink)',
                borderRadius: 0,
                marginBottom: 12,
              }}
            />
            <div
              style={{
                padding: '4px 8px 12px',
                borderBottom: '1px solid var(--hf-rule)',
              }}
            >
              <div className='lbl'>requests · last 1h</div>
              <div className='display' style={{ fontSize: 26, marginTop: 2 }}>
                206 errors
              </div>
              <div className='muted mono' style={{ fontSize: 10 }}>
                across 12,481 requests
              </div>
              <div
                style={{
                  display: 'flex',
                  gap: 6,
                  marginTop: 10,
                  overflowX: 'auto',
                }}
              >
                <span className='pill'>
                  <span className='dot ok' /> all
                </span>
                <span className='pill'>
                  <span className='dot err' /> 5xx
                </span>
                <span className='pill'>
                  <span className='dot warn' /> 4xx
                </span>
                <span className='pill'>gpt-4o</span>
              </div>
            </div>
            <div style={{ flex: 1, overflow: 'auto' }}>
              {[
                ['14:02:11', 'gpt-4o-mini', 200, '847t · $0.0042'],
                ['14:02:09', 'claude-3.5-sonnet', 200, '312t · $0.0090'],
                ['14:02:07', 'gpt-4o', 504, 'upstream_timeout'],
                ['14:02:05', 'gemini-1.5-pro', 200, '621t · $0.0031'],
                ['14:02:03', 'gpt-4o-mini', 200, '210t · $0.0011'],
                ['14:02:01', 'gpt-4o', 504, 'upstream_timeout'],
                ['14:01:58', 'claude-3.5', 200, '480t · $0.0072'],
              ].map((r, i) => (
                <div
                  key={i}
                  style={{
                    padding: '12px 8px',
                    borderBottom: '1px solid var(--hf-rule)',
                  }}
                >
                  <div
                    style={{ display: 'flex', alignItems: 'baseline', gap: 8 }}
                  >
                    <span className={'tag ' + (r[2] >= 500 ? 'err' : 'ok')}>
                      {r[2]}
                    </span>
                    <span className='strong' style={{ fontSize: 12 }}>
                      {r[1]}
                    </span>
                    <span
                      className='faint mono'
                      style={{ fontSize: 10, marginLeft: 'auto' }}
                    >
                      {r[0]}
                    </span>
                  </div>
                  <div
                    className='muted mono'
                    style={{ fontSize: 10, marginTop: 4 }}
                  >
                    {r[3]}
                  </div>
                </div>
              ))}
            </div>
            <div
              style={{
                display: 'flex',
                justifyContent: 'space-around',
                padding: '8px 0',
                borderTop: '1px solid var(--hf-rule)',
              }}
            >
              {['▣', '◇', '≣', '⚿', '✱'].map((g, i) => (
                <div
                  key={i}
                  style={{
                    fontSize: 16,
                    color: i === 2 ? 'var(--hf-accent)' : 'var(--hf-ink-3)',
                  }}
                >
                  {g}
                </div>
              ))}
            </div>
          </div>
        </div>
      )}

      {view === 'modal' && (
        <div style={{ position: 'relative', height: '100%' }}>
          <div style={{ padding: 28, opacity: 0.4 }}>
            <div
              className='hf-page-head'
              style={{ padding: 0, borderBottom: 0 }}
            >
              <div>
                <div className='lbl' style={{ marginBottom: 6 }}>
                  requests
                </div>
                <h1>206 errors</h1>
              </div>
            </div>
          </div>

          <div
            style={{
              position: 'absolute',
              inset: 0,
              background: 'rgba(10,9,8,0.4)',
              backdropFilter: 'blur(2px)',
              display: 'flex',
              justifyContent: 'center',
              paddingTop: 80,
            }}
          >
            <div
              style={{
                width: 640,
                background: 'var(--hf-elev)',
                border: '1px solid var(--hf-rule-strong)',
                boxShadow: '0 32px 72px rgba(0,0,0,0.28)',
                maxHeight: 600,
                display: 'flex',
                flexDirection: 'column',
              }}
            >
              <div
                style={{
                  padding: 22,
                  borderBottom: '1px solid var(--hf-rule)',
                }}
              >
                <div className='lbl'>explain query</div>
                <div className='display' style={{ fontSize: 22, marginTop: 4 }}>
                  What I'm searching for
                </div>
              </div>
              <div style={{ padding: 22, overflow: 'auto' }}>
                <div
                  className='panel-paper mono'
                  style={{ padding: 14, fontSize: 12, lineHeight: 1.7 }}
                >
                  <span style={{ color: 'var(--hf-info)' }}>model</span>
                  <span className='muted'>:</span>
                  <span className='strong'> gpt-4o</span>{' '}
                  <span style={{ color: 'var(--hf-accent)' }}>AND</span>{' '}
                  <span style={{ color: 'var(--hf-info)' }}>status</span>
                  <span className='muted'>:</span>
                  <span className='strong'> 5xx</span>{' '}
                  <span style={{ color: 'var(--hf-accent)' }}>AND</span>{' '}
                  <span style={{ color: 'var(--hf-info)' }}>tenant</span>
                  <span className='muted'>:</span>
                  <span className='strong'> acme</span>{' '}
                  <span style={{ color: 'var(--hf-accent)' }}>AND</span>{' '}
                  <span style={{ color: 'var(--hf-info)' }}>duration</span>
                  <span className='muted'>:&gt;</span>
                  <span className='strong'>2000ms</span>
                </div>
                <div style={{ marginTop: 18 }}>
                  <div className='lbl'>in plain english</div>
                  <p style={{ fontSize: 13, lineHeight: 1.6, marginTop: 6 }}>
                    Find requests where the <b>model</b> is <b>gpt-4o</b>, the{' '}
                    <b>HTTP status</b> indicates a server error (500–599), the{' '}
                    <b>tenant</b> is <b>acme</b>, and the{' '}
                    <b>request took longer</b> than 2 seconds.
                  </p>
                </div>
                <div style={{ marginTop: 18 }}>
                  <div className='lbl'>matched</div>
                  <div
                    style={{
                      display: 'flex',
                      alignItems: 'baseline',
                      gap: 12,
                      marginTop: 4,
                    }}
                  >
                    <span
                      className='display'
                      style={{ fontSize: 32, color: 'var(--hf-err)' }}
                    >
                      142
                    </span>
                    <span className='muted'>
                      requests · 1.14% of traffic in window
                    </span>
                  </div>
                </div>
              </div>
              <div
                style={{
                  padding: '14px 22px',
                  borderTop: '1px solid var(--hf-rule)',
                  display: 'flex',
                  gap: 10,
                }}
              >
                <button type='button' className='btn'>
                  save query
                </button>
                <button type='button' className='btn'>
                  create alert from this
                </button>
                <span style={{ flex: 1 }} />
                <button type='button' className='btn'>
                  close
                </button>
                <button type='button' className='btn primary'>
                  apply
                </button>
              </div>
            </div>
          </div>
        </div>
      )}
    </HFShell>
  );
};

export default HFStates;
