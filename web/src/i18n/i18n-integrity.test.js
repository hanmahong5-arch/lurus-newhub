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

/*
 * i18n integrity gate.
 *
 * This project keys translations by their Chinese source text: t('保存设置').
 * i18n.js sets fallbackLng: 'zh', so a key that en.json does not carry resolves
 * to the key itself — i.e. an operator running the console in English is shown
 * Chinese. Nothing else in the suite notices, because every unit test renders
 * a component whose strings happen to be present.
 *
 * These three assertions are what catch that class. Each was red before the
 * change that introduced this file: 128 unresolvable keys, 15 escaped strings
 * and 88 untranslated toasts.
 */
import fs from 'node:fs';
import path from 'node:path';
import { describe, it, expect } from 'vitest';

import en from './locales/en.json';

const SRC = path.resolve(process.cwd(), 'src');
const HAN = /[一-鿿]/;

function sourceFiles(dir, out = []) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      if (entry.name !== 'node_modules' && entry.name !== 'locales')
        sourceFiles(p, out);
    } else if (
      /\.(jsx?|tsx?)$/.test(entry.name) &&
      !/\.test\./.test(entry.name)
    )
      out.push(p);
  }
  return out;
}

const rel = (p) => path.relative(process.cwd(), p).replace(/\\/g, '/');

