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

/*
 * Structural gate: a component/hook directory nobody outside it imports is
 * unreachable code, and unreachable console code is the most expensive kind —
 * it reads like a feature, it shows up in every grep, and it ages against a
 * backend that has moved on. Cycle 12 deleted ~30k lines of exactly that
 * (components/table/{channels,tokens,usage-logs,models} plus the three
 * hooks/ directories those four were the sole importers of); this gate keeps
 * the next batch from accumulating unnoticed.
 *
 * "Importer" here means a NON-TEST source file that resolves an import to
 * something inside the directory subtree, and that itself lives outside that
 * subtree. Tests are excluded on purpose: a directory whose remaining callers
 * are its own tests is precisely the shape being caught — 27 test files kept
 * the four table directories looking alive for months.
 *
 * SCOPE LIMIT, stated so a green run is not read as more than it is: the unit
 * is the DIRECTORY. A single file with no importers inside a directory that
 * is otherwise alive is invisible here — components/settings is the standing
 * example: measured on 2026-09-19 by the same import-graph walk this file
 * does, 7 of its 22 source files have no non-test importer anywhere
 * (AIFeaturesSettingPage, DashboardSetting, ModelConfigSettingPage,
 * OperationSetting, OtherSetting, RatioSetting, SystemSetting), while the
 * directory as a whole is imported all over. That shape is deliberately out
 * of scope this cycle: the plan's §8 O-ratio records that RatioSetting.jsx is
 * the only model/group ratio editor in the tree, so "orphaned" there means
 * "unreachable feature an operator still needs", not "delete it". A per-file
 * variant needs that owner decision first, and is recorded as a next-cycle
 * item rather than half-built here.
 */

const HERE = path.dirname(fileURLToPath(import.meta.url));
const SRC = HERE;
const SRC_EXT = ['.js', '.jsx'];

const isTestName = (name) => /\.(test|spec)\.(js|jsx)$/.test(name);

/*
 * Directories that are orphaned today and are not this lane to delete. Each
 * entry carries why it is still here; an entry whose directory has gained an
 * importer, or has been deleted, fails the last test below, so the list
 * cannot rot into a silent mute.
 */
const KNOWN_ORPHANS = {
  'components/common/examples':
    'ChannelKeyViewExample.jsx — a worked example of the channel-key reveal ' +
    'flow, never mounted. Keep-or-delete is an owner call (cycle 12 L3).',
  'components/common/logo':
    'LinuxDoIcon / OIDCIcon / WeChatIcon — third-party sign-in glyphs with no ' +
    'remaining call site. Owner call (cycle 12 L3).',
  'hooks/playground':
    'usePlaygroundState and siblings — components/playground carries its own ' +
    'state today and imports none of these. Owner call (cycle 12 L3).',
};

function walkFiles(dir, out = []) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) walkFiles(full, out);
    else if (SRC_EXT.includes(path.extname(entry.name))) out.push(full);
  }
  return out;
}

// Covers the four shapes this tree actually uses: static `from '…'` (import
// and re-export), dynamic `import('…')`, CommonJS `require('…')` (the
// vi.mock factories use it), and side-effect `import '…'`.
const SPEC_RE = new RegExp(
  [
    String.raw`(?:import|export)\s[^;]*?from\s*['"]([^'"]+)['"]`,
    String.raw`import\s*\(\s*['"]([^'"]+)['"]\s*\)`,
    String.raw`require\s*\(\s*['"]([^'"]+)['"]\s*\)`,
    String.raw`import\s*['"]([^'"]+)['"]`,
  ].join('|'),
  'g',
);

function importSpecs(file) {
  const text = fs.readFileSync(file, 'utf8');
  const out = [];
  SPEC_RE.lastIndex = 0;
  let m;
  while ((m = SPEC_RE.exec(text)) !== null) {
    out.push(m[1] || m[2] || m[3] || m[4]);
  }
  return out;
}

// '@' is aliased to src/ in both vite.config.js and vitest.config.js.
function resolveSpec(fromFile, spec) {
  let base;
  if (spec.startsWith('@/')) base = path.join(SRC, spec.slice(2));
  else if (spec.startsWith('.'))
    base = path.resolve(path.dirname(fromFile), spec);
  else return null;

  const candidates = [
    base,
    base + '.js',
    base + '.jsx',
    path.join(base, 'index.js'),
    path.join(base, 'index.jsx'),
  ];
  for (const c of candidates) {
    if (fs.existsSync(c) && fs.statSync(c).isFile()) return c;
  }
  // Unresolvable (a .css, an asset, a deleted module): the raw path still
  // says which directory it was aimed at, which is all this gate needs.
  return base;
}

function candidateDirs(root) {
  const out = [];
  const rec = (dir) => {
    const entries = fs.readdirSync(dir, { withFileTypes: true });
    const hasSource = entries.some(
      (e) =>
        e.isFile() &&
        SRC_EXT.includes(path.extname(e.name)) &&
        !isTestName(e.name),
    );
    if (hasSource) out.push(dir);
    for (const e of entries) if (e.isDirectory()) rec(path.join(dir, e.name));
  };
  rec(root);
  return out;
}

const rel = (abs) => path.relative(SRC, abs).split(path.sep).join('/');

const sourceFiles = walkFiles(SRC).filter(
  (f) =>
    !isTestName(path.basename(f)) &&
    !f.startsWith(path.join(SRC, 'test') + path.sep),
);

const edges = [];
for (const file of sourceFiles) {
  for (const spec of importSpecs(file)) {
    const target = resolveSpec(file, spec);
    if (target && target.startsWith(SRC)) edges.push([file, target]);
  }
}

const dirs = ['components', 'hooks'].flatMap((d) =>
  candidateDirs(path.join(SRC, d)),
);

const orphans = dirs
  .filter((dir) => {
    const prefix = dir + path.sep;
    return !edges.some(
      ([from, to]) => to.startsWith(prefix) && !from.startsWith(prefix),
    );
  })
  .map(rel);

describe('no orphan component/hook directories', () => {
  it('scans a non-empty set of directories (fail fast if the walk broke)', () => {
    expect(dirs.length).toBeGreaterThan(20);
    expect(sourceFiles.length).toBeGreaterThan(200);
    expect(edges.length).toBeGreaterThan(200);
  });

  it('every component/hook directory has a non-test importer outside itself', () => {
    const unexpected = orphans.filter((d) => !(d in KNOWN_ORPHANS));
    expect(
      unexpected,
      'These directories under src/components or src/hooks have no non-test ' +
        'importer outside their own subtree, so nothing in the shipped app ' +
        'can reach them. Delete them, wire them up, or — when the call sits ' +
        'with the owner — add them to KNOWN_ORPHANS with the reason:\n  ' +
        unexpected.join('\n  '),
    ).toEqual([]);
  });

  it('every KNOWN_ORPHANS entry still names a directory that is still orphaned', () => {
    const stale = Object.keys(KNOWN_ORPHANS).filter(
      (d) => !orphans.includes(d),
    );
    expect(
      stale,
      'These KNOWN_ORPHANS entries are no longer orphaned, or no longer ' +
        'exist. Drop the entry so the exemption list cannot outlive its ' +
        'reason:\n  ' +
        stale.join('\n  '),
    ).toEqual([]);
  });
});
