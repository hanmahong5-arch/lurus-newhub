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
 * Source lock: no page under web/src/pages/v2 inlines an API.get call whose
 * argument names the models endpoint — the pages that need the list go
 * through useTenantModels (web/src/hooks/models/useTenantModels.js) instead. Playground/index.jsx once did exactly that
 * with a parser that treated the response object as an array — the swap
 * dropdown never left "loading". This test fails fast (not silently pass)
 * if it ever scans zero files, and matches only API.get( calls whose
 * argument contains /models — the add-model call at Models/index.jsx:138 is
 * an API.post to the same path (POST, not GET) and must not trip it.
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

// API.get( calls whose argument literal contains /models — matches both
// '/models?...' (query string) and template literals ending in `/models`.
const INLINE_MODELS_GET = /API\.get\(\s*[`'"][^`'"]*\/models\b[^`'"]*[`'"]/g;

describe('no inline /models fetch under pages/v2', () => {
  it('no page under pages/v2 inlines an API.get for the models endpoint', () => {
    const files = sourceFiles(PAGES_DIR);
    // Fail fast rather than silently pass if the scan finds nothing.
    expect(files.length).toBeGreaterThan(0);

    const offenders = [];
    for (const file of files) {
      const src = fs.readFileSync(file, 'utf8');
      let m;
      INLINE_MODELS_GET.lastIndex = 0;
      while ((m = INLINE_MODELS_GET.exec(src))) {
        const line = src.slice(0, m.index).split('\n').length;
        offenders.push(`${rel(file)}:${line}  ${m[0]}`);
      }
    }

    expect(
      offenders,
      `These pages fetch /models directly instead of through useTenantModels ` +
        `(web/src/hooks/models/useTenantModels.js):\n  ` +
        offenders.join('\n  '),
    ).toEqual([]);
  });
});
