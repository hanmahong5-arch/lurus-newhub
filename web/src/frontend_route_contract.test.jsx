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
// App.jsx is parsed as source text rather than duplicating its route list
// into a second, driftable array here — the same choice the Go-side
// contract test makes for the same reason (collectRoutes() there reads the
// real gin engine, not a hand-maintained mirror).

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

// Every path App.jsx's <Routes> table registers, as literal strings (params
// like ':id' and the '*' catch-all stay literal too — nav items never point
// at those, so no segment-matching is needed here, unlike the Go test which
// has to match concrete request paths against gin's :param syntax).
function collectRegisteredRoutePaths() {
  const src = fs.readFileSync(path.join(__dirname, 'App.jsx'), 'utf8');
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
        if (navItem.disabled || !navItem.href) return; // out of scope here
        expect(registered.has(navItem.href)).toBe(true);
      });

      it('a disabled item never points at a route that already works', () => {
        if (!navItem.disabled) return; // out of scope here
        if (!navItem.href) return; // the honest shape: no destination at all
        // A disabled:true item WITH an href that resolves is the bug this
        // lane fixed for "MJ / Task logs" — see file header.
        expect(registered.has(navItem.href)).toBe(false);
      });
    },
  );
});
