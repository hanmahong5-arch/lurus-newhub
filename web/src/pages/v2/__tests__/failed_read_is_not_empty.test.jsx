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
 * cycle-14 L8 — "a failed read must not render as an empty successful read".
 *
 * WHAT THIS GATE DOES
 * Every routed page under src/pages/v2 is MOUNTED TWICE against a mocked
 * API layer:
 *
 *   EMPTY  — every verb resolves 200 {success:true, data:<empty>}
 *   FAILED — every verb rejects with a 500 carrying {success:false}
 *
 * and the two renders' visible text must differ. Nothing here reads source
 * code to decide that (a source scan was the first idea and it proves
 * nothing about what renders: `catch` blocks that look identical render
 * differently, and blocks that look different can both fall through to the
 * same empty state). The component runs.
 *
 * WHY IT EXISTS
 * The class it locks: fetchLimits / fetchAll / fetchTenants each set a flag
 * on 403 and then emptied their rows for EVERY other outcome, so a 500, a
 * timeout, a dropped connection or a 200 carrying success:false rendered
 * character-for-character the same page as a genuinely empty tenant — "No
 * grants issued yet.", "No tenants yet.", "0 projects", and on the tenants
 * page a money figure, "$0.00 used". Four instances of one class, each next
 * to a sibling read on the SAME page that already had an honest error state.
 *
 * THREE ALLOW-LISTS, EACH WITH A ROT GUARD
 *   NO_READ_ON_MOUNT — the page reads nothing when it mounts, so there is no
 *     failed read to mis-render. Re-checked at runtime: the mocked API must
 *     still record zero GETs during the mount.
 *   HARNESS_LIMITS — this generic driver cannot render the page. Re-checked:
 *     it must still fail to render, so making it drivable forces the entry
 *     out.
 *   KNOWN_UNFIXED — the page IS in the class and is not repaired. Its
 *     assertion is INVERTED (it must still render identically), so the entry
 *     has to be deleted the day someone fixes the page. The list can only
 *     shrink; a page cannot join it without being measured in, and a NEW
 *     page in the class is caught by the main rule, not by this list.
 *
 * BLIND SPOTS — read these before trusting a green run
 *  1. It proves the two states are DISTINGUISHABLE, not that the failure
 *     state is correct, legible, or actionable. A page that renders the
 *     string "x" on failure passes. Copy quality is a review question.
 *  2. It compares whole-page text. A page with several independent reads
 *     passes as soon as ONE of them reacts — the four-panel page whose
 *     money panel still fabricates a zero is invisible here as long as some
 *     other panel says "retry". Per-panel coverage is each page's own
 *     *.test.jsx (this file's sibling oracles do exactly that for the four
 *     pages cycle-14 L8 repaired).
 *  3. Only two outcomes are injected (empty-success, 500). A page that
 *     distinguishes a 500 but still collapses 401 / 403 / a 200 carrying
 *     success:false is not caught. classifyLoad() in helpers/loadState.js is
 *     where those are separated, and only the per-page tests exercise them.
 *  4. It drives pages with a generic empty payload. Pages whose render path
 *     needs a specific shape land in HARNESS_LIMITS below rather than being
 *     silently skipped — with a rot guard that fails when the reason stops
 *     being true.
 *  5. Scope is what App.jsx routes. A panel or drawer mounted inside a page
 *     (Tenants' CreditPoolDrawer, InvitesDrawer) is never opened here.
 *  6. jsdom, mocked HFShell, mocked router: nothing about real navigation,
 *     real styling, or the shell's own reads is exercised.
 */
import fs from 'node:fs';
import path from 'node:path';
import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act, cleanup, render } from '@testing-library/react';

// ── the mocked module boundary ───────────────────────────────────────────────
// The pages under test import the helpers barrel as '../../../helpers' or
// '../../../../helpers'; this file sits at the same depth as a page
// directory, so '../../../helpers' resolves to the same module id and one
// mock covers all of them.

