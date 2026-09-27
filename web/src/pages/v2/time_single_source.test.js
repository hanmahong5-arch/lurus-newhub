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
 * Source lock: every page under web/src/pages/v2 renders timestamps through
 * helpers/formatting.js (formatTime / formatClockTime / formatRelativeTime /
 * formatTimeUTC / formatShortTs). Six pages used to carry a private
 * fmtTime/fmtWhen/fmtTs, and the copies had drifted: Audit rendered UTC while
 * Log/Authz rendered local (8h apart across two tabs), Settings re-declared
 * formatRelativeTime by name, and Dashboard's fmtTs took milliseconds but was
 * handed unix seconds — the console home showed every recent request in
 * January 1970. A page that needs a new shape adds a helper next to the
 * others, with a test, rather than a seventh local copy.
 *
 * `new Date(...)` is required before .toLocale*String so the many
 * Number.prototype.toLocaleString call sites (counts, money) stay untouched.
 * Fails fast (not silently passes) if the scan finds zero files.
 */
import fs from 'node:fs';
import path from 'node:path';
import { describe, it, expect } from 'vitest';

const PAGES_DIR = path.resolve(process.cwd(), 'src/pages/v2');

function sourceFiles(dir, out = []) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      sourceFiles(p, out);
    } else if (/\.jsx?$/.test(entry.name) && !/\.test\./.test(entry.name)) {
      out.push(p);
    }
  }
  return out;
}

const rel = (p) => path.relative(process.cwd(), p).replace(/\\/g, '/');

const PRIVATE_FORMATTER = /const\s+(fmt(Time|When|Ts)|formatRelativeTime)\s*=/g;
const INLINE_DATE_RENDER =
  /new Date\([^)]*\)\s*\.toLocale(Date|Time)?String\(|\.toISOString\(/g;

// Inline renders still standing when the lock landed (cycle-18 L4), counted
// per file. Two-sided like file_size_ratchet: a new site fails the file, and
// removing one without lowering its row fails too, so the table cannot go
// stale. Private fmtTime/fmtWhen/fmtTs declarations get no allowance at all.
const KNOWN_INLINE = {
  'src/pages/v2/Analytics/Rankings.jsx': 1,
  'src/pages/v2/Tasks/index.jsx': 2,
  'src/pages/v2/Tenants/CreditPoolDrawer.jsx': 2,
  'src/pages/v2/Tenants/InvitesDrawer.jsx': 2,
  'src/pages/v2/Tenants/StatsDrawer.jsx': 1,
  'src/pages/v2/Token/index.jsx': 1,
};

const scan = (src, re) => {
  const hits = [];
  re.lastIndex = 0;
  let m;
  while ((m = re.exec(src))) {
    hits.push({ line: src.slice(0, m.index).split('\n').length, text: m[0] });
  }
  return hits;
};

describe('time formatting has a single source under pages/v2', () => {
  it('no page declares its own time formatter or renders a Date inline', () => {
    const files = sourceFiles(PAGES_DIR);
    expect(files.length).toBeGreaterThan(0);

    const offenders = [];
    const stale = [];
    for (const file of files) {
      const src = fs.readFileSync(file, 'utf8');
      const name = rel(file);
      for (const h of scan(src, PRIVATE_FORMATTER)) {
        offenders.push(`${name}:${h.line}  ${h.text}`);
      }
      const inline = scan(src, INLINE_DATE_RENDER);
      const allowed = KNOWN_INLINE[name] ?? 0;
      if (inline.length > allowed) {
        for (const h of inline) offenders.push(`${name}:${h.line}  ${h.text}`);
      } else if (inline.length < allowed) {
        stale.push(`${name}: ${allowed} → ${inline.length}`);
      }
    }

    expect(
      offenders,
      `These pages format time on their own instead of through ` +
        `helpers/formatting.js:\n  ` +
        offenders.join('\n  '),
    ).toEqual([]);
    expect(
      stale,
      `Inline renders were removed — lower the KNOWN_INLINE row so the win ` +
        `is kept:\n  ` +
        stale.join('\n  '),
    ).toEqual([]);
  });
});
