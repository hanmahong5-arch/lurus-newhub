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
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

/*
 * File-size ratchet for web/src. Every non-test .js/.jsx above THRESHOLD
 * lines is listed with its measured line count as its ceiling. The ratchet
 * is two-sided: a listed file may not grow past its ceiling, and a listed
 * file BELOW its ceiling fails too, with the replacement row to paste. It is
 * not a review of the listed files; it is what stops the next cycle's
 * extractions from being undone quietly.
 *
 * The shrink side used to be a console.info nobody read, so the numbers went
 * stale within a day of being written. Since cycle 13 (plan section 2,
 * "只许缩不许长") the win has to be recorded by the same change that won it,
 * and growth is paid for by extracting into a sibling module rather than by
 * a quiet +40 here.
 *
 * Measured 2026-09-20 (cycle-13 W). Log/index.jsx came down from 1794 by the
 * extraction of pages/v2/Log/window.js (the query time bound) and
 * pages/v2/Log/row.js (row interpretation); Dashboard/index.jsx came down
 * from 1324 by L7's LoadErrorPanel extraction.
 */
const CEILINGS = {
  // 950 → 948 (2026-09-22): the local readTenantSlug copy gave way to the
  // shared one in hooks/common/useTenantSlug.js.
  // 948 → 887 (2026-09-22): the root tenant list and its localStorage
  // "switch" went — the server never followed it (403 TENANT_MISMATCH).
  'components/hifi/HFShell.jsx': 887,
  'components/settings/AuthSettingPage.jsx': 1086,
  'components/settings/SystemSetting.jsx': 1490,
  'components/settings/personal/cards/NotificationSettings.jsx': 930,
  'helpers/render.jsx': 2071,
  'helpers/utils.jsx': 899,
  'pages/Setting/Ratio/UpstreamRatioSync.jsx': 873,
  'pages/v2/Admin/ModelRateLimits/index.jsx': 915,
  'pages/v2/Billing/index.jsx': 833, // redeemFailure() extracted to Billing/redeemFailure.js (cycle-13 hand-finish)
  'pages/v2/Channel/index.jsx': 1908, // +2 (cycle-15 P1): import of the extracted ChannelTypeLabel/ChannelTypeSelect (components/hifi/HfModelName.jsx)
  'pages/v2/Chat/index.jsx': 879,
  'pages/v2/Dashboard/index.jsx': 1242, // +4 (cycle-13 hand-finish): pre-settle caption — one const and a three-line rationale
  'pages/v2/Flows/index.jsx': 1446,
  'pages/v2/Log/index.jsx': 1731, // +5 (cycle-15 P1): vendor logo in both model cells + status column moved beside the time
  'pages/v2/Playground/index.jsx': 1084,
  'pages/v2/Pricing/index.jsx': 805, // +4 (cycle-15 P1): vendor logo in the model cell
  'pages/v2/Settings/index.jsx': 1909,
  'pages/v2/Tenants/index.jsx': 995,
  'pages/v2/Token/index.jsx': 1671,
};

const THRESHOLD = 800;
const HERE = path.dirname(fileURLToPath(import.meta.url));
const SRC = HERE;

const isSource = (name) =>
  /\.(js|jsx|ts|tsx)$/.test(name) &&
  !/\.(test|spec)\.(js|jsx|ts|tsx)$/.test(name);

function walk(dir, out = []) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) walk(full, out);
    else if (isSource(entry.name)) out.push(full);
  }
  return out;
}

const lineCount = (file) => {
  const text = fs.readFileSync(file, 'utf8');
  if (text.length === 0) return 0;
  return text.split('\n').length - (text.endsWith('\n') ? 1 : 0);
};

describe('web/src file-size ratchet', () => {
  const files = walk(SRC);
  const rel = (abs) => path.relative(SRC, abs).split(path.sep).join('/');

  it('scans the tree at all (fail fast if the walk broke)', () => {
    expect(files.length).toBeGreaterThan(200);
  });

  it('no listed file grew past its ceiling and no unlisted file crossed the threshold', () => {
    const over = [];
    const unlisted = [];
    const shrank = [];
    const seen = new Set();
    for (const file of files) {
      const r = rel(file);
      const n = lineCount(file);
      if (Object.prototype.hasOwnProperty.call(CEILINGS, r)) {
        seen.add(r);
        if (n > CEILINGS[r])
          over.push(
            `${r}: ${n} lines, ceiling ${CEILINGS[r]} (+${n - CEILINGS[r]})`,
          );
        else if (n < CEILINGS[r])
          shrank.push(
            `${r}: ${n} lines, ceiling ${CEILINGS[r]} — replace that row with:  '${r}': ${n},`,
          );
      } else if (n > THRESHOLD) {
        unlisted.push(`${r}: ${n} lines and not in CEILINGS`);
      }
    }
    for (const r of Object.keys(CEILINGS)) {
      if (!seen.has(r))
        over.push(`${r}: listed but not found — remove the row`);
    }
    expect(
      shrank,
      'a file is below its ceiling — lower the row in the same change that shrank the file, or the ratchet quietly becomes permission to grow back',
    ).toEqual([]);
    expect(
      [...over, ...unlisted],
      'a file may shrink, not grow: extract from it or raise its row with a reason in the commit',
    ).toEqual([]);
  });
});
