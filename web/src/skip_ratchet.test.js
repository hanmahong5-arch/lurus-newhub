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
 * Skipped-test ratchet for web/src, the sibling of file_size_ratchet.test.js.
 * A skipped spec is a green line that proves nothing, and the count only ever
 * drifted upward because nothing measured it: 93 on 2026-09-27 (cycle 18),
 * spread over 49 files. CEILING is that measured count, and the ratchet is
 * two-sided for the same reason the size one is — a drop has to be recorded
 * by the change that won it, or the slack quietly becomes permission to skip
 * something else next week.
 *
 * Only the three vitest callers are counted, so a `.skip(` on an unrelated
 * object (a Semi UI prop, a helper) does not count against the budget.
 */
const CEILING = 93;
const SKIP_CALL = /\b(?:it|describe|test)\.(?:skip|todo)\(/g;

const HERE = path.dirname(fileURLToPath(import.meta.url));
const SELF = fileURLToPath(import.meta.url);

const isTestFile = (name) => /\.(test|spec)\.(js|jsx|ts|tsx)$/.test(name);

function walk(dir, out = []) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) walk(full, out);
    else if (isTestFile(entry.name) && full !== SELF) out.push(full);
  }
  return out;
}

const skipCount = (file) =>
  (fs.readFileSync(file, 'utf8').match(SKIP_CALL) || []).length;

describe('web/src skipped-test ratchet', () => {
  const files = walk(HERE);
  const rel = (abs) => path.relative(HERE, abs).split(path.sep).join('/');

  it('scans the tree at all (fail fast if the walk broke)', () => {
    expect(files.length).toBeGreaterThan(100);
  });

  it('the number of skipped/todo specs may go down, never up', () => {
    const perFile = files
      .map((f) => [rel(f), skipCount(f)])
      .filter(([, n]) => n > 0)
      .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
    const total = perFile.reduce((sum, [, n]) => sum + n, 0);
    const listing = perFile.map(([f, n]) => `${f}: ${n}`).join('\n');

    expect(
      total,
      `${total} skipped/todo specs, ceiling ${CEILING} (+${total - CEILING}) — un-skip or delete instead of raising the ceiling:\n${listing}`,
    ).toBeLessThanOrEqual(CEILING);
    expect(
      total,
      `${total} skipped/todo specs, below the ceiling ${CEILING} — lower CEILING to ${total} in the same change, or the slack becomes permission to skip again`,
    ).toBeGreaterThanOrEqual(CEILING);
  });
});
