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
 * Lock for the L5 acceptor finding (cycle7 repair round 2, items 2/10/17/27;
 * repair round 3 item 2): the `__lurus_force_http1` param_override key had
 * zero visibility in the console — an operator could not discover the key,
 * its scope, or the bypassed side calls without reading the integration
 * guide out of band.
 *
 * Round 3 fix: the previous version of this file compared a local
 * HELP_TEXT_ZH constant against itself and against the i18n JSON — it never
 * read what string the modal actually passes to t(), so it could not go red
 * if the modal's help text drifted from the constant. This version extracts
 * the i18n key straight out of EditChannelModal.jsx's source and checks that
 * key against the locale files, so a source/locale drift is what turns it
 * red — not an edit to this test file.
 *
 * This is a structural (source-text) check, not a render test: mounting the
 * full EditChannelModal form body requires a much larger harness (see
 * tc_EditChannelModal_keyreveal.test.jsx's header comment on why the sheet is
 * kept closed in that file). Reading the source directly is the narrowest
 * check that still goes red if the help text — or its translation — is
 * removed or shortened.
 */
import fs from 'node:fs';
import path from 'node:path';
import { describe, it, expect } from 'vitest';

import en from '../../../../i18n/locales/en.json';
import zh from '../../../../i18n/locales/zh.json';

const MODAL_PATH = path.resolve(
  process.cwd(),
  'src/components/table/channels/modals/EditChannelModal.jsx',
);

// extractForceHTTP1HelpKey pulls the exact string literal the modal passes
// to t() for the __lurus_force_http1 help text, by locating the marker and
// walking outward to the enclosing single quotes. Works because the help
// text itself contains no single quotes (it uses full-width 「」 brackets,
// not ASCII apostrophes) — if that ever changes this extraction, not a
// hardcoded copy of the string, is what must be updated.
function extractForceHTTP1HelpKey(source, searchFrom) {
  const markerIndex = source.indexOf('__lurus_force_http1', searchFrom);
  if (markerIndex === -1) return null;
  const quoteStart = source.lastIndexOf("'", markerIndex);
  if (quoteStart === -1) return null;
  const quoteEnd = source.indexOf("'", quoteStart + 1);
  if (quoteEnd === -1) return null;
  return source.slice(quoteStart + 1, quoteEnd);
}

describe('EditChannelModal param_override help text (__lurus_force_http1)', () => {
  const source = fs.readFileSync(MODAL_PATH, 'utf8');
  const paramOverrideFieldIndex = source.indexOf("field='param_override'");
  const nextFieldIndex = source.indexOf(
    "field='header_override'",
    paramOverrideFieldIndex,
  );
  const helpKey = extractForceHTTP1HelpKey(source, paramOverrideFieldIndex);

  it('renders help text for __lurus_force_http1 next to the param_override field', () => {
    expect(paramOverrideFieldIndex).toBeGreaterThan(-1);
    expect(nextFieldIndex).toBeGreaterThan(paramOverrideFieldIndex);

    const helpTextIndex = source.indexOf(
      '__lurus_force_http1',
      paramOverrideFieldIndex,
    );
    expect(helpTextIndex).toBeGreaterThan(-1);
    // The help text must be part of the SAME Form.TextArea block as
    // param_override (the next field definition after it), not some
    // unrelated mention elsewhere in this 3000+ line file.
    expect(helpTextIndex).toBeLessThan(nextFieldIndex);
  });

  it('the i18n key used at the help-text site is translated (zh identity + en, both present)', () => {
    expect(helpKey).toBeTruthy();
    // zh.json is an identity map (the JSON key IS the source Chinese text);
    // a shortened/edited value here — without touching this test file —
    // must fail this assertion.
    expect(zh.translation[helpKey]).toBe(helpKey);

    const enValue = en.translation[helpKey];
    expect(enValue).toBeTruthy();
    expect(enValue).not.toBe(helpKey);
    expect(enValue).toMatch(/HTTP\/1\.1/);
  });

  it('names the bypass categories from the L5 coverage-story repair (item 1), not just the covered path', () => {
    // Decision recorded in the L5 repair (routing-resilience-limits-13,
    // cycle7 round 3 item 1): task FetchTask polls now DO go through
    // app.GetHttpClientFor (so hailuo's status query is no longer a bypass
    // example); AWS AKSK mode, Coze's result-poll, Vertex's token exchange,
    // Midjourney's own image fetch, baidu's token exchange and the
    // dify/replicate file uploads remain bypassed. The help text must name
    // bypassed calls precisely, not the whole channel type, and must not
    // claim hailuo is bypassed now that it is covered.
    for (const term of [
      'AKSK',
      'Coze 结果轮询',
      'Vertex 换取 token',
      'baidu 换取 token',
    ]) {
      expect(helpKey).toContain(term);
    }
    expect(helpKey).not.toContain('hailuo');

    const enValue = en.translation[helpKey];
    for (const term of [
      'AKSK',
      "Coze's result-poll",
      "Vertex's token exchange",
      "baidu's token exchange",
    ]) {
      expect(enValue).toContain(term);
    }
    expect(enValue).not.toContain('hailuo');
  });
});
