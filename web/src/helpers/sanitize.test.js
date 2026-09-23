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
import DOMPurify from 'dompurify';
import { sanitizeHtml, sanitizeOperatorHtml, escapeHtml } from './sanitize';

/*
 * Three halves, and all three are needed.
 *
 * (1) What the STRICT profile strips. Its job is content whose author is not
 *     the operator — an upstream model's ```html fence, a third-party release
 *     note, a legal document. Four of these cases are the cycle-12 repair and
 *     fail on the tree as it stood at the start of the round (measured: the
 *     two <style> cases and the two form-control cases): both survived
 *     DOMPurify's `html` profile, which ALLOWS style/form/input/button, and
 *     FORBID_CONTENTS only bites tags that are NOT allowed.
 *
 *     The <style> case deliberately puts the tag AFTER body content. A
 *     document that OPENS with <style> is not a test of the sanitiser at all:
 *     the HTML parser hoists a leading <style> into <head> and DOMPurify
 *     returns body.innerHTML, so the case passes whatever the sanitiser does.
 *     The first version of this file made exactly that mistake and the
 *     shipped DocumentRenderer comment cited it as proof.
 *
 * (2) What the OPERATOR profile keeps. A root admin types this HTML into
 *     System settings and the settings UI says "supports HTML" in so many
 *     words (BrandingSettingPage.jsx). Layout CSS and target="_blank" have to
 *     survive, with rel forced; the script / event-handler / form-control
 *     floor is the same as strict.
 *
 * (3) The structural gate: every dangerouslySetInnerHTML in src/ names one of
 *     the sanitisers at the sink. Before cycle 12, sanitizeHtml existed and
 *     had zero call sites in the whole tree (only escapeHtml, from CodeViewer,
 *     was wired) while ten sinks fed it raw operator- or model-authored HTML.
 *     A per-sink unit test would not have caught that; only an enumeration
 *     does. The gate parses ONE textual shape, so it also counts bare
 *     occurrences of the attribute name and fails when the two disagree —
 *     otherwise a sink spelled some other way (a template literal, a call
 *     with an object argument, a pre-built {__html} object passed by name)
 *     would be invisible rather than red.
 */

