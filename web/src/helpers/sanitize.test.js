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
import { describe, it, expect } from 'vitest';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { sanitizeHtml, escapeHtml } from './sanitize';

/*
 * Two halves, and both are needed.
 *
 * The first half pins what sanitizeHtml actually strips — a sanitiser nobody
 * has behaviour tests for is a comment, not a control.
 *
 * The second half is the structural gate: every dangerouslySetInnerHTML in
 * src/ has to name sanitizeHtml or escapeHtml at the sink. Before cycle 12,
 * sanitizeHtml existed and had zero call sites in the whole tree (only
 * escapeHtml, from CodeViewer, was wired) while ten sinks fed it raw
 * operator- or model-authored HTML. A per-sink unit test would not have
 * caught that; only an enumeration of the sinks does.
 */

describe('sanitizeHtml', () => {
  const cases = [
    {
      name: 'strips an onerror handler while keeping the element',
      input: '<img src=x onerror=alert(1)>',
      absent: ['onerror'],
      present: ['<img'],
    },
    {
      name: 'strips a script element and its body',
      input: '<script>alert(1)</script><b>ok</b>',
      absent: ['script', 'alert'],
      present: ['<b>ok</b>'],
    },
    {
      name: 'strips a javascript: href but keeps the anchor text',
      input: '<a href="javascript:alert(1)">click</a>',
      absent: ['javascript:'],
      present: ['click'],
    },
    {
      name: 'strips an iframe outright',
      input: '<iframe src="https://evil.example"></iframe>',
      absent: ['iframe', 'evil.example'],
      present: [],
    },
    {
      name: 'strips an inline event handler on a plain element',
      input: '<div onclick="steal()">hi</div>',
      absent: ['onclick', 'steal'],
      present: ['hi'],
    },
    {
      name: 'strips a style element (so no CSS reaches the page)',
      input:
        '<style>body{background:url(https://evil.example)}</style><p>x</p>',
      absent: ['<style', 'evil.example'],
      present: ['<p>x</p>'],
    },
    {
      name: 'keeps ordinary formatting intact',
      input: '<b>bold</b> and <em>em</em>',
      absent: [],
      present: ['<b>bold</b>', '<em>em</em>'],
    },
    {
      name: 'keeps an http link and its href',
      input: '<a href="https://ok.example/a?b=1">link</a>',
      absent: [],
      present: ['https://ok.example/a?b=1', 'link'],
    },
  ];

  it.each(cases)('$name', ({ input, absent, present }) => {
    const out = sanitizeHtml(input);
    for (const needle of absent) expect(out).not.toContain(needle);
    for (const needle of present) expect(out).toContain(needle);
  });

  it('turns null and undefined into an empty string rather than the words', () => {
    expect(sanitizeHtml(null)).toBe('');
    expect(sanitizeHtml(undefined)).toBe('');
  });
});

describe('escapeHtml', () => {
  it('shows markup verbatim instead of rendering it', () => {
    expect(escapeHtml('<img src=x onerror=alert(1)>')).toBe(
      '&lt;img src=x onerror=alert(1)&gt;',
    );
  });

  it('escapes the ampersand first so an entity is not double-decoded', () => {
    expect(escapeHtml('&lt;')).toBe('&amp;lt;');
  });
});

/* ------------------------------------------------------------------ */

const HERE = path.dirname(fileURLToPath(import.meta.url));
const SRC = path.resolve(HERE, '..');

const isTestName = (name) => /\.(test|spec)\.(js|jsx)$/.test(name);

/*
 * Sinks whose value is sanitised or escaped somewhere other than the JSX
 * attribute itself. Keyed by src-relative file, then by the exact expression
 * the sink names. Each entry says where the escaping happens; the last test
 * fails if the sink moved or went away, so an entry cannot outlive its sink.
 */
const OFF_SITE_ESCAPED = {
  'components/playground/CodeViewer.jsx': {
    highlightedContent:
      'escapeHtml (or highlightJson, which escapes as it tokenises) is applied ' +
      'in the highlightedContent useMemo — this viewer DISPLAYS what a server ' +
      'returned, so markup is content to read, not formatting to render.',
  },
};

function walkFiles(dir, out = []) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) walkFiles(full, out);
    else if (['.js', '.jsx'].includes(path.extname(entry.name))) out.push(full);
  }
  return out;
}

const rel = (abs) => path.relative(SRC, abs).split(path.sep).join('/');

// Matches the one shape this tree uses: dangerouslySetInnerHTML={{ __html: X }}
// where X runs to the closing brace. Comments that merely mention the
// attribute do not match, because they carry no `__html:`.
const SINK_RE = /dangerouslySetInnerHTML=\{\{\s*__html:\s*([^}]+?)\s*\}\}/g;

const sinks = [];
for (const file of walkFiles(SRC)) {
  if (isTestName(path.basename(file))) continue;
  const text = fs.readFileSync(file, 'utf8');
  SINK_RE.lastIndex = 0;
  let m;
  while ((m = SINK_RE.exec(text)) !== null) {
    sinks.push({
      file: rel(file),
      expr: m[1].trim(),
      line: text.slice(0, m.index).split('\n').length,
    });
  }
}

const SANITISED_AT_SINK = /^(sanitizeHtml|escapeHtml)\(/;

describe('every dangerouslySetInnerHTML goes through a sanitiser', () => {
  it('finds the sinks at all (fail fast if the scan broke)', () => {
    expect(sinks.length).toBeGreaterThanOrEqual(8);
  });

  it('names sanitizeHtml or escapeHtml at the sink, or carries a recorded reason', () => {
    const raw = sinks.filter(
      (s) =>
        !SANITISED_AT_SINK.test(s.expr) &&
        OFF_SITE_ESCAPED[s.file]?.[s.expr] === undefined,
    );
    expect(
      raw,
      'These dangerouslySetInnerHTML sinks inject a value that was not passed ' +
        'through helpers/sanitize. Wrap it in sanitizeHtml() at the sink, or — ' +
        'when the value is escaped elsewhere on purpose — record it in ' +
        'OFF_SITE_ESCAPED with where:\n  ' +
        raw.map((s) => `${s.file}:${s.line}  __html: ${s.expr}`).join('\n  '),
    ).toEqual([]);
  });

  it('every OFF_SITE_ESCAPED entry still matches a live sink', () => {
    const stale = [];
    for (const [file, exprs] of Object.entries(OFF_SITE_ESCAPED)) {
      for (const expr of Object.keys(exprs)) {
        if (!sinks.some((s) => s.file === file && s.expr === expr)) {
          stale.push(`${file}  __html: ${expr}`);
        }
      }
    }
    expect(
      stale,
      'These OFF_SITE_ESCAPED entries no longer match any sink. Drop them so ' +
        'the exemption list cannot outlive its reason:\n  ' +
        stale.join('\n  '),
    ).toEqual([]);
  });
});
