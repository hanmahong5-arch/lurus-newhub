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
import React, { useRef } from 'react';
import { useTranslation } from 'react-i18next';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';

// HfMarkdown — the one markdown renderer for /console/v2/ model output
// (Chat's assistant turns, Playground's output columns). Deliberately NOT
// components/common/markdown/MarkdownRenderer: that one drags in mermaid,
// katex, highlight.js and its own github-theme CSS to support the legacy
// console's full feature set — heavy, and off the hifi visual language.
// This component only takes remark-gfm (tables/strikethrough/task lists) on
// top of the CommonMark react-markdown already parses; there is no
// rehype-raw plugin, so raw HTML written in the source text (a stray
// `<script>`, an `<img onerror=...>`) is escaped to plain text rather than
// turned into real DOM nodes — react-markdown's default behaviour, not
// anything this file adds. Error text from a failed call is rendered as
// plain text by the caller, never through here.
//
// `content` may be empty/undefined (a still-streaming-in-practice-impossible
// but still defensive case, and the initial render before any content
// exists) — renders nothing rather than an empty markdown tree.

const isUnsafeHref = (href) => {
  if (!href) return false;
  // "javascript:" (any casing/whitespace padding) is the one scheme a
  // markdown link can carry that would execute on click; everything else
  // (http/https/mailto/relative paths/#anchors) is left alone.
  return /^\s*javascript:/i.test(String(href));
};

const HfLink = ({ href, children, node: _node, ...rest }) => (
  <a
    {...rest}
    href={isUnsafeHref(href) ? undefined : href}
    target='_blank'
    rel='noopener noreferrer'
  >
    {children}
  </a>
);

const HfCodeBlock = ({ children, node: _node, ...rest }) => {
  const { t: tr } = useTranslation();
  const preRef = useRef(null);

  const onCopy = () => {
    try {
      navigator.clipboard?.writeText(preRef.current?.textContent || '');
    } catch (_) {
      /* clipboard unavailable — nothing to surface here, the copy button
         itself is the only affordance and there is no toast host to route
         through inside a markdown leaf. */
    }
  };

  return (
    <div style={{ position: 'relative' }}>
      <button
        type='button'
        className='btn ghost xs'
        onClick={onCopy}
        style={{ position: 'absolute', top: 6, right: 6, zIndex: 1 }}
      >
        {tr('console.common.copy', 'copy')}
      </button>
      <pre {...rest} ref={preRef} className='hf-code'>
        {children}
      </pre>
    </div>
  );
};

const HfTable = ({ children, node: _node, ...rest }) => (
  <div className='hf-table-scroll'>
    <table {...rest} className='t'>
      {children}
    </table>
  </div>
);

const COMPONENTS = {
  a: HfLink,
  pre: HfCodeBlock,
  table: HfTable,
};

const HfMarkdown = ({ content, testId, className }) => {
  if (!content) return null;
  return (
    <div
      className={className ? `hf-markdown ${className}` : 'hf-markdown'}
      data-testid={testId}
    >
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={COMPONENTS}>
        {content}
      </ReactMarkdown>
    </div>
  );
};

export default HfMarkdown;