describe('sanitizeHtml — content whose author is not the operator', () => {
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
      // The tag comes after <p> on purpose: a leading <style> is hoisted into
      // <head> by the parser, so that arrangement proves nothing.
      name: 'strips a style element that appears after body content',
      input:
        '<p>x</p><style>body{background:url(https://evil.example)}</style>',
      absent: ['<style', 'evil.example'],
      present: ['<p>x</p>'],
    },
    {
      name: 'strips a style element carrying an external @import',
      input: '<p>x</p><style>@import url(https://evil.example/a.css);</style>',
      absent: ['@import', 'evil.example'],
      present: ['<p>x</p>'],
    },
    {
      // This is the credential-prompt half of "model output paints the
      // console": an overlay with somewhere to type.
      name: 'strips form controls so model output cannot paint a login form',
      input:
        '<form action="https://evil.example"><input name="password" type="password"><button>sign in</button></form>',
      absent: ['<form', '<input', '<button', 'evil.example'],
      present: [],
    },
    {
      name: 'strips a bare input outside a form',
      input: '<div><input name="apikey"></div>',
      absent: ['<input'],
      present: ['<div>'],
    },
    {
      // The overlay half of "model output paints the console": an inline
      // style is enough to cover the whole origin, no <style> block needed.
      name: 'strips the style attribute so model output cannot paint an overlay',
      input:
        '<div style="position:fixed;inset:0;background:#fff;z-index:9999">x</div>',
      absent: ['position:fixed', 'style='],
      present: ['<div>x</div>'],
    },
    {
      // A model asked for a diagram answers with SVG; the drawing must
      // survive while the script and the handler inside it do not.
      name: 'keeps an svg drawing but strips its script and handlers',
      input:
        '<svg viewBox="0 0 10 10" onload="steal()"><script>steal()</script><circle cx="5" cy="5" r="4"></circle></svg>',
      absent: ['onload', 'script', 'steal'],
      present: ['<svg', '<circle'],
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
    {
      // Strict is for authors the operator did not choose; a link that takes
      // the tab over is the lesser evil versus handing out window.opener.
      name: 'drops target so untrusted content cannot open a named window',
      input: '<a href="https://ok.example" target="_blank">link</a>',
      absent: ['target'],
      present: ['<a href="https://ok.example">link</a>'],
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

describe('sanitizeOperatorHtml — HTML a root admin typed into settings', () => {
  it('keeps a style block after body content, so a customer home page keeps its CSS', () => {
    const out = sanitizeOperatorHtml(
      '<div class="hero">hi</div><style>.hero{padding:40px}</style>',
    );
    expect(out).toContain('<style>');
    expect(out).toContain('.hero{padding:40px}');
  });

  it('keeps target="_blank" and forces rel="noopener noreferrer"', () => {
    const out = sanitizeOperatorHtml(
      '<a href="https://docs.acme" target="_blank">Docs</a>',
    );
    expect(out).toContain('target="_blank"');
    expect(out).toContain('rel="noopener noreferrer"');
  });

  it('forces rel even when the author supplied a weaker one', () => {
    const out = sanitizeOperatorHtml(
      '<a href="https://docs.acme" target="_blank" rel="author">Docs</a>',
    );
    expect(out).toContain('rel="noopener noreferrer"');
    expect(out).not.toContain('rel="author"');
  });

  it('leaves a link without target alone', () => {
    const out = sanitizeOperatorHtml('<a href="https://docs.acme">Docs</a>');
    expect(out).toBe('<a href="https://docs.acme">Docs</a>');
  });

  it('keeps the script / handler / javascript: floor', () => {
    expect(sanitizeOperatorHtml('<script>alert(1)</script><b>ok</b>')).toBe(
      '<b>ok</b>',
    );
    expect(sanitizeOperatorHtml('<img src=x onerror=alert(1)>')).not.toContain(
      'onerror',
    );
    expect(
      sanitizeOperatorHtml('<a href="javascript:alert(1)">c</a>'),
    ).not.toContain('javascript:');
  });

  it('still refuses form controls — an operator links to a form, not embeds one', () => {
    const out = sanitizeOperatorHtml(
      '<form action="/x"><input name="a"><button>go</button></form>',
    );
    expect(out).not.toContain('<form');
    expect(out).not.toContain('<input');
    expect(out).not.toContain('<button');
  });

  it('leaves the module-global hook registry as it found it', () => {
    /*
     * DOMPurify hooks are module-global and sanitizeOperatorHtml registers
     * one per call, so it has to remove it again.
     *
     * Asserting this through sanitizeHtml would NOT discriminate: the strict
     * profile has no ADD_ATTR target, DOMPurify removes the attribute during
     * attribute filtering, and afterSanitizeAttributes runs after that — so
     * a leaked hook finds no target to act on and the strict output is
     * identical either way. (Checked: deleting the removeHook call leaves
     * that version of this test green.) The property that does change is the
     * registry itself, so measure it directly: a raw sanitize that admits
     * target must come back WITHOUT a forced rel, and the hooks must not
     * pile up one per call.
     */
    sanitizeOperatorHtml('<a href="https://x" target="_blank">l</a>');
    sanitizeOperatorHtml('<a href="https://y" target="_blank">l</a>');
    const afterwards = DOMPurify.sanitize(
      '<a href="https://z" target="_blank">l</a>',
      { USE_PROFILES: { html: true }, ADD_ATTR: ['target'] },
    );
    expect(afterwards).toBe('<a href="https://z" target="_blank">l</a>');
  });

  it('turns null and undefined into an empty string rather than the words', () => {
    expect(sanitizeOperatorHtml(null)).toBe('');
    expect(sanitizeOperatorHtml(undefined)).toBe('');
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

/*
 * Files with an occurrence of the attribute name that is prose, not a sink.
 * Every entry is checked below: the file must still contain the string, and
 * every occurrence the sink scan did not parse must sit on a comment line —
 * so a comment-only excuse cannot quietly hide a sink the scan cannot see.
 */
const MENTION_ONLY = {
  'helpers/sanitize.js':
    'the module header explains which sinks need which profile',
  'components/playground/CodeViewer.jsx':
    'the comment above the highlightedContent useMemo names the sink it feeds',
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
const ATTR = 'dangerouslySetInnerHTML';

const sinks = [];
// file -> how many times the bare attribute name occurs, parsed or not.
const mentions = new Map();
for (const file of walkFiles(SRC)) {
  if (isTestName(path.basename(file))) continue;
  const text = fs.readFileSync(file, 'utf8');
  const occurrences = text.split(ATTR).length - 1;
  if (occurrences > 0) mentions.set(rel(file), occurrences);
  SINK_RE.lastIndex = 0;
  let m;
  while ((m = SINK_RE.exec(text)) !== null) {
    sinks.push({
      file: rel(file),
      // A sink written across several lines keeps prettier's trailing comma
      // inside the capture; drop it so one sink has one spelling here.
      expr: m[1].trim().replace(/,$/, ''),
      line: text.slice(0, m.index).split('\n').length,
    });
  }
}

const SANITISED_AT_SINK = /^(sanitizeHtml|sanitizeOperatorHtml|escapeHtml)\(/;
const SINK_CALL = /^(sanitizeHtml|sanitizeOperatorHtml|escapeHtml)\((.+)\)$/;

/*
 * Which profile each sink is entitled to, and why. Two profiles only help if
 * each sink uses the right one, and nothing else in the tree says which is
 * which: swapping Footer to the strict profile (customer's target="_blank"
 * silently dies) or the html fence to the operator profile (model output may
 * paint the console again) are both one-word edits that every other test
 * here would let through.
 *
 * Keyed by src-relative file, then by the exact argument text at the sink.
 * Add a row when you add a sink; the row is a decision about whose HTML it is.
 */
const PROFILE_BY_SINK = {
  'components/common/DocumentRenderer/index.jsx': {
    content: [
      'sanitizeHtml',
      'About / privacy policy / user agreement, served to anonymous visitors',
    ],
  },
  'components/common/markdown/MarkdownRenderer.jsx': {
    htmlCode: [
      'sanitizeHtml',
      'the text of an upstream model response fence, rendered with no opt-in',
    ],
  },
  'components/layout/Footer.jsx': {
    footer: [
      'sanitizeOperatorHtml',
      'the Footer option, typed by a root admin in System settings',
    ],
  },
  'components/layout/NoticeModal.jsx': {
    noticeContent: [
      'sanitizeOperatorHtml',
      'the Notice option, typed by a root admin',
    ],
    htmlExtra: [
      'sanitizeOperatorHtml',
      'the extra line of an operator-authored announcement',
    ],
    htmlContent: [
      'sanitizeOperatorHtml',
      'the body of an operator-authored announcement',
    ],
  },
  'components/settings/OtherSetting.jsx': {
    'updateData.content': [
      'sanitizeHtml',
      'the same remote release notes, in the legacy settings page',
    ],
  },
  'helpers/utils.jsx': {
    htmlContent: [
      'sanitizeHtml',
      'showNotice(msg, true) toast body; a toast has no use for page CSS',
    ],
  },
  'pages/Home/index.jsx': {
    homePageContent: [
      'sanitizeOperatorHtml',
      'the HomePageContent option; the settings UI advertises HTML support for it',
    ],
  },
};

describe('every dangerouslySetInnerHTML goes through a sanitiser', () => {
  it('finds the sinks at all (fail fast if the scan broke)', () => {
    expect(sinks.length).toBeGreaterThanOrEqual(8);
  });

  it('names a sanitiser at the sink, or carries a recorded reason', () => {
    const raw = sinks.filter(
      (s) =>
        !SANITISED_AT_SINK.test(s.expr) &&
        OFF_SITE_ESCAPED[s.file]?.[s.expr] === undefined,
    );
    expect(
      raw,
      'These dangerouslySetInnerHTML sinks inject a value that was not passed ' +
        'through helpers/sanitize. Wrap it in sanitizeHtml() (content whose ' +
        'author is not the operator) or sanitizeOperatorHtml() (HTML a root ' +
        'admin typed into settings) at the sink, or — when the value is ' +
        'escaped elsewhere on purpose — record it in OFF_SITE_ESCAPED with ' +
        'where:\n  ' +
        raw.map((s) => `${s.file}:${s.line}  __html: ${s.expr}`).join('\n  '),
    ).toEqual([]);
  });

  /*
   * SINK_RE understands one spelling. These shapes are all legal React and
   * none of them match it:
   *   dangerouslySetInnerHTML={{ __html: `<b>${raw}</b>` }}   (the } of ${}
   *                                       closes the [^}] class)
   *   dangerouslySetInnerHTML={{ __html: f(x, {gfm: true}) }}
   *   dangerouslySetInnerHTML={prebuiltHtmlObject}
   * Counting the bare attribute name and comparing catches all three: an
   * unparsed sink shows up as a mention with no matching parse.
   */
  it('parses every occurrence of the attribute, so an unknown spelling is red not invisible', () => {
    const unexplained = [];
    for (const [file, count] of mentions) {
      const parsed = sinks.filter((s) => s.file === file).length;
      const excused = MENTION_ONLY[file] ? 1 : 0;
      if (parsed + excused !== count) {
        unexplained.push(
          `${file}: ${count} occurrence(s) of ${ATTR}, ${parsed} parsed as a ` +
            `sink${excused ? ', 1 excused as prose' : ''}`,
        );
      }
    }
    expect(
      unexplained,
      'An occurrence of ' +
        ATTR +
        ' that the sink regex could not parse is a sink this gate cannot ' +
        'check. Either write it in the shape the regex understands — ' +
        '{{ __html: sanitizeHtml(x) }} — or widen SINK_RE. Do not add it to ' +
        'MENTION_ONLY unless it really is prose:\n  ' +
        unexplained.join('\n  '),
    ).toEqual([]);
  });

  it('uses the profile recorded for each sink, and records one for every sink', () => {
    const wrong = [];
    for (const s of sinks) {
      if (!SANITISED_AT_SINK.test(s.expr)) continue; // covered by OFF_SITE_ESCAPED
      const [, fn, arg] = SINK_CALL.exec(s.expr) ?? [];
      const row = PROFILE_BY_SINK[s.file]?.[arg];
      if (!row) {
        wrong.push(
          `${s.file}:${s.line}  __html: ${s.expr}  — no PROFILE_BY_SINK row. ` +
            'Decide whose HTML this is and add one.',
        );
      } else if (row[0] !== fn) {
        wrong.push(
          `${s.file}:${s.line}  calls ${fn} but is recorded as ${row[0]} (${row[1]})`,
        );
      }
    }
    expect(wrong, wrong.join('\n  ')).toEqual([]);
  });

  it('every PROFILE_BY_SINK row still matches a live sink', () => {
    const stale = [];
    for (const [file, args] of Object.entries(PROFILE_BY_SINK)) {
      for (const [arg, [fn]] of Object.entries(args)) {
        if (!sinks.some((s) => s.file === file && s.expr === `${fn}(${arg})`)) {
          stale.push(`${file}  __html: ${fn}(${arg})`);
        }
      }
    }
    expect(
      stale,
      'These PROFILE_BY_SINK rows no longer match any sink; drop or update ' +
        'them so the table cannot outlive its sinks:\n  ' +
        stale.join('\n  '),
    ).toEqual([]);
  });

  it('every MENTION_ONLY entry is still a real file whose excused occurrences sit on comment lines', () => {
    const stale = [];
    for (const [file, reason] of Object.entries(MENTION_ONLY)) {
      if (!mentions.has(file)) {
        stale.push(`${file} no longer contains ${ATTR} (${reason})`);
        continue;
      }
      // The excuse is "prose": every occurrence the sink scan did not parse
      // must be on a comment line, or the file has a sink spelled in a way
      // the scan cannot see and the excuse is hiding it.
      const lines = fs.readFileSync(path.join(SRC, file), 'utf8').split('\n');
      const parsedLines = new Set(
        sinks.filter((s) => s.file === file).map((s) => s.line),
      );
      lines.forEach((line, i) => {
        if (!line.includes(ATTR) || parsedLines.has(i + 1)) return;
        if (!/^\s*(\/\/|\*|\/\*|\{\/\*)/.test(line)) {
          stale.push(
            `${file}:${i + 1} mentions ${ATTR} outside a comment and outside a parsed sink: ${line.trim()}`,
          );
        }
      });
    }
    expect(stale, stale.join('\n  ')).toEqual([]);
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
