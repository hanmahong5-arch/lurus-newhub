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

// The v2 console shipped ten WIPBanner renders across four pages, telling
// customers that Chat, Flows, Models and Settings were "design-mock only".
// Chat was running real completions the whole time, and the Models banner
// named a capability whose endpoints had already shipped. The banners were
// removed page by page; this holds the total at zero so the component can only
// come back through a deliberate edit to THIS file.
//
// Scope limits belong in a code comment. If a surface genuinely is not
// implemented, do not render it — a customer seeing a warning box cannot tell
// "we chose not to build this" from "this product is unfinished".
const V2_PAGES = path.resolve(__dirname);

const walk = (dir) => {
  const out = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      out.push(...walk(full));
      continue;
    }
    if (!/\.(jsx?|tsx?)$/.test(entry.name)) continue;
    // Test files may still NAME the component — several assert its absence.
    if (/\.(test|spec)\.[jt]sx?$/.test(entry.name)) continue;
    out.push(full);
  }
  return out;
};

describe('v2 console — no work-in-progress banners', () => {
  it('no page under src/pages/v2 imports or renders WIPBanner', () => {
    const files = walk(V2_PAGES);
    // Guard the guard: an empty file list would make this vacuously green.
    expect(files.length).toBeGreaterThan(10);

    const offenders = files.filter((f) =>
      fs.readFileSync(f, 'utf8').includes('WIPBanner'),
    );

    expect(
      offenders.map((f) => path.relative(V2_PAGES, f)),
      'a v2 page references WIPBanner — implement the surface, hide it, or ' +
        'record the scope limit in a code comment instead of in the UI',
    ).toEqual([]);
  });
});
