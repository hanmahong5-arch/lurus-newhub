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
import { readFileSync, existsSync, statSync } from 'fs';
import { dirname, join, resolve } from 'path';
import { fileURLToPath } from 'url';

// Structural gate on the application's *synchronous* module graph.
//
// The entry chunk is what a first-time visitor downloads before anything is
// painted. `@lobehub/icons` is a ~4 MB vendor-logo pack; render.jsx used to
// pull it in with `import * as LobeIcons`, and helpers/index.js re-exported
// render.jsx, so every module that touched the helpers barrel — including
// App.jsx — dragged the whole pack into the entry chunk. The runtime string
// indexing in getLobeHubIcon ('OpenAI.Color' -> LobeIcons['OpenAI']) also
// defeats tree-shaking, so none of it could be dropped.
//
// This walks the import graph the bundler walks for the entry chunk: static
// `import ... from`, bare side-effect `import 'x'`, and `export ... from`
// re-exports. It deliberately does NOT follow `import()` — a dynamic import
// is exactly the escape hatch that moves a dependency out of the entry chunk,
// so `lazy(() => import('./pages/Home'))` and the memoised
// `import('@lobehub/icons')` inside helpers/lobeIcon.jsx are both out of
// scope by construction.
const FORBIDDEN_IN_ENTRY_GRAPH = ['@lobehub/icons'];

// Package boundary, not string equality. `@lobehub/icons` declares
// main/module = 'es/index.js' and no `exports` map (node_modules/@lobehub/
// icons/package.json, v2.48.0), so `import '@lobehub/icons/es'` — or
// '@lobehub/icons/es/index.js' — is a second spelling of the identical 13 MB
// of vendor logos and would have walked straight past an `includes()` check.
// A package whose NAME merely starts with a forbidden one ('@lobehub/
// icons-extra') is a different package and must not be caught: hence the
// explicit '/' separator rather than a bare startsWith.
export function forbiddenPackageFor(specifier) {
  return FORBIDDEN_IN_ENTRY_GRAPH.find(
    (pkg) => specifier === pkg || specifier.startsWith(`${pkg}/`),
  );
}

const HERE = dirname(fileURLToPath(import.meta.url));
const SRC_ROOT = HERE;
const ENTRY_POINTS = ['index.jsx', 'App.jsx'].map((f) => join(SRC_ROOT, f));

// Extensions this walker can read. A `.css` / `.json` / font import is a leaf:
// it cannot itself import anything, so it is recorded and not opened.
const WALKABLE = ['.js', '.jsx'];

const CANDIDATE_SUFFIXES = [
  '',
  '.js',
  '.jsx',
  '/index.js',
  '/index.jsx',
  '.json',
  '.css',
];

function isFile(p) {
  return existsSync(p) && statSync(p).isFile();
}

function resolveSpecifier(specifier, importerFile) {
  let base;
  if (specifier.startsWith('.')) {
    base = resolve(dirname(importerFile), specifier);
  } else if (specifier.startsWith('@/')) {
    // vite.config.js / vitest.config.js alias: '@' -> web/src
    base = join(SRC_ROOT, specifier.slice(2));
  } else {
    return null; // bare package specifier — not a file in this tree
  }
  for (const suffix of CANDIDATE_SUFFIXES) {
    const candidate = base + suffix;
    if (isFile(candidate)) return candidate;
  }
  return null;
}

// Static specifiers only. Anchoring each pattern at the start of a line keeps
// `//` and `*` comment bodies out (a commented-out import does not ship), and
// `import(` never matches because every pattern requires either a quote or a
// `from` clause right after the import clause.
const STATIC_IMPORT_PATTERNS = [
  /^[ \t]*import\s+[^;]*?\sfrom\s+['"]([^'"]+)['"]/gm,
  /^[ \t]*import\s+['"]([^'"]+)['"]/gm,
  /^[ \t]*export\s+(?:\*|\{[^}]*\})\s*(?:as\s+\w+\s+)?from\s+['"]([^'"]+)['"]/gm,
];

function staticSpecifiersOf(source) {
  const found = new Set();
  for (const pattern of STATIC_IMPORT_PATTERNS) {
    pattern.lastIndex = 0;
    let m;
    while ((m = pattern.exec(source)) !== null) {
      found.add(m[1]);
    }
  }
  return [...found];
}

function walkEntryGraph() {
  const visited = new Set();
  const queue = [...ENTRY_POINTS];
  // importer file -> forbidden specifier it pulled in synchronously
  const offenders = [];

  while (queue.length > 0) {
    const file = queue.pop();
    if (visited.has(file)) continue;
    visited.add(file);
    if (!WALKABLE.some((ext) => file.endsWith(ext))) continue;

    const source = readFileSync(file, 'utf8');
    for (const specifier of staticSpecifiersOf(source)) {
      const forbidden = forbiddenPackageFor(specifier);
      if (forbidden) {
        offenders.push({
          file: file.slice(SRC_ROOT.length + 1),
          specifier,
          package: forbidden,
        });
        continue;
      }
      const resolved = resolveSpecifier(specifier, file);
      if (resolved && !visited.has(resolved)) queue.push(resolved);
    }
  }

  return { visited, offenders };
}

describe('entry chunk module graph', () => {
  const { visited, offenders } = walkEntryGraph();

  it('walks a real graph rather than collapsing to the entry files', () => {
    // Fail fast instead of passing vacuously: if resolution broke (an alias
    // change, a rename), an empty walk would make every assertion below true.
    for (const entry of ENTRY_POINTS) {
      expect(isFile(entry), `${entry} must exist`).toBe(true);
    }
    // 170 files before cycle 12; 67 after W made the eleven legacy pages
    // lazy (measured 2026-09-19). The floor only guards against an empty
    // walk, so it sits well under the real size.
    expect(visited.size).toBeGreaterThan(40);
  });

  it.each(FORBIDDEN_IN_ENTRY_GRAPH)(
    'does not reach %s through a static import, under any spelling',
    (pkg) => {
      const hits = offenders
        .filter((o) => o.package === pkg)
        .map((o) => `${o.file} (${o.specifier})`);
      expect(
        hits,
        `${pkg} is statically reachable from the entry, so it lands in the entry chunk. ` +
          `Import it with import() from a module the entry does not statically reach ` +
          `(see src/helpers/lobeIcon.jsx). Offending files: ${hits.join(', ')}`,
      ).toEqual([]);
    },
  );
});

// The gate above is a negative assertion, which is only worth what its matcher
// is worth: while it compared specifiers for equality, `import '@lobehub/icons/
// es'` put the very same 13 MB back into the entry chunk with the gate still
// green.
describe('forbidden-package matching', () => {
  it.each([
    ['@lobehub/icons', true, 'the package itself'],
    [
      '@lobehub/icons/es',
      true,
      "main/module is 'es/index.js' and there is no exports map, so this subpath IS the whole pack",
    ],
    ['@lobehub/icons/es/index.js', true, 'a deep path into the same package'],
    [
      '@lobehub/icons-extra',
      false,
      'a different package that merely shares a name prefix',
    ],
    ['@lobehub/iconsx', false, 'likewise'],
    ['@lobehub/ui', false, 'a different package in the same scope'],
    ['lucide-react', false, 'the icon set that is meant to be in the entry'],
  ])('%s -> forbidden: %s (%s)', (specifier, expected) => {
    expect(Boolean(forbiddenPackageFor(specifier))).toBe(expected);
  });
});
