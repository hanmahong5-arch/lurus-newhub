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
import { useTranslation } from 'react-i18next';
// Same barrel Token/index.jsx uses for the same function — every page test
// in this codebase already mocks '../../../helpers' as one module boundary;
// importing the un-barrelled helpers/token.js instead would bypass that
// mock and drag in Semi UI's toast/lottie stack in every test that renders
// this panel without its own bespoke mock.
import { getServerAddress } from '../../../helpers';
import { buildSnippets, langTabsFor } from '../Token/snippets';

// ViewCodePanel — Playground's "view code" — reuses Token/snippets.js
// (buildSnippets) so the exported curl/python/node here is byte-identical
// in shape to what the Token page itself ships, just filled in with the
// column's own model, system prompt, user message and sampling params
// instead of Token's fixed "hi" turn.
//
// The key is ALWAYS the literal placeholder YOUR_KEY — this component has
// no token in scope (Playground authenticates the run itself, server-side,
// through the operator's session), and even if it did, embedding a real key
// in exportable example code is the one thing this snippet must never do.
const ViewCodePanel = ({ model, system, user, params, anthropicModel }) => {
  const { t: tr } = useTranslation();
  const [lang, setLang] = useState('curl');

  const host = useMemo(() => getServerAddress().replace(/\/+$/, ''), []);
  const snippetMap = useMemo(
    () =>
      buildSnippets('YOUR_KEY', host, {
        openaiModel: model,
        anthropicModel,
        system,
        user,
        params,
      }),
    [host, model, anthropicModel, system, user, params],
  );

  const tabs = langTabsFor(anthropicModel);
  // The anthropic tab can disappear out from under the current selection if
  // the column's model changes to one that no longer speaks the anthropic
  // wire — fall back to curl rather than rendering an empty snippet.
  const activeLang = tabs.some(([k]) => k === lang) ? lang : 'curl';

  const copySnippet = () => {
    try {
      navigator.clipboard?.writeText(snippetMap[activeLang]);
    } catch (_) {
      /* no clipboard, no toast host reachable from here — the copy button
         itself is the only affordance. */
    }
  };

  return (
    <div
      className='panel'
      data-testid='playground-view-code-panel'
      style={{ marginTop: 14, padding: 0 }}
    >
      <div
        style={{
          display: 'flex',
          gap: 0,
          borderBottom: '1px solid var(--hf-rule)',
        }}
      >
        {tabs.map(([k, l]) => (
          <button
            key={k}
            type='button'
            data-testid={`view-code-tab-${k}`}
            onClick={() => setLang(k)}
            style={{
              padding: '10px 16px',
              border: 0,
              background: 'transparent',
              cursor: 'pointer',
              fontFamily: 'var(--hf-mono)',
              fontSize: 11,
              color: activeLang === k ? 'var(--hf-ink)' : 'var(--hf-ink-3)',
              borderBottom:
                activeLang === k
                  ? '2px solid var(--hf-accent)'
                  : '2px solid transparent',
              marginBottom: -1,
            }}
          >
            {l}
          </button>
        ))}
        <span style={{ flex: 1 }} />
        <button
          type='button'
          className='btn ghost sm'
          style={{ alignSelf: 'center', marginRight: 8 }}
          onClick={copySnippet}
        >
          {tr('console.common.copy', 'copy')} ⧉
        </button>
      </div>
      <pre
        className='mono'
        data-testid='view-code-snippet'
        style={{
          margin: 0,
          padding: 16,
          fontSize: 11,
          color: 'var(--hf-ink-2)',
          whiteSpace: 'pre',
          overflow: 'auto',
          maxHeight: 260,
        }}
      >
        {snippetMap[activeLang]}
      </pre>
    </div>
  );
};

export default ViewCodePanel;
