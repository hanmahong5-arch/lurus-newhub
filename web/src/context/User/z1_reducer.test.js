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

import { reducer, initialState } from './reducer';

describe('User reducer', () => {
  it('starts logged out', () => {
    expect(initialState).toEqual({ user: undefined });
  });

  it("'login' stores the payload as the current user", () => {
    const user = { id: 7, username: 'alice', role: 1, group: 'default' };
    const next = reducer(initialState, { type: 'login', payload: user });
    expect(next.user).toEqual(user);
  });

  // Privilege safety: 'login' must REPLACE the user object, never merge into
  // it. A merge would let a previously-elevated role survive a refresh that
  // demoted the account, which is a silent privilege escalation.
  it("'login' replaces the whole user — a demoted role cannot linger", () => {
    const before = { user: { id: 7, username: 'alice', role: 100 } };
    const next = reducer(before, {
      type: 'login',
      payload: { id: 7, username: 'alice', role: 1 },
    });
    expect(next.user.role).toBe(1);
  });

  it("'login' with a payload that omits a field drops that field entirely", () => {
    const before = { user: { id: 7, username: 'alice', role: 100 } };
    const next = reducer(before, {
      type: 'login',
      payload: { id: 7, username: 'alice' },
    });
    expect(Object.prototype.hasOwnProperty.call(next.user, 'role')).toBe(false);
    expect(next.user.role).toBeUndefined();
  });

  it("'logout' clears the user", () => {
    const before = { user: { id: 7, username: 'alice', role: 100 } };
    expect(reducer(before, { type: 'logout' }).user).toBeUndefined();
  });

  it('never mutates the state it was handed', () => {
    const before = { user: { id: 7, role: 100 } };
    const loggedOut = reducer(before, { type: 'logout' });
    const loggedIn = reducer(before, { type: 'login', payload: { id: 9 } });
    expect(before.user).toEqual({ id: 7, role: 100 });
    expect(loggedOut).not.toBe(before);
    expect(loggedIn).not.toBe(before);
  });

  it('keeps unrelated state keys across both transitions', () => {
    const before = { user: undefined, tenantSlug: 'acme' };
    expect(reducer(before, { type: 'login', payload: { id: 1 } })).toEqual({
      user: { id: 1 },
      tenantSlug: 'acme',
    });
    expect(reducer(before, { type: 'logout' })).toEqual({
      user: undefined,
      tenantSlug: 'acme',
    });
  });

  // Identity matters: returning a fresh object for actions the reducer does
  // not understand would re-render every UserContext consumer for nothing.
  it('returns the identical state object for an unknown action', () => {
    const state = { user: { id: 1 } };
    expect(reducer(state, { type: 'refresh' })).toBe(state);
    expect(reducer(state, { type: '' })).toBe(state);
  });

  // Documented sharp edge: there is no payload guard, so a malformed
  // 'login' dispatch silently produces the logged-out shape rather than
  // failing loudly. Consumers must treat `user === undefined` as anonymous.
  it("'login' without a payload yields the logged-out shape", () => {
    expect(reducer(initialState, { type: 'login' }).user).toBeUndefined();
  });
});
