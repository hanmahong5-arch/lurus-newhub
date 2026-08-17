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

describe('Status reducer', () => {
  it('starts with no server status loaded', () => {
    expect(initialState).toEqual({ status: undefined });
  });

  it("'set' stores the server config payload verbatim", () => {
    const payload = {
      ServerAddress: 'https://hub.example.test',
      HeaderNavModules: '{"pricing":{"requireAuth":true}}',
      QuotaPerUnit: 500000,
    };
    expect(reducer(initialState, { type: 'set', payload }).status).toEqual(
      payload,
    );
  });

  // Degradation contract: the backend may ship a status object that is
  // missing keys the frontend knows about. Those read back as `undefined`,
  // NOT as a built-in default — every consumer has to supply its own
  // fallback (see App.jsx's `statusState?.status?.HeaderNavModules`).
  it('leaves fields absent from the payload as undefined, not defaulted', () => {
    const next = reducer(initialState, {
      type: 'set',
      payload: { ServerAddress: 'https://hub.example.test' },
    });
    expect(next.status.ServerAddress).toBe('https://hub.example.test');
    expect(next.status.HeaderNavModules).toBeUndefined();
    expect(next.status.QuotaPerUnit).toBeUndefined();
  });

  // A second 'set' REPLACES rather than merges — a feature flag removed by
  // the backend must actually disappear, not stay on from the last fetch.
  it("'set' replaces the previous status instead of merging it", () => {
    const before = { status: { DisplayInCurrencyEnabled: true, Foo: 1 } };
    const next = reducer(before, {
      type: 'set',
      payload: { DisplayInCurrencyEnabled: false },
    });
    expect(next.status).toEqual({ DisplayInCurrencyEnabled: false });
    expect(next.status.Foo).toBeUndefined();
  });

  it("'unset' clears the cached status", () => {
    const before = { status: { ServerAddress: 'https://hub.example.test' } };
    expect(reducer(before, { type: 'unset' }).status).toBeUndefined();
  });

  it('never mutates the state it was handed', () => {
    const before = { status: { ServerAddress: 'a' } };
    reducer(before, { type: 'set', payload: { ServerAddress: 'b' } });
    reducer(before, { type: 'unset' });
    expect(before.status).toEqual({ ServerAddress: 'a' });
  });

  it('returns the identical state object for an unknown action', () => {
    const state = { status: { ServerAddress: 'a' } };
    expect(reducer(state, { type: 'reset' })).toBe(state);
  });

  it('keeps unrelated state keys across both transitions', () => {
    const before = { status: undefined, lastFetchedAt: 42 };
    expect(reducer(before, { type: 'set', payload: { A: 1 } })).toEqual({
      status: { A: 1 },
      lastFetchedAt: 42,
    });
    expect(reducer(before, { type: 'unset' })).toEqual({
      status: undefined,
      lastFetchedAt: 42,
    });
  });
});