// Semi UI's import chain pulls in lottie, which throws on jsdom's
// stub-less getContext(). vi.hoisted runs before the page imports below,
// early enough for lottie's top-level code to find a context. Same stub as
// Admin/Users/index.test.jsx: this stubs the CANVAS, not the components, so
// the real ConfirmDialog/Modal still render.
vi.hoisted(() => {
  const ctx = new Proxy(
    {},
    {
      get: (target, prop) =>
        prop in target ? target[prop] : () => ({ data: [] }),
      set: (target, prop, value) => ((target[prop] = value), true),
    },
  );
  HTMLCanvasElement.prototype.getContext = () => ctx;
});

const apiMock = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  patch: vi.fn(),
  delete: vi.fn(),
}));

vi.mock('../../../helpers', () => ({
  API: apiMock,
  showError: vi.fn(),
  showSuccess: vi.fn(),
  // Role helpers: a routed page may gate part of its body on these. Both
  // scenarios get the same answer, so a difference can never come from here.
  isAdmin: () => true,
  isRoot: () => true,
  getServerAddress: () => 'http://localhost:3000',
}));

// Mirror i18next's en behaviour: return the English defaultValue with
// {{var}} interpolation. Without it a count assertion would compare
// "{{count}}" against itself and every page would look identical.
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key, fallback, opts) => {
      const vars =
        typeof fallback === 'object' && fallback !== null ? fallback : opts;
      let out = typeof fallback === 'string' ? fallback : key;
      if (vars) {
        for (const [k, v] of Object.entries(vars)) {
          out = out.split(`{{${k}}}`).join(String(v));
        }
      }
      return out;
    },
    i18n: { language: 'en', changeLanguage: () => {} },
  }),
  Trans: ({ children }) => children,
  initReactI18next: { type: '3rdParty', init: () => {} },
}));

// Only the shell CHROME is replaced. Its named exports (NAV_SECTIONS,
// visibleNavItems, navItemEnabledByFeatureFlags, useBridgedUser — the
// CommandPalette page reads all four) stay real, so a page's own body is
// what differs between the two scenarios, not a stubbed nav model.
vi.mock('../../../components/hifi/HFShell', async (importOriginal) => {
  const actual = await importOriginal();
  return {
    ...actual,
    default: ({ children, actions }) =>
      React.createElement(
        'div',
        { 'data-testid': 'hf-shell' },
        React.createElement('div', { 'data-testid': 'hf-actions' }, actions),
        children,
      ),
  };
});

// VChart in jsdom needs a canvas; the trend chart is presentation, not a
// read outcome.
vi.mock('../../../components/hifi/HfUsageTrendChart', () => ({
  default: () => React.createElement('div', { 'data-testid': 'trend-chart' }),
}));

vi.mock('react-router-dom', () => ({
  useNavigate: () => () => {},
  useLocation: () => ({ pathname: '/console/v2', search: '', hash: '' }),
  useParams: () => ({}),
  useSearchParams: () => [new URLSearchParams(), () => {}],
  Link: ({ children, ...rest }) => React.createElement('a', rest, children),
  NavLink: ({ children, ...rest }) => React.createElement('a', rest, children),
}));

// ── the two scenarios ────────────────────────────────────────────────────────

// An empty payload that satisfies both `data.items`-style and `data.map`-style
// readers: an Array (so .map/.length work) carrying the empty collection keys
// this console's handlers use. A page whose render path needs a richer shape
// lands in HARNESS_LIMITS, it is never silently skipped.
const COLLECTION_KEYS = [
  'items',
  'tenants',
  'users',
  'records',
  'data',
  'logs',
  'rows',
  'channels',
  'tokens',
  'models',
  'projects',
  'grants',
  'resources',
  'invoices',
  'tasks',
  'routes',
  'entries',
  'list',
  'results',
  'keys',
];

const emptyPayload = () => {
  const arr = [];
  for (const k of COLLECTION_KEYS) arr[k] = [];
  return arr;
};

const emptyOk = () => ({
  status: 200,
  data: { success: true, message: '', data: emptyPayload(), total: 0 },
});

const injectedFailure = () => {
  const err = new Error('gate: injected 500');
  err.isAxiosError = true;
  err.response = {
    status: 500,
    data: { success: false, message: 'gate: injected 500' },
  };
  return err;
};

