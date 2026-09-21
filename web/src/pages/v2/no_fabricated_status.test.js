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
 * Cycle 11 (L4) — this is NOT a general oracle for "every UI claim has a
 * backend". It catches exactly two shapes that were both found on HEAD:
 *
 *   (a) fabricated console state — literal region names (us-west,
 *       eu-frankfurt, ap-shanghai) and data-residency copy that Settings
 *       rendered as "available" with zero backend behind them. Reappearing
 *       anywhere under pages/v2 or in a locale file is the same defect
 *       coming back under a different string.
 *
 *   (b) a routed v2 page that neither calls the backend (API.<verb>) nor
 *       imports a hooks/ or models/ module is either a static/illustrative
 *       page (must be named in STATIC_PAGE_ALLOWLIST with a one-line reason)
 *       or it is a new instance of the same defect class as (a) — a page
 *       that LOOKS live but has nothing behind it.
 *
 * Neither rule proves a page's displayed numbers are real, or that every
 * button works — only that the two known fabrication shapes don't return.
 *
 * Scope limits (read before trusting a green run):
 *   - Rule (a) matching is case-sensitive plain-string `.includes()` against
 *     src/pages/v2/**, src/components/**, and the locale JSON files. It does
 *     not catch re-cased strings ("Data Residency"), runtime-assembled
 *     strings ('us' + '-west'), or any other directory (e.g. src/services,
 *     src/helpers); it also skips *.test.* files (see sourceFiles), so
 *     fixtures are not scanned.
 *   - Rule (b) only walks pages reachable from App.jsx's lazy-import map and
 *     slug table / literal path routes; a fabricated panel embedded in an
 *     otherwise-live page (one that already has its own API call) is
 *     invisible to both rules.
 *   - Third blind spot, named by cycle 12 L3 rather than fixed here: a page
 *     that DOES call the backend can still fabricate, by rendering a failed
 *     call as a benign answer. Admin/Gateway drew "no open breakers" and an
 *     empty table on a 502; Admin/Settings drew every auth toggle as off;
 *     Admin/CostIntelligence said the savings endpoint "returned no data".
 *     All three had an API call, so rule (b) was satisfied, and none of the
 *     strings were fabricated, so rule (a) was too. The oracle for that
 *     shape is a per-page failure-injection test; a structural rule would
 *     have to know which render branch a null payload reaches, which this
 *     file deliberately does not attempt.
 *
 *     Coverage of that oracle, as of cycle 12's repair round: FIVE pages
 *     have it — Admin/Gateway, Admin/CostIntelligence, Admin/Settings,
 *     Admin/ModelPerformance and Admin/SystemTasks (each page's own
 *     *.test.jsx injects a 502, a bare network reject, a 200 carrying
 *     success:false, a 403 and a 401).
 *
 *     TEN call sites still catch 403 and nothing else, so each still renders
 *     its benign-zero branch on any other failure — and, since L4 split 401
 *     out as its own denial, renders an expired session as an empty page
 *     rather than "sign in again". Enumerated by
 *     `grep -rn "status === 403" src/pages/v2 --include=*.jsx` on
 *     2026-09-19, excluding the five pages above, excluding Analytics/
 *     Rankings.jsx:127 (already has an error state) and Flows/index.jsx:86
 *     (a write-error message helper, not a load path):
 *
 *       Admin/Audit/index.jsx:263          empty feed = "no audit events"
 *       Admin/Audit/index.jsx:307          action taxonomy silently empty
 *       Admin/Diagnostics/index.jsx:75     probe panel blank = "all fine"
 *       Admin/Users/index.jsx:318          empty list = "no users"
 *       Admin/ModelRateLimits/index.jsx:262/279/321  no limits = "unlimited"
 *       Admin/Authz.jsx:79                 no grants = "nobody delegated"
 *       Tenants/index.jsx:505              empty list = "no tenants"
 *       Projects/index.jsx:274             empty list = "no projects"
 *
 *     Those files are outside the lane's ownership this cycle; the list is
 *     also carried in the lane return value as a next-cycle item. Audit is
 *     the compliance-visible one and Diagnostics the incident-visible one.
 *
 *     Cycle 13 (L7) closed Dashboard, Admin/Users, Admin/Audit (both sites)
 *     and Admin/Diagnostics through helpers/loadState.js classifyLoad()
 *     (Diagnostics by dropping a carve-out rather than adding a state), and
 *     L9 made Projects' spend panel tri-state. Re-running the same grep on
 *     2026-09-20 leaves: Admin/ModelRateLimits (3 sites), Admin/Authz,
 *     Tenants, Projects' LIST read (index.jsx:303), plus the five pages that
 *     already had the oracle and Rankings/Flows as before. Still not fixed
 *     here; still a next-cycle item.
 */
