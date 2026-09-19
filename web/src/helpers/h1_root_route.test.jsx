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
import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';

/*
 * RootRoute is the client-side half of the change that gave the seven
 * root-only rail entries a minRole:100 (components/hifi/HFShell.jsx). Hiding
 * a link is cosmetics: the /console/v2/admin pages were reachable by typing
 * the URL, and what came back was a page rendered against a refusal. The
 * server is still the authority — every one of those pages calls a route
 * under /api/v2/admin, which middleware.RootJWTAuth answers with 403 — but
 * the console should not mount a root-only page for a role-10 admin in the
 * first place.
 *
 * This mirrors the existing AdminRoute suite in h1_dashboard_misc.test.jsx,
 * one notch up: role >= 100 instead of role >= 10.
 */

// auth.jsx renders react-router Navigate; a stub keeps the assertions about
// WHERE it redirects rather than about router internals.
vi.mock('react-router-dom', () => ({
  Navigate: ({ to, replace, state }) =>
    React.createElement('div', {
      'data-testid': 'navigate',
      'data-to': to,
      'data-replace': String(!!replace),
      'data-has-from': state && 'from' in state ? 'yes' : 'no',
    }),
}));

import { RootRoute } from './auth';

const child = <span data-testid='child'>secret</span>;

beforeEach(() => {
  localStorage.clear();
});

const to = () => screen.getByTestId('navigate').getAttribute('data-to');

describe('RootRoute', () => {
  it('admits role 100', () => {
    localStorage.setItem('user', JSON.stringify({ id: 1, role: 100 }));
    render(<RootRoute>{child}</RootRoute>);
    expect(screen.getByTestId('child')).toBeTruthy();
  });

  it('sends a role-10 admin to /forbidden, not to /login', () => {
    localStorage.setItem('user', JSON.stringify({ id: 1, role: 10 }));
    render(<RootRoute>{child}</RootRoute>);
    expect(to()).toBe('/forbidden');
    expect(screen.queryByTestId('child')).toBeNull();
  });

  it('sends a regular user to /forbidden', () => {
    localStorage.setItem('user', JSON.stringify({ id: 1, role: 1 }));
    render(<RootRoute>{child}</RootRoute>);
    expect(to()).toBe('/forbidden');
  });

  it('sends an anonymous visitor to /login, remembering where', () => {
    render(<RootRoute>{child}</RootRoute>);
    expect(to()).toBe('/login');
    expect(screen.getByTestId('navigate').getAttribute('data-has-from')).toBe(
      'yes',
    );
  });

  it('rejects a non-numeric role rather than coercing it', () => {
    // A string "100" must NOT clear the gate: >= on mixed types is exactly
    // how privilege checks get bypassed.
    localStorage.setItem('user', JSON.stringify({ id: 1, role: '100' }));
    render(<RootRoute>{child}</RootRoute>);
    expect(to()).toBe('/forbidden');
  });

  it('fails closed on a corrupted user shim', () => {
    localStorage.setItem('user', '{"role":100,');
    render(<RootRoute>{child}</RootRoute>);
    expect(to()).toBe('/forbidden');
  });
});
