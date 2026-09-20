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
 * A key that en.json does not carry resolves to the key itself — i.e. an
 * operator running the console in English is shown Chinese. Nothing else in
 * the suite notices, because every unit test renders a component whose strings
 * happen to be present.
 *
 * That is true of i18n.js both before and after the fallback change: it used
 * to route en through fallbackLng: 'zh' and now gives zh no fallback at all
 * (see the comment on fallbackLng there), and either way the string an
 * English-locale operator reads for a missing key is Chinese.
 *
 * The cases below are what catch that class. The first three were red when
 * this file was written — 128 unresolvable keys, 15 escaped strings, 88
 * untranslated toasts — and each one added since closed a spelling those three
 * cannot see: an interpolated template literal, Chinese written straight into
 * markup, a dotted console.* key missing from one bundle (in either
 * direction), and a key that arrives through a variable rather than a literal.
 */
import fs from 'node:fs';
import path from 'node:path';
import { describe, it, expect } from 'vitest';

import en from './locales/en.json';
import zh from './locales/zh.json';
import { DATE_RANGE_PRESETS } from '../constants/console.constants';
import { ERROR_MESSAGES } from '../constants/playground.constants';

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

// t('…') / i18next.t('…') / tr('…'), first argument only, allowed to sit on
// its own line.
// The body may contain the OTHER quote character — several keys embed a quoted
// name, e.g. t('确定要删除供应商 "{{name}}" 吗？') — so the class excludes only
// the delimiter itself. Excluding both quotes truncates such a key mid-string
// and then reports the call as untranslated.
//
// Two axes, and this covers one of them.
//
// The NAME axis is closed: collecting every `useTranslation(` call site under
// src and grouping the destructuring forms yields two bindings for the
// translate function — a plain `{ t }` (with or without i18n alongside) and
// `{ t: tr }`. Nothing binds a third name. The rename is the v2 console's:
// measured on this branch, 46 `{ t: tr }` sites across 29 files, every one of
// them under pages/v2 or components/hifi, where `t` is already taken by a
// token loop variable. Scanning for `t(` alone left those 46 sites unaudited,
// and two of their keys were in fact missing from en.json.
//
// The ARGUMENT axis is NOT closed by this regex: it reads a literal first
// argument and nothing else, so `t(preset.text)` — a key arriving through a
// variable — is invisible to it, whatever the binding is called. Five Chinese
// quick-range labels and six playground error messages reached the English
// console that way. The two cases named 'every t() call whose key arrives
// through a variable is accounted for' and 'every key table handed to t()
// through a variable resolves in en.json' are what hold that axis.
const T_CALL = /\b(?:tr|t)\(\s*(['"])((?:(?!\1)[^\\]|\\.)*?)\1/gs;
// Backticks included: a template literal is just as visible on screen as a
// quoted one, and four notifiers were built that way.
const LITERAL = /(['"`])((?:[^\\]|\\.)*?)\1/gs;

// t(`…`) / tr(`…`). A template literal handed to t() becomes its own key after
// interpolation — '成功删除 3 个模型' — which no bundle can contain, so the call
// renders Chinese in every locale while looking translated. Five existed.
const T_TEMPLATE = /\b(?:tr|t)\(\s*`((?:[^`\\]|\\.)*)`/gs;

const T_OPEN = /\b(?:tr|t)\(/g;

/**
 * The first argument of every t() / tr() call, as source text.
 *
 * Read by scanning to the matching delimiter rather than by a regex for one
 * expression shape: a member expression, an index, a call and a ternary are
 * all keys arriving through a variable, and a pattern written for identifiers
 * would quietly miss the other three. Quotes and nesting are respected so that
 * t(foo('a,b')) yields one argument, not two.
 */
function firstArguments(src) {
  const args = [];
  T_OPEN.lastIndex = 0;
  let m;
  while ((m = T_OPEN.exec(src))) {
    const open = m.index + m[0].length;
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
      else if (c === '(' || c === '[' || c === '{') depth++;
      else if (c === ')' || c === ']' || c === '}') {
        if (depth === 0) break;
        depth--;
      } else if (c === ',' && depth === 0) break;
    }
    args.push({ start: open, text: src.slice(open, i).trim() });
  }
  return args;
}

const isLiteralArgument = (text) => /^['"`]/.test(text);

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

/**
 * Every leaf key of a bundle, in the dotted form a t() call would use.
 *
 * Both spellings collapse to the same string on purpose: the bundles hold
 * "setting.group.general" as one flat key containing dots, and console.* as a
 * nested object, and i18next reads either — ignoreJSONStructure defaults to
 * true, so a lookup that misses the nested path falls back to a deep find of
 * the flat key (verified against the installed engine: t('setting.group.
 * general') is 'General' in en and '通用' in zh).
 */
function dottedKeys(obj, prefix = '', out = []) {
  for (const k of Object.keys(obj)) {
    const v = obj[k];
    const p = prefix ? `${prefix}.${k}` : k;
    if (v && typeof v === 'object') dottedKeys(v, p, out);
    else out.push(p);
  }
  return out;
}

const consoleKeys = (bundle) =>
  dottedKeys(bundle.translation).filter((k) => k.startsWith('console.'));

// Checked the way i18next resolves it: the nested path first, then the flat
// key. Checking only one spelling would report a key as missing while the
// screen shows it translated.
const resolvesDotted = (bundle, key) => {
  const nested = key
    .split('.')
    .reduce(
      (node, part) =>
        node && typeof node === 'object' ? node[part] : undefined,
      bundle.translation,
    );
  return (
    nested !== undefined ||
    Object.prototype.hasOwnProperty.call(bundle.translation, key)
  );
};

// Like resolvesDotted, but also accepts the CLDR plural-suffixed sibling:
// t('console.admin.users.count', { count: n }) resolves through
// count_one/count_other and never through a literal `count` leaf. Without
// this, the reference scan below reports every plural console.* key as
// missing (26 false positives, measured while writing the gate) on top of
// the 15 keys that were genuinely absent from en.json.
const resolvesDottedPlural = (bundle, key) =>
  PLURAL_SUFFIXES.some((s) => resolvesDotted(bundle, key + s));

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
 * for an English operator, exactly as English stays English for a Chinese one.
 * That is Chinese in the markup which is correct as it stands, so it is
 * exempted by VALUE — Chinese appearing anywhere else, in this file included,
 * still fails.
 *
 * 日本語 sat here too while the picker offered ja. The picker offers the
 * languages i18n.js registers and ja is not one of them, so the exemption is
 * gone: `grep -rn 日本語 src --include=*.jsx --include=*.js` outside .test.
 * files returns nothing to exempt, and 日本語 appearing in markup from here on
 * fails like any other Chinese string — which is the honest state for a
 * language this build does not ship.
 *
 * The one production site that carried it was
 * components/table/models/modals/SyncWizardModal.jsx:121 (a radio offering
 * ja), and that whole orphan directory was deleted earlier on this branch. If
 * it ever comes back, it comes back offering a language the build does not
 * ship, and this case is the thing that says so.
 */
const LANGUAGE_ENDONYMS = new Set(['中文']);

/*
 * Every t() / tr() call in src whose first argument is not a literal, keyed by
 * file and by the expression as written, with the number of times that exact
 * expression appears in that file. Enumerated by the case below, which fails
 * on a new one and on a stale one alike.
 *
 * The reason has to say where the key text comes from and why it resolves,
 * because no scan in this file can follow a variable. Where the keys are a
 * table, the case after it reads the table.
 */
const NON_LITERAL_T_CALLS = {
  'src/components/hifi/HFShell.jsx  s.hKey': {
    count: 1,
    reason:
      'console.nav.section_* from the v2 nav model, called as t(s.hKey, s.h): the second argument is the English heading, so a key no bundle holds degrades to English rather than to an identifier. All 33 console.nav.* keys resolve in en.json today.',
  },
  'src/components/hifi/HFShell.jsx  it.key': {
    count: 1,
    reason:
      'console.nav.* item key, called as t(it.key, it.label) — English default, same family as s.hKey.',
  },
  'src/components/hifi/HFShell.jsx  it.titleKey': {
    count: 1,
    reason:
      "console.nav.* tooltip for a deferred entry, called as t(it.titleKey, it.title || 'not available in v2 yet') — English default.",
  },
  "src/components/settings/SecuritySettingPage.jsx  domainFilterMode ? '域名白名单' : '域名黑名单'":
    {
      count: 1,
      reason:
        'Ternary over two Chinese literals; both branches are checked by the literal scan in the case below, which walks string literals inside non-literal arguments. Both resolve in en.json.',
    },
  "src/components/settings/SecuritySettingPage.jsx  ipFilterMode ? 'IP白名单' : 'IP黑名单'":
    {
      count: 1,
      reason:
        'Ternary over two Chinese literals; both branches resolve in en.json and are checked by the case below.',
    },
  "src/components/settings/SystemSetting.jsx  domainFilterMode ? '域名白名单' : '域名黑名单'":
    {
      count: 1,
      reason:
        'Same ternary as SecuritySettingPage.jsx — this file is the older settings shell that still carries it. Both branches resolve in en.json and are checked by the case below.',
    },
  "src/components/settings/SystemSetting.jsx  ipFilterMode ? 'IP白名单' : 'IP黑名单'":
    {
      count: 1,
      reason:
        'Same ternary as SecuritySettingPage.jsx. Both branches resolve in en.json and are checked by the case below.',
    },
  'src/components/setup/components/steps/UsageModeStep.jsx  key': {
    count: 1,
    reason:
      'The 11 mode_feat_* keys declared in MODE_FEATURES in that same file; every one of them resolves in en.json (measured).',
  },
  'src/components/table/mj-logs/MjLogsFilters.jsx  preset.text': {
    count: 1,
    reason:
      'DATE_RANGE_PRESETS from constants/console.constants.js — read by the table case below, which is what made the five Chinese quick-range labels red.',
  },
  'src/components/table/task-logs/TaskLogsFilters.jsx  preset.text': {
    count: 1,
    reason: 'DATE_RANGE_PRESETS, same as MjLogsFilters.jsx.',
  },
  "src/components/table/users/modals/EditUserModal.jsx  isEdit ? '编辑' : '新建'":
    {
      count: 1,
      reason:
        'Ternary over two Chinese literals, both in en.json, both checked by the case below.',
    },
  "src/helpers/render.jsx  parts.join(' * ')": {
    count: 1,
    reason:
      "A composite of parts that were each translated by their own i18next.t() before the join, plus the non-Chinese '{{ratioType}}: {{groupRatio}}'. The joined string is a key no bundle holds, so i18next returns it and interpolates it (verified against the installed engine), which is why the ratio tooltip reads in the operator's language even though this call never resolves.",
  },
  'src/hooks/playground/useDataLoader.js  message': {
    count: 2,
    reason:
      'The server-supplied message field of a failed /api/user/models or /api/user/groups response. The gateway answers in Chinese and t() is a pass-through for it; giving server errors an error_code plus Accept-Language negotiation is plan section 7, next cycle. Not a key this repo can add to en.json.',
  },
  'src/hooks/playground/useMessageActions.jsx  ERROR_MESSAGES.NO_TEXT_CONTENT':
    {
      count: 1,
      reason:
        'ERROR_MESSAGES from constants/playground.constants.js — read by the table case below; six of its eight values were missing from en.json.',
    },
  'src/hooks/playground/useMessageActions.jsx  ERROR_MESSAGES.COPY_FAILED': {
    count: 1,
    reason: 'ERROR_MESSAGES, same table.',
  },
  'src/hooks/playground/useMessageActions.jsx  ERROR_MESSAGES.COPY_HTTPS_REQUIRED':
    {
      count: 1,
      reason: 'ERROR_MESSAGES, same table.',
    },
  'src/hooks/playground/useMessageActions.jsx  ERROR_MESSAGES.BROWSER_NOT_SUPPORTED':
    {
      count: 1,
      reason: 'ERROR_MESSAGES, same table.',
    },
  'src/pages/Setting/SettingsSidebar.jsx  group.labelKey': {
    count: 2,
    reason:
      "setting.group.* from the GROUPS table in that same file. They are flat keys in the bundles — \"setting.group.general\" is one key containing dots, not a nested object — and resolve because ignoreJSONStructure defaults to true (verified: t('setting.group.general') is 'General' in en and '通用' in zh).",
  },
  'src/pages/Setting/SettingsSidebar.jsx  item.labelKey': {
    count: 2,
    reason:
      'setting.nav.* from the same GROUPS table; flat keys, all 11 resolve in both bundles.',
  },
  'src/pages/v2/CommandPalette/index.jsx  it.key': {
    count: 1,
    reason:
      'console.nav.* again, called as tr(it.key, it.label) with the English label as the default.',
  },
};

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
   * The argument axis.
   *
   * Every scan above reads a literal first argument. A key that arrives
   * through a variable — t(preset.text) — is invisible to all of them, and
   * that is not a hypothetical: five quick-range labels (今天 / 近 7 天 / 本周 /
   * 近 30 天 / 本月, constants/console.constants.js) reached /console/task and
   * /console/midjourney in Chinese for an English operator, and six of the
   * eight constants/playground.constants.js ERROR_MESSAGES did the same in the
   * playground. Neither family was ever red.
   *
   * So the sites are enumerated instead, with a reason each. The comparison is
   * an equality: a new t(<variable>) call fails until it is listed and its key
   * source is audited, and a site that goes away fails too rather than leaving
   * a stale exemption behind. The second case does the part a list cannot —
   * it reads the two key tables and checks the keys themselves.
   */
  it('every t() call whose key arrives through a variable is accounted for', () => {
    const found = new Map();
    for (const [file, src] of CLEANED) {
      for (const arg of firstArguments(src)) {
        if (!arg.text || isLiteralArgument(arg.text)) continue;
        const site = `${rel(file)}  ${arg.text}`;
        found.set(site, (found.get(site) || 0) + 1);
      }
    }
    const measured = [...found].map(([site, n]) => `${site} x${n}`).sort();
    const declared = Object.entries(NON_LITERAL_T_CALLS)
      .map(([site, { count }]) => `${site} x${count}`)
      .sort();
    expect(
      Object.entries(NON_LITERAL_T_CALLS)
        .filter(([, v]) => !v.reason || v.reason.length < 20)
        .map(([site]) => site),
      'every exemption carries a reason',
    ).toEqual([]);
    expect(
      measured,
      `t(<variable>) hands i18next a key no scan in this file can read, so ` +
        `each site is listed in NON_LITERAL_T_CALLS with the reason its keys ` +
        `resolve. A site listed here and not measured means the list went ` +
        `stale; a site measured and not listed is new and unaudited — follow ` +
        `the key back to where the strings are written and make sure en.json ` +
        `carries every one of them (that is what the next case checks for the ` +
        `two constant tables).`,
    ).toEqual(declared);
  });

  it('every key table handed to t() through a variable resolves in en.json', () => {
    // The tables the sites above read. Values are keys, so a value carrying
    // Han that en.json does not hold is Chinese on an English screen; the
    // English-only members (playground's API_REQUEST_ERROR family aside) are
    // covered by the same check because it is keyed on the Han test.
    const tables = {
      'constants/console.constants.js DATE_RANGE_PRESETS':
        DATE_RANGE_PRESETS.map((p) => p.text),
      'constants/playground.constants.js ERROR_MESSAGES':
        Object.values(ERROR_MESSAGES),
    };
    const missing = [];
    for (const [table, keys] of Object.entries(tables))
      for (const key of keys)
        if (HAN.test(key) && !resolves(en.translation, key))
          missing.push(`${table}  ${key}`);

    // Literals written INSIDE a non-literal argument are keys too —
    // t(isEdit ? '编辑' : '新建') is five such sites — and the scan at the top
    // of this file skips the whole call, because the first character after the
    // parenthesis is not a quote.
    for (const [file, src] of CLEANED)
      for (const arg of firstArguments(src)) {
        if (!arg.text || isLiteralArgument(arg.text)) continue;
        LITERAL.lastIndex = 0;
        let m;
        while ((m = LITERAL.exec(arg.text))) {
          const key = unescapeLiteral(m[2]);
          if (!HAN.test(key) || resolves(en.translation, key)) continue;
          const line = src.slice(0, arg.start).split('\n').length;
          missing.push(`${rel(file)}:${line}  ${key}`);
        }
      }

    expect(
      missing,
      `These are rendered as t(<value>) and the value is the key, so an ` +
        `English-locale operator reads the Chinese. Add each to ` +
        `src/i18n/locales/en.json:\n  ` +
        missing.join('\n  '),
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
   * The Chinese-source scan above only catches t() calls keyed by literal
   * Han text — it cannot see the v2 console's `console.*` dotted keys
   * (t('console.playground.no_models', 'no models available')), because the
   * key itself carries no Han characters to trigger the check. For that
   * family, an English key silently missing while zh.json still carries it
   * puts the raw identifier — `console.playground.no_models` — on the English
   * console, or the Chinese string when the call passes no default, with no
   * red test anywhere. This closes that gap for the `console.*` namespace
   * specifically (0 violations on HEAD).
   */
  it('every console.* key present in zh.json also resolves in en.json', () => {
    const missing = consoleKeys(zh).filter((k) => !resolvesDotted(en, k));
    expect(
      missing,
      `These console.* keys exist in zh.json but not en.json. An ` +
        `English-locale operator reads the identifier, or the Chinese, for ` +
        `each:\n  ` +
        missing.join('\n  '),
    ).toEqual([]);
  });

  /*
   * And the other direction, which has no fallback to soften it.
   *
   * zh is a terminal locale: fallbackLng gives it an empty list, so a
   * console.* key that only en.json carries renders its own identifier —
   * console.settings.session_registry_disabled — to a Chinese operator. That
   * is deliberate (a Chinese-source key IS its own Chinese string, and a
   * blanket zh -> en fallback would answer 265 of those lookups in English),
   * and it is exactly why the dotted family needs the symmetric gate: for it,
   * key-as-value is never a readable string.
   *
   * 0 violations today, which is when the assertion is cheap — the surface is
   * one key away from growing every time a lane adds console.* copy.
   */
  it('every console.* key present in en.json also resolves in zh.json', () => {
    const missing = consoleKeys(en).filter((k) => !resolvesDotted(zh, k));
    expect(
      missing,
      `zh has no fallback, so these render as the raw identifier for a ` +
        `Chinese operator. Add each to src/i18n/locales/zh.json:\n  ` +
        missing.join('\n  '),
    ).toEqual([]);
  });

  /*
   * The two console.* symmetry checks above only compare bundle-to-bundle:
   * a key present in NEITHER bundle is symmetric (vacuously) and neither
   * catches it. That is exactly how 15 keys reached the v2 console — 11 in
   * CommandPalette/index.jsx, 3 in Admin/Diagnostics + Admin/Users
   * (console.common.yes/no/error), 1 in Settings/index.jsx — every one of
   * them called with a literal console.* key and an English default, so the
   * page still rendered, in the DEFAULT text, on every locale; nothing here
   * was ever exercised by a test that would have caught the identifier or
   * the fallback-to-Chinese cases the other gates in this file are named
   * for. This closes that gap for the two directories a v2 console lane
   * most often edits: it reads what src/pages/v2 and src/components
   * actually CALL, not what either bundle happens to declare.
   *
   * The floor on files/keys scanned is a canary for the glob itself — an
   * empty match set would make the missing-keys assertion below pass
   * vacuously, same reasoning as the other fail-fast floors in this repo's
   * structural gates.
   */
  it('every console.* key referenced under src/pages/v2 and src/components resolves in en.json', () => {
    const scanDirs = ['src/pages/v2', 'src/components'].map((d) =>
      path.resolve(process.cwd(), d),
    );
    const scannedFiles = [];
    for (const dir of scanDirs) sourceFiles(dir, scannedFiles);
    expect(
      scannedFiles.length,
      'this gate scanned suspiciously few source files — the directory list ' +
        'or the glob it reuses from sourceFiles() may be broken',
    ).toBeGreaterThanOrEqual(20);

    const referenced = new Map();
    for (const file of scannedFiles) {
      const src = fs.readFileSync(file, 'utf8');
      T_CALL.lastIndex = 0;
      let m;
      while ((m = T_CALL.exec(src))) {
        const key = unescapeLiteral(m[2]);
        if (!key.startsWith('console.')) continue;
        if (!referenced.has(key)) referenced.set(key, rel(file));
      }
    }
    expect(
      referenced.size,
      'this gate matched suspiciously few console.* key references — the ' +
        'T_CALL regex or the console. prefix filter may be broken',
    ).toBeGreaterThanOrEqual(500);

    const missing = [...referenced]
      .filter(([key]) => !resolvesDottedPlural(en, key))
      .map(([key, file]) => `${file}  ${key}`);
    expect(
      missing,
      `These console.* keys are called from src/pages/v2 or src/components ` +
        `and en.json has neither the key nor its plural-suffixed form, so ` +
        `every locale — English included — shows the raw identifier. Add ` +
        `each to src/i18n/locales/en.json (and src/i18n/locales/zh.json, ` +
        `for the symmetry gate above):\n  ` +
        missing.join('\n  '),
    ).toEqual([]);
  });

  /*
   * What used to be here: a per-locale ceiling on untranslated keys for
   * fr / ja / ru / vi (221 / 221 / 221 / 217), ratcheting four bundles that
   * i18n.js registered.
   *
   * i18n.js no longer registers them, so those numbers measured files no
   * browser could reach — a ratchet on a shelf. Which languages ship, and the
   * ratio each shipped bundle has to clear, is one question and it now has one
   * owner: locale-coverage.test.js. Re-registering fr there without finishing
   * the translation is what turns red, which is the condition this ceiling was
   * standing in for.
   *
   * The files themselves stay in the tree; the same test pins that too.
   */
});