import fs from 'node:fs';
import path from 'node:path';
import { describe, it, expect } from 'vitest';

const PAGES_DIR = path.resolve(process.cwd(), 'src/pages/v2');
const COMPONENTS_DIR = path.resolve(process.cwd(), 'src/components');
const LOCALES_DIR = path.resolve(process.cwd(), 'src/i18n/locales');
const APP_JSX = path.resolve(process.cwd(), 'src/App.jsx');

const rel = (p) => path.relative(process.cwd(), p).replace(/\\/g, '/');

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

// ── Rule (a): fabricated status patterns must never appear again ──────────

const FABRICATED_PATTERNS = [
  'us-west',
  'eu-frankfurt',
  'ap-shanghai',
  'data residency',
  'data_residency',
  'region_current',
  'region_available',
  'section_region',
];

describe('no fabricated status surfaces under pages/v2', () => {
  it('scans more than 10 files (fail fast if the scan itself found nothing)', () => {
    const pageFiles = sourceFiles(PAGES_DIR);
    const componentFiles = sourceFiles(COMPONENTS_DIR);
    const localeFiles = fs
      .readdirSync(LOCALES_DIR)
      .filter((f) => f.endsWith('.json'))
      .map((f) => path.join(LOCALES_DIR, f));
    expect(
      pageFiles.length + componentFiles.length + localeFiles.length,
    ).toBeGreaterThan(10);
  });

  it('no page/component under pages/v2 or components, or locale file, names a fabricated region or claims data residency', () => {
    const pageFiles = sourceFiles(PAGES_DIR);
    const componentFiles = sourceFiles(COMPONENTS_DIR);
    const localeFiles = fs
      .readdirSync(LOCALES_DIR)
      .filter((f) => f.endsWith('.json'))
      .map((f) => path.join(LOCALES_DIR, f));
    const files = [...pageFiles, ...componentFiles, ...localeFiles];
    expect(files.length).toBeGreaterThan(10);

    const offenders = [];
    for (const file of files) {
      const src = fs.readFileSync(file, 'utf8');
      for (const pattern of FABRICATED_PATTERNS) {
        if (src.includes(pattern)) {
          offenders.push(`${rel(file)}: "${pattern}"`);
        }
      }
    }

    expect(
      offenders,
      `Banned fabricated-status literals found (see the header comment for ` +
        `what this rule is and is not). If one of these strings has become ` +
        `backed by a real store, remove it from FABRICATED_PATTERNS in the ` +
        `same change that adds the backend:\n  ` +
        offenders.join('\n  '),
    ).toEqual([]);
  });
});

// ── Rule (b): every routed static-looking v2 page must be allowlisted ─────

// Reason each entry is honestly static: no API call, no hooks/models import,
// and (rot guard, checked below) still true today.
const STATIC_PAGE_ALLOWLIST = {
  'design-system': 'component/token gallery — nothing to fetch',
  states:
    'renders a static curl walkthrough plus illustrative empty/loading/error/mobile/modal views, no live data',
  'account-disabled':
    'suspension landing: static copy; its CTAs are a mailto and a full-page ' +
    'navigation to /api/v2/auth/zita-logout; no API.<verb> call and no ' +
    'hooks/models import',
};

function parseImportMap(appSrc) {
  const importRe =
    /const (\w+) = lazy\(\(\) => import\('\.\/pages\/v2\/([^']+)'\)\);/g;
  const map = {};
  let m;
  while ((m = importRe.exec(appSrc))) {
    map[m[1]] = m[2];
  }
  return map;
}

function parseSlugTable(appSrc) {
  // The slug rows carry an optional third element since cycle 12 (a route
  // guard such as RootRoute), so the anchor stops at the second name.
  const mapCallIdx = appSrc.indexOf('].map(([slug, Component');
  if (mapCallIdx === -1) {
    throw new Error(
      'no_fabricated_status: App.jsx no longer contains the ' +
        '].map(([slug, Component marker this parser anchors on ' +
        '— update the anchor, do not delete the test.',
    );
  }
  const arrStart = appSrc.lastIndexOf('{[', mapCallIdx);
  expect(arrStart).toBeGreaterThan(-1);
  const block = appSrc.slice(arrStart, mapCallIdx);
  const tupleRe = /\[\s*'([\w/-]+)'\s*,\s*(\w+)\s*(?:,\s*\w+\s*)?\]/g;
  const routes = [];
  let m;
  while ((m = tupleRe.exec(block))) {
    routes.push({ slug: m[1], varName: m[2] });
  }
  return routes;
}

