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

// Frontend counterpart to
// internal/adapter/handler/router/frontend_route_contract_test.go, which
// walks web/src for API.<verb>('/api/…') calls and checks each against the
// real Go route table. That file catches a button calling an endpoint the
// server never registered. It has nothing to say about the OTHER kind of
// dead end this cycle is fixing: a sidebar link (or a link the sidebar
// deliberately withholds) whose target is wrong inside the frontend's own
// route table, App.jsx. That direction had no test at all before this file.
//
// Two failure modes, both real bugs that shipped in this codebase's history:
//   1. A nav item's href points at a path App.jsx never registers — a click
//      that 404s.
//   2. A nav item is marked disabled:true (rendered as a greyed, unclickable
//      placeholder with an honest "not available yet" title) while its href
//      points at a route that DOES exist — a working page hidden behind a
//      lie. This is exactly what "MJ / Task logs" was before cycle-10 L6/L7:
//      pages/v2/Tasks existed nowhere, so disabled:true with href:null was
//      accurate; the day the page and its route landed, nothing would have
//      forced anyone to also flip disabled:false — except this test.
//
// NAV_SECTIONS has zero disabled:true items at HEAD (the item above was the
// last one), so mode 2's per-item check below never evaluates a real item —
// it is exercised instead by a fixture block further down, against
// constructed items rather than live nav data, so it still fails a real
// assertion if the predicate itself regresses.
//
// App.jsx is parsed as source text rather than duplicating its route list
// into a second, driftable array here — the same choice the Go-side
// contract test makes for the same reason (collectRoutes() there reads the
// real gin engine, not a hand-maintained mirror).
//
// A raw regex over that source text is blind to comments: `path='/x'` inside
// a `{/* ... */}` or `// ...` comment reads exactly like a live <Route>. That
// is the shape of a real drift bug — commenting out a Route while retiring a
// page, without also touching the rail link that still points at it — so
// stripComments() below runs first and strips both comment forms (preserving
// string contents, so a path literal containing `//`, e.g. none exist today,
// would not be mistaken for a line comment).

import { describe, it, expect, vi } from 'vitest';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

// HFShell.jsx's own module-top imports (API/TenantSwitcher/useFormDraft) pull
// in a Semi UI chain that crashes jsdom (no real <canvas>, used by a lottie
// animation deep in that chain) — the same reason HFShell.test.jsx mocks all
// three before importing the component. NAV_SECTIONS is a plain exported
// array with no runtime dependency on any of the three; only importing the
// module needs them stubbed.
vi.mock('./helpers', () => ({
  API: { get: vi.fn(), post: vi.fn() },
}));
vi.mock('./components/hifi/TenantSwitcher', () => ({
  default: () => null,
}));
vi.mock('./hooks/common/useFormDraft', () => ({
  clearAllDrafts: vi.fn(),
}));

const { NAV_SECTIONS } = await import('./components/hifi/HFShell');

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// Strips `// line` and `/* block */` comments from JS/JSX source while
// leaving string/template literal contents untouched, so a comment
// containing `path='...'` (or a URL containing `//`) cannot be mistaken for
// live code. Not a full JS tokenizer — just enough state (comment vs.
// string vs. plain code) to be correct on App.jsx's own syntax shapes.
function stripComments(src) {
  let out = '';
  let i = 0;
  while (i < src.length) {
    const two = src.slice(i, i + 2);
    if (two === '/*') {
      const end = src.indexOf('*/', i + 2);
      i = end === -1 ? src.length : end + 2;
      continue;
    }
    if (two === '//') {
      const end = src.indexOf('\n', i + 2);
      i = end === -1 ? src.length : end;
      continue;
    }
    const ch = src[i];
    if (ch === "'" || ch === '"' || ch === '`') {
      out += ch;
      i += 1;
      while (i < src.length && src[i] !== ch) {
        if (src[i] === '\\' && i + 1 < src.length) {
          out += src[i] + src[i + 1];
          i += 2;
          continue;
        }
        out += src[i];
        i += 1;
      }
      if (i < src.length) {
        out += src[i];
        i += 1;
      }
      continue;
    }
    out += ch;
    i += 1;
  }
  return out;
}