const wireEmpty = () => {
  for (const fn of Object.values(apiMock)) {
    fn.mockReset();
    fn.mockImplementation(() => Promise.resolve(emptyOk()));
  }
};

const wireFailure = () => {
  for (const fn of Object.values(apiMock)) {
    fn.mockReset();
    fn.mockImplementation(() => Promise.reject(injectedFailure()));
  }
};

const normalise = (text) => text.replace(/\s+/g, ' ').trim();

// Let mount effects, their awaits and any follow-up setState settle, then
// keep turning until the rendered text stops changing.
//
// A fixed number of turns was the first version and it FLAKED: under a
// loaded full-suite run a page's post-fetch render was still in flight after
// three turns, both scenarios read back the same pre-answer text, and the
// gate reported a defect that was not there. Two changes make that
// impossible rather than unlikely: settle on OBSERVED STABILITY (three
// consecutive identical readings), and report whether stability was ever
// reached, so a page that never stops moving is called out as unmeasurable
// instead of being compared mid-flight.
const settleUntilStable = async (maxTurns = 60, stableTurns = 3) => {
  let last = null;
  let stable = 0;
  for (let i = 0; i < maxTurns; i += 1) {
    // eslint-disable-next-line no-await-in-loop
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0));
    });
    const now = normalise(document.body.textContent || '');
    stable = now === last ? stable + 1 : 0;
    last = now;
    if (stable >= stableTurns) return { text: now, stable: true };
  }
  return { text: last ?? '', stable: false };
};

const renderUnder = async (loadModule, wire) => {
  wire();
  const mod = await loadModule();
  const Page = mod.default;
  let text = '';
  let stable = false;
  let error = null;
  try {
    await act(async () => {
      render(React.createElement(Page));
    });
    ({ text, stable } = await settleUntilStable());
  } catch (e) {
    error = e;
  } finally {
    cleanup();
  }
  return { text, stable, error, reads: apiMock.get.mock.calls.length };
};

// ── scope: what App.jsx routes ───────────────────────────────────────────────
// slug → loader. Static import specifiers (not a computed path) so vite
// bundles them; the slug is the same one App.jsx's route table uses, and the
// first test below fails when the route table grows an entry this map does
// not have.

const PAGES = {
  dashboard: () => import('../Dashboard/index.jsx'),
  log: () => import('../Log/index.jsx'),
  tasks: () => import('../Tasks/index.jsx'),
  channel: () => import('../Channel/index.jsx'),
  token: () => import('../Token/index.jsx'),
  playground: () => import('../Playground/index.jsx'),
  cmdk: () => import('../CommandPalette/index.jsx'),
  models: () => import('../Models/index.jsx'),
  chat: () => import('../Chat/index.jsx'),
  tenants: () => import('../Tenants/index.jsx'),
  pricing: () => import('../Pricing/index.jsx'),
  redemption: () => import('../Redemption/index.jsx'),
  projects: () => import('../Projects/index.jsx'),
  billing: () => import('../Billing/index.jsx'),
  settings: () => import('../Settings/index.jsx'),
  flows: () => import('../Flows/index.jsx'),
  'design-system': () => import('../DesignSystem/index.jsx'),
  states: () => import('../States/index.jsx'),
  'account-disabled': () => import('../AccountDisabled/index.jsx'),
  'admin/users': () => import('../Admin/Users/index.jsx'),
  'admin/audit': () => import('../Admin/Audit/index.jsx'),
  'admin/gateway': () => import('../Admin/Gateway/index.jsx'),
  'admin/settings': () => import('../Admin/Settings/index.jsx'),
  'admin/cost-intelligence': () =>
    import('../Admin/CostIntelligence/index.jsx'),
  'admin/model-performance': () =>
    import('../Admin/ModelPerformance/index.jsx'),
  'admin/rankings': () => import('../Analytics/Rankings.jsx'),
  'admin/model-limits': () => import('../Admin/ModelRateLimits/index.jsx'),
  'admin/system-tasks': () => import('../Admin/SystemTasks.jsx'),
  'admin/authz': () => import('../Admin/Authz.jsx'),
  'admin/diagnostics': () => import('../Admin/Diagnostics/index.jsx'),
};