function parseLiteralPathRoutes(appSrc) {
  // Matches <Route path='/console/v2/<slug>' ... element with <VarName /> a
  // few hundred chars later (the account-disabled route today). Does not
  // match the bare '/console/v2' redirect (no trailing slug) or the slug
  // table, which uses a template literal path, not a single-quoted one.
  const re = /path='\/console\/v2\/([\w-]+)'[\s\S]{0,400}?<(\w+)\s*\/>/g;
  const routes = [];
  let m;
  while ((m = re.exec(appSrc))) {
    routes.push({ slug: m[1], varName: m[2] });
  }
  return routes;
}

// A page "has a consumer" if any of its non-test source files call the
// backend or import a hooks/ or models/ module.
const API_CALL_RE = /API\.(get|post|put|patch|delete)\(/;
const HOOKS_MODELS_IMPORT_RE = /from\s+['"][^'"]*\/(hooks|models)\//;

function pageDirCandidates(dir) {
  const asDir = path.join(PAGES_DIR, dir, 'index.jsx');
  const asFile = path.join(PAGES_DIR, `${dir}.jsx`);
  if (fs.existsSync(asDir)) return [path.join(PAGES_DIR, dir)];
  if (fs.existsSync(asFile)) return [asFile];
  return []; // page directory/file does not exist on disk
}

function pageHasConsumer(dir) {
  const candidates = pageDirCandidates(dir);
  if (candidates.length === 0) return false; // nothing to scan == no consumer
  const target = candidates[0];
  const files = fs.statSync(target).isDirectory()
    ? sourceFiles(target)
    : [target];
  for (const file of files) {
    const src = fs.readFileSync(file, 'utf8');
    if (API_CALL_RE.test(src) || HOOKS_MODELS_IMPORT_RE.test(src)) {
      return true;
    }
  }
  return false;
}

describe('every routed static-looking v2 page is allowlisted', () => {
  it('routed pages with zero API/hooks/models consumers are all named in STATIC_PAGE_ALLOWLIST', () => {
    const appSrc = fs.readFileSync(APP_JSX, 'utf8');
    const importMap = parseImportMap(appSrc);
    expect(Object.keys(importMap).length).toBeGreaterThan(10);

    const routes = [
      ...parseSlugTable(appSrc),
      ...parseLiteralPathRoutes(appSrc),
    ];
    expect(routes.length).toBeGreaterThan(10);

    const unlisted = [];
    for (const { slug, varName } of routes) {
      const dir = importMap[varName];
      if (!dir) continue; // component not lazily imported from pages/v2 — not our concern
      if (pageHasConsumer(dir)) continue;
      if (!Object.prototype.hasOwnProperty.call(STATIC_PAGE_ALLOWLIST, slug)) {
        unlisted.push(`${slug} (${dir})`);
      }
    }

    expect(
      unlisted,
      `Routed pages with no API call and no hooks/models import, not in ` +
        `STATIC_PAGE_ALLOWLIST — either they are static (add a one-line ` +
        `reason) or they are fabricated status that needs a real backend; ` +
        `a slug whose page directory no longer exists lands here too ` +
        `(delete the route):\n  ` +
        unlisted.join('\n  '),
    ).toEqual([]);
  });

  it('rot guard: every allowlisted page is still routed and still has zero consumers', () => {
    const appSrc = fs.readFileSync(APP_JSX, 'utf8');
    const importMap = parseImportMap(appSrc);
    const routes = [
      ...parseSlugTable(appSrc),
      ...parseLiteralPathRoutes(appSrc),
    ];
    const routedSlugs = new Set(routes.map((r) => r.slug));

    const stale = [];
    for (const slug of Object.keys(STATIC_PAGE_ALLOWLIST)) {
      if (!routedSlugs.has(slug)) {
        stale.push(`${slug}: allowlisted but no longer routed`);
        continue;
      }
      const route = routes.find((r) => r.slug === slug);
      const dir = importMap[route.varName];
      if (dir && pageHasConsumer(dir)) {
        stale.push(
          `${slug}: allowlisted as static but now has an API/hooks/models consumer`,
        );
      }
    }

    expect(stale).toEqual([]);
  });
});
