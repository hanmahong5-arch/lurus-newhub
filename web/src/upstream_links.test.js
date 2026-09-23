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
 * A rebrand (276ec19f "rebrand from New API to Ailurus") rewrote the
 * upstream project's name inside URLs as well as in prose. Every such URL
 * then pointed at a repository or file that does not exist:
 *   github.com/QuantumNous/lurus-api            404 (real: new-api)
 *   github.com/Calcium-Ion/lurus-api-worker     404 (real: new-api-worker)
 *   github.com/Calcium-Ion/lurus-api-horizon    404 (real: new-api-horizon)
 *   basellm.github.io/llm-metadata/api/ailurus/ 404 (real: api/newapi/)
 * The last one broke the "official ratio presets" sync outright; the rest
 * broke the upstream attribution the LICENSE requires us to keep.
 * (Status codes checked by hand on 2026-09-23.)
 *
 * Our own name belongs in our own text, never inside someone else's URL.
 */
import { describe, expect, it } from 'vitest';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';

const SRC = join(__dirname);
const RENAMED_UPSTREAM_URL =
  /(github\.com\/(QuantumNous|Calcium-Ion)\/(lurus-api|ailurus)|llm-metadata\/api\/ailurus)/i;

function walk(dir, out = []) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.(jsx?|json)$/.test(name) && !/\.test\./.test(name)) out.push(p);
  }
  return out;
}

describe('upstream URLs keep the upstream name', () => {
  it('no source file points at a renamed (non-existent) upstream URL', () => {
    const hits = [];
    for (const f of walk(SRC)) {
      readFileSync(f, 'utf8')
        .split('\n')
        .forEach((line, i) => {
          if (RENAMED_UPSTREAM_URL.test(line)) {
            hits.push(`${relative(SRC, f)}:${i + 1}`);
          }
        });
    }
    expect(hits).toEqual([]);
  });
});