// Routed pages that issue NO read when they mount: there is no failed read
// for them to mis-render. The rot guard below re-checks this at RUNTIME (the
// mocked API must record zero calls during the mount), not by reading source
// — a page whose first read arrives in a later cycle falls out of the
// allow-list the moment it does.
const NO_READ_ON_MOUNT = {
  'design-system':
    'component/token gallery — renders fixtures, fetches nothing',
  states:
    'illustrative empty/loading/error/mobile views plus a static curl walkthrough — no live data',
  'account-disabled':
    'suspension landing: static copy, a mailto and a full-page navigation to the logout endpoint',
  flows:
    'create-channel/create-token wizard: every call is a POST/PUT the operator ' +
    'triggers (index.jsx:1064/1163/1233/1267) plus one GET inside a step action ' +
    '(:1182). Nothing is read on mount, so there is no load state to get wrong.',
};

// Routed pages this generic harness cannot drive. NOT a licence to skip: each
// entry is re-checked below and the gate fails when the stated reason stops
// being true, so making a page drivable forces the entry out.
//   'throws-empty'  — the page throws under the EMPTY scenario, i.e. its
//                     render needs a payload shape the generic empty one does
//                     not supply. Fixing the harness for it is a per-page job.
// EMPTY as of cycle-14 L8: all 30 routed pages render under this harness.
const HARNESS_LIMITS = {};

// ── the debt ratchet ─────────────────────────────────────────────────────────
// Pages that ARE in the class and are NOT fixed. Every entry here was
// measured by this gate, not asserted from reading code: the quoted text is
// what the page renders on a 500, which is character-for-character what it
// renders for an empty account.
//
// The assertion for these is INVERTED — they must still render identically.
// So the gate fails in both directions: a new page that joins the class is
// caught by the main rule, and a page that leaves it (someone gave it an
// error state) fails here until its entry is deleted. A list that can only
// be shortened is the point; nothing may be added to it without the same
// measurement.
//
// Some of these pages DO raise a red toast (their own showError, or the API
// interceptor's) — the harness mocks those away deliberately. A toast is
// transient and the panel is not: once it fades, the page is still stating
// the zero as a finding, which is the defect. So "in class" here means the
// RENDERED PAGE is indistinguishable, not that the failure was invisible.
//
// None of these files are in cycle-14 L8's ownership. They are carried in
// the lane's hand-off notes with the exact call sites.
const KNOWN_UNFIXED = {
  tasks:
    'Tasks/index.jsx — a 500 renders "No tasks found." over "0–0 of 0", the ' +
    'same page an account with no async jobs sees.',
  channel:
    'Channel/index.jsx — a 500 renders "0 upstream channels", "healthy 0 / ' +
    'disabled 0 / error 0" and "No channels yet. Add one to get started.": a ' +
    'fleet-health claim made from a read that never answered.',
  token:
    'Token/index.jsx — a 500 renders "No tokens yet. Create one to get ' +
    'started.", which invites a customer to create a second token they may ' +
    'already have.',
  cmdk:
    'CommandPalette/index.jsx — the palette lists its static nav entries and ' +
    'silently drops the model/token results whose read failed, so the result ' +
    'count is identical to a tenant with nothing to find.',
  models:
    'Models/index.jsx — a 500 renders "catalog 0 models" and "no models yet", ' +
    'i.e. "this tenant can route nothing", from an unread catalog.',
  pricing:
    'Pricing/index.jsx — a 500 renders the price table as "no data"; prices ' +
    'are the numbers a customer is billed on.',
  redemption:
    'Redemption/index.jsx — a 500 renders "No redemption codes yet. Click ' +
    '\\"+ new code\\" to generate.", which invites re-issuing codes that may ' +
    'already exist.',
};

const rel = (p) => path.relative(process.cwd(), p).replace(/\\/g, '/');
const PAGES_DIR = path.resolve(process.cwd(), 'src/pages/v2');
const APP_JSX = path.resolve(process.cwd(), 'src/App.jsx');

function sourceFiles(dir, out = []) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, entry.name);
    if (entry.isDirectory()) sourceFiles(p, out);
    else if (/\.jsx?$/.test(entry.name) && !/\.test\./.test(entry.name))
      out.push(p);
  }
  return out;
}