// Block comments are blanked rather than removed so line numbers survive.
const stripComments = (s) =>
  s
    .replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
    .replace(
      /(^|[^:'"`\\])\/\/[^\n]*/g,
      (m, p1) => p1 + ' '.repeat(m.length - p1.length),
    );

const FILES = sourceFiles(SRC);
const CLEANED = new Map(
  FILES.map((f) => [f, stripComments(fs.readFileSync(f, 'utf8'))]),
);

// t('…') / i18next.t('…'), first argument only, allowed to sit on its own line.
// The body may contain the OTHER quote character — several keys embed a quoted
// name, e.g. t('确定要删除供应商 "{{name}}" 吗？') — so the class excludes only
// the delimiter itself. Excluding both quotes truncates such a key mid-string
// and then reports the call as untranslated.
const T_CALL = /\bt\(\s*(['"])((?:(?!\1)[^\\]|\\.)*?)\1/gs;
const LITERAL = /(['"])((?:[^\\\n]|\\.)*?)\1/g;

/**
 * Turn the source text of a string literal into the string the engine actually
 * builds. Several keys embed \n, and the runtime key therefore holds a real
 * newline while the source holds a backslash and an n. Comparing the raw source
 * against en.json would let a key look present while it can never resolve —
 * and would let a fix look applied when the entry it added is unreachable.
 */
function unescapeLiteral(body) {
  return body.replace(/\\(u[0-9a-fA-F]{4}|x[0-9a-fA-F]{2}|.)/gs, (_, esc) => {
    if (esc[0] === 'u') return String.fromCharCode(parseInt(esc.slice(1), 16));
    if (esc[0] === 'x') return String.fromCharCode(parseInt(esc.slice(1), 16));
    const simple = {
      n: '\n',
      t: '\t',
      r: '\r',
      b: '\b',
      f: '\f',
      v: '\v',
      0: '\0',
    };
    return Object.prototype.hasOwnProperty.call(simple, esc)
      ? simple[esc]
      : esc;
  });
}

// i18next resolves a `count` option through CLDR plural suffixes, so any of
// these standing in for the bare key means the string is genuinely translated.
const PLURAL_SUFFIXES = [
  '',
  '_zero',
  '_one',
  '_two',
  '_few',
  '_many',
  '_other',
];
const resolves = (bundle, key) =>
  PLURAL_SUFFIXES.some((s) =>
    Object.prototype.hasOwnProperty.call(bundle, key + s),
  );

const NOTIFIER_OPEN =
  /\b(?:showSuccess|showError|showWarning|showInfo|showNotice|setError|setErrMsg|Notification\.(?:error|success|warning|info)|Toast\.(?:error|success|warning|info)|Modal\.(?:error|warning|info|confirm))\s*\(/g;

/**
 * Yield the source text of each notifier call, from its opening parenthesis to
 * the matching close. Parentheses inside string literals are skipped so that a
 * message such as '(可选)' does not truncate the span.
 */
function notifierSpans(src) {
  const spans = [];
  NOTIFIER_OPEN.lastIndex = 0;
  let m;
  while ((m = NOTIFIER_OPEN.exec(src))) {
    const open = m.index + m[0].length - 1;
    let depth = 0;
    let quote = null;
    let i = open;
    for (; i < src.length; i++) {
      const c = src[i];
      if (quote) {
        if (c === '\\') i++;
        else if (c === quote) quote = null;
        continue;
      }
      if (c === "'" || c === '"' || c === '`') quote = c;
      else if (c === '(') depth++;
      else if (c === ')') {
        depth--;
        if (depth === 0) break;
      }
    }
    spans.push({ start: open, text: src.slice(open, i + 1) });
  }
  return spans;
}

// Measured, not chosen: 71 on 2026-08-22, 28 of them on the OpenRouter sync
// page alone. Lower it whenever a surface is translated; never raise it.
const MARKUP_CHINESE_CEILING = 71;

describe('i18n integrity', () => {
  it('every Chinese t() key in the source resolves in en.json', () => {
    const missing = [];
    for (const [file, src] of CLEANED) {
      // Scanned over the whole file, not line by line: prettier wraps long
      // calls, so t(\n  '…'\n) is common and a per-line scan simply cannot see
      // those keys. One such key was already missing when this was written.
      let m;
      T_CALL.lastIndex = 0;
      while ((m = T_CALL.exec(src))) {
        const key = unescapeLiteral(m[2]);
        if (!HAN.test(key)) continue;
        if (resolves(en.translation, key)) continue;
        const line = src.slice(0, m.index).split('\n').length;
        missing.push(`${rel(file)}:${line}  ${key}`);
      }
    }
    expect(
      missing,
      `These keys fall back to Chinese for an English-locale operator.\n` +
        `Add each to src/i18n/locales/en.json:\n  ${missing.join('\n  ')}`,
    ).toEqual([]);
  });

  it('no Chinese is written as \\u escapes, which hides it from both t() and grep', () => {
    const offenders = [];
    for (const [file, src] of CLEANED) {
      src.split('\n').forEach((line, i) => {
        // A character class such as /^[一-龥]+$/ is a range, not text.
        if (/\[[^\]]*\\u[0-9a-fA-F]{4}\s*-/.test(line)) return;
        for (const run of line.match(/(?:\\u[0-9a-fA-F]{4})+/g) || []) {
          const decoded = run
            .split(/\\u/)
            .filter(Boolean)
            .map((h) => String.fromCharCode(parseInt(h, 16)))
            .join('');
          if (HAN.test(decoded))
            offenders.push(`${rel(file)}:${i + 1}  ${run} = ${decoded}`);
        }
      });
    }
    expect(
      offenders,
      `Escaped Chinese is invisible to the key audit above: the extracted key ` +
        `is the backslash text, which contains no Han, so the first assertion ` +
        `silently skips it while the runtime key is Chinese. Write the ` +
        `characters literally and pass them through t().\n  ` +
        offenders.join('\n  '),
    ).toEqual([]);
  });

  it('no operator-facing toast or error is hardcoded in Chinese', () => {
    const NOTIFIER =
      /\b(showSuccess|showError|showWarning|showInfo|showNotice|setError|setErrMsg|Notification\.(?:error|success|warning|info)|Toast\.(?:error|success|warning|info)|Modal\.(?:error|warning|info|confirm))\s*\(/;
    const offenders = [];
    for (const [file, src] of CLEANED) {
      // Keys used with t() anywhere are translated; only bare literals count.
      const translated = new Set();
      let k;
      T_CALL.lastIndex = 0;
      while ((k = T_CALL.exec(src))) translated.add(unescapeLiteral(k[2]));

      // Prettier wraps these calls freely — the message can sit three or four
      // lines below the notifier, inside an object literal or a ternary — so
      // the span is found by matching the call's parentheses rather than by
      // guessing a line window. A window of two lines missed a quarter of them.
      for (const span of notifierSpans(src)) {
        let m;
        LITERAL.lastIndex = 0;
        while ((m = LITERAL.exec(span.text))) {
          if (!HAN.test(m[2])) continue;
          if (translated.has(unescapeLiteral(m[2]))) continue;
          const line = src.slice(0, span.start + m.index).split('\n').length;
          offenders.push(`${rel(file)}:${line}  ${m[2]}`);
        }
      }
    }
    expect(
      offenders,
      `These reach the screen in Chinese in every locale, English included. ` +
        `Wrap each in t(…) (or i18next.t(…) outside a component, as ` +
        `helpers/render.jsx does) and add the English to en.json.\n  ` +
        offenders.join('\n  '),
    ).toEqual([]);
  });

  /*
   * Chinese written straight into markup — JSX text, or a label / placeholder /
   * title prop — never reaches t() at all, so no locale can fix it. The two
   * classes above are held at zero; this one is a ratchet, because whole
   * surfaces are still built that way. /console/openrouter-sync is the worst:
   * measured against the running console in an en-US browser, its heading,
   * every table column and every form field render in Chinese. Translating
   * that page is a change of its own; what this assertion buys is that the
   * count cannot grow while it waits.
   */
  it('markup-level Chinese does not spread further', () => {
    const JSX_TEXT = />[^<>{}]*[一-鿿][^<>{}]*</g;
    const BARE_PROP =
      /\b(?:label|placeholder|title|description|text|tooltip|content|emptyText|okText|cancelText)\s*=\s*(['"])((?:(?!\1)[^\\])*?)\1/g;
    const offenders = [];
    for (const [file, src] of CLEANED) {
      for (const re of [JSX_TEXT, BARE_PROP]) {
        re.lastIndex = 0;
        let m;
        while ((m = re.exec(src))) {
          const text = (m[2] ?? m[0].slice(1, -1)).trim();
          if (!HAN.test(text)) continue;
          const line = src.slice(0, m.index).split('\n').length;
          offenders.push(`${rel(file)}:${line}  ${text}`);
        }
      }
    }
    const byFile = offenders.reduce((acc, o) => {
      const f = o.split(':')[0];
      acc[f] = (acc[f] || 0) + 1;
      return acc;
    }, {});
    const worst = Object.entries(byFile)
      .sort((a, b) => b[1] - a[1])
      .slice(0, 5)
      .map(([f, n]) => `${n} ${f}`)
      .join('\n  ');
    expect(
      offenders.length,
      `Markup-level Chinese grew. Worst offenders:\n  ${worst}\n` +
        `Route the new strings through t() rather than raising this number.`,
    ).toBeLessThanOrEqual(MARKUP_CHINESE_CEILING);
  });

  /*
   * fr / ja / ru / vi are upstream leftovers, already ~600 keys behind the
   * union of all locales. Bringing them to parity is not what makes the
   * console launchable — en is the fallback every non-Chinese operator
   * actually lands on — but they must not slide further. This is a ratchet,
   * not a target: lower the number when a locale improves, never raise it.
   */
  it.each([
    ['fr', 221],
    ['ja', 221],
    ['ru', 221],
    ['vi', 217],
  ])('%s carries no more than %i untranslated keys', async (lng, ceiling) => {
    const bundle = (await import(`./locales/${lng}.json`)).default.translation;
    const keys = new Set();
    for (const src of CLEANED.values()) {
      let m;
      T_CALL.lastIndex = 0;
      while ((m = T_CALL.exec(src)))
        if (HAN.test(m[2])) keys.add(unescapeLiteral(m[2]));
    }
    const missing = [...keys].filter((k) => !resolves(bundle, k));
    expect(missing.length).toBeLessThanOrEqual(ceiling);
  });
});