// Every path a chunk of App.jsx-shaped source registers, as literal strings
// (params like ':id' and the '*' catch-all stay literal too — nav items
// never point at those, so no segment-matching is needed here, unlike the Go
// test which has to match concrete request paths against gin's :param
// syntax). Takes source text directly (rather than reading App.jsx itself)
// so the comment-stripping behaviour below can be pinned against a small
// fixture string, independent of App.jsx's current real content.
function extractRoutesFromSource(rawSrc) {
  const src = stripComments(rawSrc);
  const routes = new Set();

  // Every literal `<Route path='...'>` / `path="..."`, wherever the
  // attribute sits relative to the opening tag.
  for (const m of src.matchAll(/path=(['"])([^'"]+)\1/g)) {
    routes.add(m[2]);
  }

  // App.jsx:373's `[slug, Component].map(...)` table registers the bulk of
  // /console/v2/* as one <Route> per array entry rather than one literal
  // <Route> per page, so those slugs need pulling out separately. Matched by
  // the `V2<PascalCase>` component-name shape every entry in that array uses
  // (see App.jsx's `const V2Foo = lazy(...)` imports above it).
  for (const m of src.matchAll(/\[\s*'([a-zA-Z0-9/_-]+)'\s*,\s*V2\w+\s*\]/g)) {
    routes.add(`/console/v2/${m[1]}`);
  }

  return routes;
}

function collectRegisteredRoutePaths() {
  const raw = fs.readFileSync(path.join(__dirname, 'App.jsx'), 'utf8');
  return extractRoutesFromSource(raw);
}

// Regression test for a real false-negative: a nav item repointed at a route
// whose <Route> was commented out (retiring a page) used to still read as
// "registered", because the old parser matched `path=(['"])...` against raw
// source text with no notion of comments. Fixture-based (not App.jsx itself)
// so this doesn't depend on App.jsx ever actually containing a commented-out
// route.
describe('extractRoutesFromSource ignores a route that only appears inside a comment', () => {
  const fixtureSrc = `
    <Routes>
      <Route path='/console/real' element={<Real />} />
      {/* retired: <Route path='/console/ghost' element={<Ghost />} /> */}
      // <Route path='/console/also-ghost' element={<AlsoGhost />} />
    </Routes>
  `;
  const routes = extractRoutesFromSource(fixtureSrc);

  it('counts the live route', () => {
    expect(routes.has('/console/real')).toBe(true);
  });

  it('does not count a route inside a block ({/* */}) comment', () => {
    expect(routes.has('/console/ghost')).toBe(false);
  });

  it('does not count a route inside a line (//) comment', () => {
    expect(routes.has('/console/also-ghost')).toBe(false);
  });
});

// The two checks below, factored out of the per-item `it()`s so the fixture
// block after them can call the exact same logic against constructed items
// instead of only against whatever NAV_SECTIONS happens to contain today.
//
// Returns true when the item is fine (either out of scope for this check, or
// checked and correct) and false when it is the bug shape.
function enabledItemResolvesRoute(navItem, registered) {
  if (navItem.disabled || !navItem.href) return true; // out of scope here
  return registered.has(navItem.href);
}

function disabledItemHasNoWorkingHref(navItem, registered) {
  if (!navItem.disabled) return true; // out of scope here
  if (!navItem.href) return true; // the honest shape: no destination at all
  // A disabled:true item WITH an href that resolves is the bug this lane
  // fixed for "MJ / Task logs" — see file header.
  return !registered.has(navItem.href);
}

describe('nav rail destinations resolve to routes App.jsx actually registers', () => {
  const registered = collectRegisteredRoutePaths();

  // Guards the extraction itself: if App.jsx's quote style or the slug-array
  // shape ever changes enough that the regexes above stop matching, this
  // whole file should fail loudly instead of passing every case below for
  // the wrong reason (an empty `registered` set makes every href "missing").
  it('sanity: route extraction found a non-trivial table', () => {
    expect(registered.size).toBeGreaterThan(20);
  });

  const items = NAV_SECTIONS.flatMap((section) => section.items);

  it('sanity: there is at least one nav item to check', () => {
    expect(items.length).toBeGreaterThan(10);
  });

  describe.each(items.map((navItem) => [navItem.id, navItem]))(
    '%s',
    (_id, navItem) => {
      it('an enabled item with an href points at a registered route', () => {
        expect(enabledItemResolvesRoute(navItem, registered)).toBe(true);
      });

      it('a disabled item never points at a route that already works', () => {
        expect(disabledItemHasNoWorkingHref(navItem, registered)).toBe(true);
      });
    },
  );

  // NAV_SECTIONS has zero `disabled: true` items at HEAD (the last one, "MJ
  // / Task logs", was un-disabled by this lane) — every "disabled item never
  // points..." case directly above therefore hits the `!navItem.disabled`
  // early return and asserts nothing about a real item; that direction of
  // the check is unexercised by the parameterized block on its own. These
  // four fixture cases call the same two predicates against constructed
  // items, independent of what NAV_SECTIONS currently contains, so this
  // direction has at least one non-vacuous assertion at HEAD and a
  // regression in either predicate fails a real `it()` here.
  describe('both directions of the check are exercised by fixtures, independent of live NAV_SECTIONS content', () => {
    const workingHref = [...registered][0];
    const brokenHref = '/console/v2/__not-a-registered-route__';

    it('sanity: the working-href fixture is actually a registered route', () => {
      expect(registered.has(workingHref)).toBe(true);
    });

    it('sanity: the broken-href fixture is not a registered route', () => {
      expect(registered.has(brokenHref)).toBe(false);
    });

    it('an enabled item whose href resolves passes', () => {
      expect(
        enabledItemResolvesRoute(
          { disabled: false, href: workingHref },
          registered,
        ),
      ).toBe(true);
    });

    it('an enabled item whose href does not resolve fails', () => {
      expect(
        enabledItemResolvesRoute(
          { disabled: false, href: brokenHref },
          registered,
        ),
      ).toBe(false);
    });

    it('a disabled item with no href (the honest shape) passes', () => {
      expect(
        disabledItemHasNoWorkingHref(
          { disabled: true, href: null },
          registered,
        ),
      ).toBe(true);
    });

    it('a disabled item whose href resolves anyway (the bug shape) fails', () => {
      expect(
        disabledItemHasNoWorkingHref(
          { disabled: true, href: workingHref },
          registered,
        ),
      ).toBe(false);
    });
  });
});