// Where each slug's module lives, relative to src/pages/v2. The driver needs
// the LOADER (it runs the module); this map is what lets a test assert the
// file is actually there. Vite rewrites dynamic-import specifiers, so the
// path cannot be recovered from the loader's source at runtime — hence two
// maps, and a test below that they describe the same set of pages.
const PAGE_SOURCE = {
  dashboard: 'Dashboard/index.jsx',
  log: 'Log/index.jsx',
  tasks: 'Tasks/index.jsx',
  channel: 'Channel/index.jsx',
  token: 'Token/index.jsx',
  playground: 'Playground/index.jsx',
  cmdk: 'CommandPalette/index.jsx',
  models: 'Models/index.jsx',
  chat: 'Chat/index.jsx',
  tenants: 'Tenants/index.jsx',
  pricing: 'Pricing/index.jsx',
  redemption: 'Redemption/index.jsx',
  projects: 'Projects/index.jsx',
  billing: 'Billing/index.jsx',
  settings: 'Settings/index.jsx',
  flows: 'Flows/index.jsx',
  'design-system': 'DesignSystem/index.jsx',
  states: 'States/index.jsx',
  'account-disabled': 'AccountDisabled/index.jsx',
  'admin/users': 'Admin/Users/index.jsx',
  'admin/audit': 'Admin/Audit/index.jsx',
  'admin/gateway': 'Admin/Gateway/index.jsx',
  'admin/settings': 'Admin/Settings/index.jsx',
  'admin/cost-intelligence': 'Admin/CostIntelligence/index.jsx',
  'admin/model-performance': 'Admin/ModelPerformance/index.jsx',
  'admin/rankings': 'Analytics/Rankings.jsx',
  'admin/model-limits': 'Admin/ModelRateLimits/index.jsx',
  'admin/system-tasks': 'Admin/SystemTasks.jsx',
  'admin/authz': 'Admin/Authz.jsx',
  'admin/diagnostics': 'Admin/Diagnostics/index.jsx',
};

beforeEach(() => {
  try {
    localStorage.setItem('tenant_slug', 'acme');
  } catch (_) {
    /* private mode */
  }
});

afterEach(() => {
  cleanup();
});

describe('scope: the driver covers every routed v2 page', () => {
  it('every slug App.jsx routes has a loader in PAGES', () => {
    const appSrc = fs.readFileSync(APP_JSX, 'utf8');
    const mapCallIdx = appSrc.indexOf('].map(([slug, Component');
    expect(
      mapCallIdx,
      'App.jsx no longer contains the "].map(([slug, Component" marker this ' +
        'parser anchors on — update the anchor, do not delete the test.',
    ).toBeGreaterThan(-1);
    const arrStart = appSrc.lastIndexOf('{[', mapCallIdx);
    const block = appSrc.slice(arrStart, mapCallIdx);
    const tupleRe = /\[\s*'([\w/-]+)'\s*,\s*(\w+)\s*(?:,\s*\w+\s*)?\]/g;
    const routedSlugs = [];
    let m;
    while ((m = tupleRe.exec(block))) routedSlugs.push(m[1]);
    // Literal-path routes (account-disabled today) live outside the table.
    const literalRe = /path='\/console\/v2\/([\w-]+)'/g;
    while ((m = literalRe.exec(appSrc))) routedSlugs.push(m[1]);

    expect(
      routedSlugs.length,
      'the route-table parse found suspiciously few slugs — it is broken, ' +
        'and a broken parse would let this whole gate pass vacuously',
    ).toBeGreaterThan(20);

    const missing = routedSlugs.filter(
      (s) => !Object.prototype.hasOwnProperty.call(PAGES, s),
    );
    expect(
      missing,
      `App.jsx routes these v2 slugs and this gate never drives them. Add a ` +
        `loader to PAGES and PAGE_SOURCE (and, if the page reads nothing on ` +
        `mount, an entry in NO_READ_ON_MOUNT):\n  ` +
        missing.join('\n  '),
    ).toEqual([]);
  });

  it('PAGES and PAGE_SOURCE describe the same pages, and every file exists', () => {
    expect(Object.keys(PAGE_SOURCE).sort()).toEqual(Object.keys(PAGES).sort());
    const missing = Object.keys(PAGE_SOURCE).filter(
      (slug) => !fs.existsSync(path.resolve(PAGES_DIR, PAGE_SOURCE[slug])),
    );
    expect(
      missing,
      `PAGE_SOURCE points at files that do not exist:\n  ` +
        missing.join('\n  '),
    ).toEqual([]);
  });

  // The helpers barrel is mocked with a fixed export list. A page importing a
  // seventh name from it would get undefined and crash for a reason that has
  // nothing to do with the class this gate is about.
  it('the helpers mock still covers every name pages/v2 imports from the barrel', () => {
    const MOCKED = [
      'API',
      'showError',
      'showSuccess',
      'isAdmin',
      'isRoot',
      'getServerAddress',
    ];
    const files = sourceFiles(PAGES_DIR);
    expect(files.length).toBeGreaterThan(20);
    const unmocked = new Set();
    for (const file of files) {
      const src = fs.readFileSync(file, 'utf8');
      const re = /import\s*\{([^}]*)\}\s*from\s*'[^']*\/helpers'/g;
      let m;
      while ((m = re.exec(src))) {
        for (const raw of m[1].split(',')) {
          const name = raw
            .trim()
            .split(/\s+as\s+/)[0]
            .trim();
          if (name && !MOCKED.includes(name))
            unmocked.add(`${rel(file)}: ${name}`);
        }
      }
    }
    expect(
      [...unmocked],
      `These names are imported from the helpers barrel by a page under ` +
        `src/pages/v2 but are not in this file's mock, so the page would ` +
        `receive undefined. Add them to the vi.mock factory above:\n  ` +
        [...unmocked].join('\n  '),
    ).toEqual([]);
  });
});

