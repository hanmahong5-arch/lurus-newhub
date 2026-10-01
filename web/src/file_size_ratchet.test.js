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
  // 887 → 843 (2026-09-22 cycle-15 P4): nav section/item rendering moved to
  // components/hifi/HfNav.jsx with the collapsible admin sections.
  'components/hifi/HFShell.jsx': 757, // → 757 cycle-19: theme hook → useThemeToggle.js, identity cluster → HfUserMenu.jsx
  'components/settings/AuthSettingPage.jsx': 1086,
  'components/settings/SystemSetting.jsx': 1490,
  'components/settings/personal/cards/NotificationSettings.jsx': 930,
  'helpers/render.jsx': 2071,
  'helpers/utils.jsx': 899,
  'pages/Setting/Ratio/UpstreamRatioSync.jsx': 873,
  // pages/v2/Admin/ModelRateLimits/index.jsx left the list (cycle 14, 915 → 757):
  // LimitModal + inputStyle extracted to ModelRateLimits/LimitModal.jsx.
  'pages/v2/Billing/index.jsx': 803, // redeemFailure() extracted to Billing/redeemFailure.js (cycle-13 hand-finish); → 803 cycle-19: trend → Billing/TrendBars.jsx
  // 1908 → 1904 (cycle-18 L1): the list and upstream-models reads moved onto
  // hooks/common/useTenantRead.js; a failed read renders HfLoadError.
  // 1904 → 1840 (cycle-18 L3): SyncModelsModal and ChannelModal dropped their
  // hand-rolled backdrop/panel for components/hifi/HfDialog.
  'pages/v2/Channel/index.jsx': 1840,
  'pages/v2/Chat/index.jsx': 770, // → 770 cycle-19: session list → Chat/SessionList.jsx
  // 1190 → 1179 (cycle-18 L4): the private fmtTs (ms-based, fed seconds —
  // every recent request read as 1970) gave way to helpers/formatting.js
  // formatShortTs.
  'pages/v2/Dashboard/index.jsx': 1055, // 1242 → 1190 (cycle-15 P3): daily bucketing moved to components/hifi/activitySeries.js; → 1055 cycle-19: KPI strip → KpiCards.jsx, realtime cards → LivePanel.jsx
  'pages/v2/Flows/index.jsx': 1446,
  // 1630 → 1623 (cycle-18 L4): the private fmtTime gave way to
  // helpers/formatting.js formatClockTime({ ms: false }).
  'pages/v2/Log/index.jsx': 1611, // 1731 → 1684 (cycle-15 P4): stat header extracted to Log/StatHeader.jsx; → 1630: routing trace moved to Log/RouteAttempts.jsx; → 1611 cycle-19: CSV export → ExportCsvButton.jsx, outcome chip → OutcomeChip.jsx
  'pages/v2/Playground/index.jsx': 1083,
  // 805 → 804 (cycle-18 L1): the pricing read moved onto useTenantRead; the
  // failed-read badge renders its dash as text (a string count skips plural).
  'pages/v2/Pricing/index.jsx': 804,
  // 1909 → 1889 (cycle-18 L4): the page's own formatRelativeTime copy gave
  // way to the same-named helper in helpers/formatting.js.
  'pages/v2/Settings/index.jsx': 1889,
  'pages/v2/Tenants/index.jsx': 753, // 995 → 920 (cycle 14), → 789 (LimitsModal moved to Tenants/LimitsModal.jsx): StatsDrawer extracted to Tenants/StatsDrawer.jsx; failed-read panel is the shared components/hifi/HfLoadError.jsx; → 753 (CreateModal's backdrop/panel/footer now components/hifi/HfDialog.jsx)
  // 1639 → 1632 (cycle-18 L1): the tokens and projects reads moved onto
  // useTenantRead.
  // 1632 → 1595 (cycle-18 L3): CreateModal dropped its hand-rolled
  // backdrop/panel for components/hifi/HfDialog.
  'pages/v2/Token/index.jsx': 1540, // → 1541 cycle-19: snippet builder → Token/snippets.js; → 1540 cycle-20: page grid moved to .hf-token-* classes in hifi-tokens.css
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
