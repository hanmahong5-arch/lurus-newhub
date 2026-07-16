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

/* HiFi 14 — Design System UI Kit. Ported from hifi/hf14-design-system.jsx. */

const SWATCHES = [
  ['paper', 'var(--hf-paper)', '--hf-paper'],
  ['bg', 'var(--hf-bg)', '--hf-bg'],
  ['sunken', 'var(--hf-sunken)', '--hf-sunken'],
  ['elev', 'var(--hf-elev)', '--hf-elev'],
  ['rule', 'var(--hf-rule)', '--hf-rule'],
  ['ink', 'var(--hf-ink)', '--hf-ink'],
  ['accent', 'var(--hf-accent)', '--hf-accent'],
  ['accent-2', 'var(--hf-accent-2)', '--hf-accent-2'],
  ['ok', 'var(--hf-ok)', '--hf-ok'],
  ['warn', 'var(--hf-warn)', '--hf-warn'],
  ['err', 'var(--hf-err)', '--hf-err'],
  ['info', 'var(--hf-info)', '--hf-info'],
];

const HFDesignSystem = () => (
  <div
    className='hf'
    style={{ background: 'var(--hf-bg)', minHeight: '100vh', overflow: 'auto' }}
  >
    <div
      style={{
        padding: '40px 56px',
        borderBottom: '1px solid var(--hf-rule)',
        background: 'var(--hf-paper)',
      }}
    >
      <div className='lbl'>design system</div>
      <h1
        className='display'
        style={{ fontSize: 56, margin: '8px 0 4px', letterSpacing: '-0.03em' }}
      >
        Lurus Hub · UI Kit
      </h1>
      <div className='muted' style={{ fontSize: 14 }}>
        v1 · editorial dev-tool aesthetic · light + dark
      </div>
    </div>

    <div
      style={{ padding: '40px 56px', borderBottom: '1px solid var(--hf-rule)' }}
    >
      <div className='lbl'>type</div>
      <h2 className='display' style={{ fontSize: 28, margin: '4px 0 22px' }}>
        Display, sans, mono — three voices
      </h2>
      <div
        style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(3, 1fr)',
          gap: 22,
        }}
      >
        <div className='panel' style={{ padding: 22 }}>
          <div className='lbl'>display · Fraunces</div>
          <div
            className='display'
            style={{ fontSize: 56, lineHeight: 1, margin: '12px 0 4px' }}
          >
            Aa
          </div>
          <div className='muted mono' style={{ fontSize: 10 }}>
            500 weight · -0.025em tracking
          </div>
          <div
            style={{
              display: 'flex',
              flexDirection: 'column',
              gap: 10,
              marginTop: 18,
            }}
          >
            <div className='display' style={{ fontSize: 36 }}>
              Hi-fi headline
            </div>
            <div className='display' style={{ fontSize: 22 }}>
              Section title
            </div>
            <div className='display' style={{ fontSize: 16 }}>
              Card title
            </div>
          </div>
        </div>
        <div className='panel' style={{ padding: 22 }}>
          <div className='lbl'>sans · Inter Tight</div>
          <div
            style={{
              fontFamily: 'var(--hf-sans)',
              fontSize: 56,
              fontWeight: 500,
              lineHeight: 1,
              margin: '12px 0 4px',
            }}
          >
            Aa
          </div>
          <div className='muted mono' style={{ fontSize: 10 }}>
            13px body · 1.5 leading
          </div>
          <div style={{ marginTop: 18, fontSize: 13, lineHeight: 1.55 }}>
            Body copy size for descriptions and explanations. The <b>strong</b>{' '}
            variant is regular weight; the <span className='muted'>muted</span>{' '}
            variant is for labels; <span className='faint'>faint</span> for
            hints.
          </div>
        </div>
        <div className='panel' style={{ padding: 22 }}>
          <div className='lbl'>mono · JetBrains Mono</div>
          <div
            className='mono'
            style={{ fontSize: 56, lineHeight: 1, margin: '12px 0 4px' }}
          >
            Aa
          </div>
          <div className='muted mono' style={{ fontSize: 10 }}>
            11px in tables · 10px in tags
          </div>
          <div
            className='mono'
            style={{ marginTop: 18, fontSize: 11, lineHeight: 1.7 }}
          >
            <div>14:02:11.481 · 200 · gpt-4o-mini</div>
            <div>14:02:09.220 · 200 · claude-3.5-sonnet</div>
            <div>14:02:07.014 · 504 · gpt-4o</div>
            <div>14:02:05.901 · 200 · gemini-1.5-pro</div>
          </div>
        </div>
      </div>
    </div>

    <div
      style={{ padding: '40px 56px', borderBottom: '1px solid var(--hf-rule)' }}
    >
      <div className='lbl'>color</div>
      <h2 className='display' style={{ fontSize: 28, margin: '4px 0 22px' }}>
        Paper, ink, signal
      </h2>
      <div
        style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(6, 1fr)',
          gap: 12,
        }}
      >
        {SWATCHES.map((s, i) => (
          <div key={i}>
            <div
              style={{
                height: 60,
                background: s[1],
                border: '1px solid var(--hf-rule)',
              }}
            />
            <div className='lbl' style={{ marginTop: 6 }}>
              {s[0]}
            </div>
            <div className='faint mono' style={{ fontSize: 9 }}>
              {s[2]}
            </div>
          </div>
        ))}
      </div>
    </div>

    <div
      style={{ padding: '40px 56px', borderBottom: '1px solid var(--hf-rule)' }}
    >
      <div className='lbl'>controls</div>
      <h2 className='display' style={{ fontSize: 28, margin: '4px 0 22px' }}>
        Buttons, fields, kbd
      </h2>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 22 }}>
        <div className='panel' style={{ padding: 22 }}>
          <div className='lbl' style={{ marginBottom: 10 }}>
            buttons
          </div>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8 }}>
            <button type='button' className='btn primary'>
              primary
            </button>
            <button type='button' className='btn acc'>
              accent
            </button>
            <button type='button' className='btn'>
              secondary
            </button>
            <button type='button' className='btn ghost'>
              ghost
            </button>
            <button type='button' className='btn sm primary'>
              sm primary
            </button>
            <button type='button' className='btn sm'>
              sm
            </button>
            <button
              type='button'
              className='btn'
              disabled
              style={{ opacity: 0.4 }}
            >
              disabled
            </button>
          </div>

          <div className='lbl' style={{ margin: '20px 0 10px' }}>
            fields
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
            <div className='field' style={{ width: 280 }}>
              <span className='muted'>placeholder…</span>
            </div>
            <div className='field' style={{ width: 280 }}>
              <span className='strong'>filled value</span>
            </div>
          </div>

          <div className='lbl' style={{ margin: '20px 0 10px' }}>
            keyboard
          </div>
          <div style={{ display: 'flex', gap: 4, alignItems: 'center' }}>
            <span className='kbd'>⌘</span>
            <span className='kbd'>K</span>
            <span className='muted' style={{ marginLeft: 8 }}>
              open palette
            </span>
          </div>
        </div>

        <div className='panel' style={{ padding: 22 }}>
          <div className='lbl' style={{ marginBottom: 10 }}>
            tags & pills
          </div>
          <div
            style={{
              display: 'flex',
              flexWrap: 'wrap',
              gap: 6,
              marginBottom: 14,
            }}
          >
            <span className='tag ok'>ok</span>
            <span className='tag warn'>warn</span>
            <span className='tag err'>err</span>
            <span className='tag info'>info</span>
            <span className='tag acc'>accent</span>
            <span className='tag solid'>solid</span>
          </div>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
            <span className='pill'>
              <span className='dot ok' /> healthy
            </span>
            <span className='pill'>
              <span className='dot warn' /> degraded
            </span>
            <span className='pill'>
              <span className='dot err' /> down
            </span>
            <span className='pill'>200ms</span>
            <span className='pill'>$0.0042</span>
          </div>

          <div className='lbl' style={{ margin: '20px 0 10px' }}>
            data viz
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            <span className='spark acc'>
              {[3, 5, 8, 9, 12, 18, 22, 28, 35, 42, 38, 40, 36, 34].map(
                (v, i) => (
                  <i key={i} style={{ height: v + 'px' }} />
                ),
              )}
            </span>
            <span className='spark ok'>
              {[8, 9, 10, 11, 9, 8, 7, 9, 10, 11, 12, 11, 10, 9, 8, 9].map(
                (v, i) => (
                  <i key={i} style={{ height: v * 1.5 + 'px' }} />
                ),
              )}
            </span>
            <span className='spark err'>
              {[2, 3, 2, 3, 4, 5, 6, 8, 9, 11, 12, 10, 8].map((v, i) => (
                <i key={i} style={{ height: v * 1.8 + 'px' }} />
              ))}
            </span>
          </div>
        </div>
      </div>
    </div>

    <div style={{ padding: '40px 56px' }}>
      <div className='lbl'>patterns</div>
      <h2 className='display' style={{ fontSize: 28, margin: '4px 0 22px' }}>
        Tables, panels, KPIs
      </h2>
      <div className='panel' style={{ padding: 22, marginBottom: 22 }}>
        <div
          style={{
            display: 'grid',
            gridTemplateColumns: 'repeat(4, 1fr)',
            gap: 22,
          }}
        >
          {[
            ['avg ttft', '412ms', 'var(--hf-ok)'],
            ['p95 dur', '2.10s', 'var(--hf-ink)'],
            ['error rate', '1.65%', 'var(--hf-err)'],
            ['hot model', 'gpt-4o', 'var(--hf-ink)'],
          ].map(([l, v, c], i) => (
            <div key={i}>
              <div className='lbl'>{l}</div>
              <div
                className='display'
                style={{ fontSize: 32, color: c, marginTop: 2 }}
              >
                {v}
              </div>
            </div>
          ))}
        </div>
      </div>
      <div className='panel'>
        <div className='hf-table-scroll'>
          <table className='t'>
            <thead>
              <tr>
                <th>timestamp</th>
                <th>model</th>
                <th>status</th>
                <th>cost</th>
              </tr>
            </thead>
            <tbody>
              {[
                ['14:02:11', 'gpt-4o-mini', 200, 0.0042],
                ['14:02:09', 'claude-3.5', 200, 0.009],
                ['14:02:07', 'gpt-4o', 504, 0],
              ].map((r, i) => (
                <tr key={i}>
                  <td className='mono muted'>{r[0]}</td>
                  <td className='strong'>{r[1]}</td>
                  <td>
                    <span className={'tag ' + (r[2] >= 500 ? 'err' : 'ok')}>
                      {r[2]}
                    </span>
                  </td>
                  <td className='mono'>{r[3] ? '$' + r[3].toFixed(4) : '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  </div>
);

export default HFDesignSystem;