// Each driver case mounts TWO full pages and waits for each to stop
// re-rendering. Vitest's 5s default is a comfortable fit on an idle machine
// and not on a loaded one: in a full-suite run (167 files in parallel) the
// Chat page's pair took past 5s and the case died of a timeout rather than
// of anything it asserts. The window is patience, not an assertion — no
// case here passes because it waited longer.
const DRIVE_TIMEOUT_MS = 60000;

const allowListed = (slug) =>
  [NO_READ_ON_MOUNT, HARNESS_LIMITS, KNOWN_UNFIXED].some((list) =>
    Object.prototype.hasOwnProperty.call(list, slug),
  );

describe('a failed read never renders as an empty successful read', () => {
  const inScope = Object.keys(PAGES).filter((slug) => !allowListed(slug));

  it('drives enough pages to be worth trusting', () => {
    expect(
      inScope.length,
      'too few pages in scope — either the allow-lists have swallowed the ' +
        'gate or the route map is broken',
    ).toBeGreaterThanOrEqual(15);
  });

  for (const slug of inScope) {
    it(
      `${slug}: the 500 render differs from the empty-success render`,
      async () => {
        const load = PAGES[slug];
        const ok = await renderUnder(load, wireEmpty);
        expect(
          ok.error,
          `${slug} threw while rendering the EMPTY-SUCCESS scenario, so this ` +
            `gate could not compare anything. Either give the harness what the ` +
            `page needs, or add it to HARNESS_LIMITS with 'throws-empty' and a ` +
            `reason.\n${ok.error?.stack ?? ''}`,
        ).toBeNull();

        // Without this floor the comparison could pass vacuously: a page that
        // never fetched has no failed read to render honestly, and any
        // difference between the two runs would be noise.
        expect(
          ok.reads,
          `${slug} issued no API.get on mount, so this gate compared two ` +
            `renders of a page that never read anything. If that is correct, ` +
            `move it to NO_READ_ON_MOUNT with a reason.`,
        ).toBeGreaterThan(0);

        const failed = await renderUnder(load, wireFailure);
        expect(
          failed.error,
          `${slug} threw while rendering the FAILED-READ scenario — a page ` +
            `must not crash on a 500.\n${failed.error?.stack ?? ''}`,
        ).toBeNull();

        // Comparing two renders that are still moving would be comparing
        // whichever frame the loop stopped on: the pre-answer text is the
        // same in both scenarios, so it reads as a defect that is not there.
        // Say so plainly instead.
        expect(
          ok.stable && failed.stable,
          `${slug} never stopped re-rendering within the settle window, so ` +
            `this gate has nothing trustworthy to compare (stable: empty=` +
            `${ok.stable} failed=${failed.stable}). Find what keeps it moving ` +
            `or add it to HARNESS_LIMITS — do NOT read this as a defect.`,
        ).toBe(true);

        expect(
          failed.text,
          `${slug} renders a failed read EXACTLY like an empty successful one. ` +
            `Whatever this page says when the account is genuinely empty, it ` +
            `is saying it now about a read that never answered. Give the ` +
            `failure its own state (helpers/loadState.js classifyLoad is the ` +
            `shared vocabulary).\nrendered: "${failed.text.slice(0, 400)}"`,
        ).not.toBe(ok.text);
      },
      DRIVE_TIMEOUT_MS,
    );
  }
});

