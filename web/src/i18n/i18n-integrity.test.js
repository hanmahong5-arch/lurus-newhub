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
// Backticks included: a template literal is just as visible on screen as a
// quoted one, and four notifiers were built that way.
const LITERAL = /(['"`])((?:[^\\]|\\.)*?)\1/gs;

// t(`…`). A template literal handed to t() becomes its own key after
// interpolation — '成功删除 3 个模型' — which no bundle can contain, so the call
// renders Chinese in every locale while looking translated. Five existed.
const T_TEMPLATE = /\bt\(\s*`((?:[^`\\]|\\.)*)`/gs;

/**
 * Blank out the key of every t() call, keeping length so offsets still map to
 * lines. Used before scanning for bare literals: excusing a literal because the
 * same text appears in some t() call elsewhere in the file is what let
 * `title: '保存失败'` sit three lines above `t('保存失败')` and render Chinese.
 */
const maskTranslatedKeys = (src) =>
  src.replace(T_CALL, (m) => ' '.repeat(m.length));

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

/*
 * The language picker names each language in its own language: 中文 stays 中文
 * for a French operator, exactly as Français stays Français for a Chinese one.
 * These are the only Chinese strings in the markup that are correct as they
 * stand, so they are exempted by VALUE — Chinese appearing anywhere else, in
 * this file included, still fails.
 */
const LANGUAGE_ENDONYMS = new Set(['中文', '日本語']);

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
    const offenders = [];
    for (const [file, src] of CLEANED) {
      // The key of each t() call is blanked, so what remains inside a notifier
      // is by definition a literal that reaches the screen untranslated. The
      // earlier form excused any literal whose text was used with t() SOMEWHERE
      // in the same file, which is how `title: '保存失败'` hid three lines above
      // an `i18next.t('保存失败')` that was not the call rendering it.
      const masked = maskTranslatedKeys(src);

      // Prettier wraps these calls freely — the message can sit three or four
      // lines below the notifier, inside an object literal or a ternary — so
      // the span is found by matching the call's parentheses rather than by
      // guessing a line window. A window of two lines missed a quarter of them.
      for (const span of notifierSpans(masked)) {
        let m;
        LITERAL.lastIndex = 0;
        while ((m = LITERAL.exec(span.text))) {
          if (!HAN.test(m[2])) continue;
          const line = masked.slice(0, span.start + m.index).split('\n').length;
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

  it('no t() call is handed an interpolated template literal', () => {
    const offenders = [];
    for (const [file, src] of CLEANED) {
      let m;
      T_TEMPLATE.lastIndex = 0;
      while ((m = T_TEMPLATE.exec(src))) {
        if (!HAN.test(m[1])) continue;
        const line = src.slice(0, m.index).split('\n').length;
        offenders.push(`${rel(file)}:${line}  ${m[1].replace(/\s+/g, ' ')}`);
      }
    }
    expect(
      offenders,
      `t(\`…\`) builds its key by interpolation, so the key that reaches ` +
        `i18next is e.g. '成功删除 3 个模型' — a string no bundle can hold. The ` +
        `call renders Chinese in every locale while reading as translated, ` +
        `and the key audit above cannot see it either. Use a placeholder: ` +
        `t('成功删除 {{n}} 个模型', { n }).\n  ` +
        offenders.join('\n  '),
    ).toEqual([]);
  });

  /*
   * Chinese written straight into markup — JSX text, or a label / placeholder /
   * title prop — never reaches t() at all, so no locale can fix it. This was a
   * ratchet at 71 while whole surfaces were still built that way;
   * /console/openrouter-sync alone held 28 of them. Those are translated now,
   * so it is an invariant like the rest.
   */
  it('no Chinese is written straight into markup', () => {
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
          if (LANGUAGE_ENDONYMS.has(text)) continue;
          const line = src.slice(0, m.index).split('\n').length;
          offenders.push(`${rel(file)}:${line}  ${text}`);
        }
      }
    }
    expect(
      offenders,
      `Chinese in markup never reaches t(), so no locale can fix it — it is ` +
        `Chinese on screen for every operator. Wrap each in t() and add the ` +
        `English to en.json.\n  ` +
        offenders.join('\n  '),
    ).toEqual([]);
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
