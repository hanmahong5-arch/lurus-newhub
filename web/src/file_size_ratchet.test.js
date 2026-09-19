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
 * File-size ratchet for web/src (cycle 12 W, 2026-09-19). Every non-test
 * .js/.jsx above THRESHOLD lines on that day is listed with its line count as
 * its ceiling. A listed file may shrink (lower its row in the same change), it
 * may not grow past its ceiling, and no unlisted file may cross THRESHOLD.
 * This is not a review of the listed files; it is what stops the next cycle's
 * extractions from being undone quietly.
 */
const CEILINGS = {
  'components/hifi/HFShell.jsx': 950,
  'components/settings/AuthSettingPage.jsx': 1086,
  'components/settings/SystemSetting.jsx': 1490,
  'components/settings/personal/cards/NotificationSettings.jsx': 930,
  'helpers/render.jsx': 2071,
  'helpers/utils.jsx': 899,
  'pages/Setting/Ratio/UpstreamRatioSync.jsx': 873,
  'pages/v2/Admin/ModelRateLimits/index.jsx': 915,
  'pages/v2/Billing/index.jsx': 835,
  'pages/v2/Channel/index.jsx': 1906,
  'pages/v2/Chat/index.jsx': 879,
  'pages/v2/Dashboard/index.jsx': 1247,
  'pages/v2/Flows/index.jsx': 1446,
  'pages/v2/Log/index.jsx': 1769,
  'pages/v2/Playground/index.jsx': 1084,
  'pages/v2/Pricing/index.jsx': 801,
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
            `${r}: ${n} lines, ceiling ${CEILINGS[r]} — lower the ceiling`,
          );
      } else if (n > THRESHOLD) {
        unlisted.push(`${r}: ${n} lines and not in CEILINGS`);
      }
    }
    for (const r of Object.keys(CEILINGS)) {
      if (!seen.has(r))
        over.push(`${r}: listed but not found — remove the row`);
    }
    if (shrank.length)
      console.info(
        `files below their ceiling (lower them):\n  ${shrank.join('\n  ')}`,
      );
    expect(
      [...over, ...unlisted],
      'a file may shrink, not grow: extract from it or raise its row with a reason in the commit',
    ).toEqual([]);
  });
});