describe('allow-list rot guards', () => {
  for (const slug of Object.keys(NO_READ_ON_MOUNT)) {
    it(
      `${slug}: still reads nothing on mount`,
      async () => {
        expect(
          Object.prototype.hasOwnProperty.call(PAGES, slug),
          `${slug} is allow-listed as read-free but is no longer in PAGES`,
        ).toBe(true);
        const out = await renderUnder(PAGES[slug], wireEmpty);
        expect(out.error).toBeNull();
        const reads = apiMock.get.mock.calls.map((c) => String(c[0]));
        expect(
          reads,
          `${slug} is allow-listed as making no read on mount, but it called ` +
            `API.get. Remove the entry so the page is driven like the rest:\n  ` +
            reads.join('\n  '),
        ).toEqual([]);
      },
      DRIVE_TIMEOUT_MS,
    );
  }

  // The ratchet, in the direction that matters: when one of these pages is
  // repaired this test goes red and names the entry to delete. It can only
  // ever shrink — adding to it means measuring a NEW page into the class,
  // which is the thing the main rule refuses to let happen quietly.
  for (const slug of Object.keys(KNOWN_UNFIXED)) {
    it(
      `${slug}: still unfixed (delete its KNOWN_UNFIXED entry once it is)`,
      async () => {
        expect(
          Object.prototype.hasOwnProperty.call(PAGES, slug),
          `${slug} is listed as unfixed but is no longer in PAGES`,
        ).toBe(true);
        const ok = await renderUnder(PAGES[slug], wireEmpty);
        const failed = await renderUnder(PAGES[slug], wireFailure);
        expect(ok.error).toBeNull();
        expect(failed.error).toBeNull();
        expect(
          ok.reads,
          `${slug} no longer reads anything on mount — move it to ` +
            `NO_READ_ON_MOUNT instead of leaving it here`,
        ).toBeGreaterThan(0);
        expect(
          failed.text,
          `${slug} no longer renders a failed read like an empty one — it has ` +
            `been repaired. Delete its entry from KNOWN_UNFIXED so the main ` +
            `rule starts holding it.`,
        ).toBe(ok.text);
      },
      DRIVE_TIMEOUT_MS,
    );
  }

  it(
    'every HARNESS_LIMITS entry still cannot be driven',
    async () => {
      const stale = [];
      for (const [slug, reason] of Object.entries(HARNESS_LIMITS)) {
        if (!Object.prototype.hasOwnProperty.call(PAGES, slug)) {
          stale.push(`${slug}: allow-listed but no longer in PAGES`);
          continue;
        }
        // eslint-disable-next-line no-await-in-loop
        const ok = await renderUnder(PAGES[slug], wireEmpty);
        const stillThrows = ok.error !== null;
        if (String(reason).startsWith('throws-empty') && !stillThrows) {
          stale.push(
            `${slug}: allow-listed as 'throws-empty' but it renders cleanly ` +
              `now — remove the entry, the page can be driven`,
          );
        }
      }
      expect(
        stale,
        `A harness allow-list entry stopped being true:\n  ` +
          stale.join('\n  '),
      ).toEqual([]);
    },
    DRIVE_TIMEOUT_MS,
  );
});
