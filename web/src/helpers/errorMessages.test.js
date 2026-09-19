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
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';

// Echo keys so the status→message mapping branch is asserted deterministically,
// independent of i18n init timing.
vi.mock('../i18n/i18n', () => ({ default: { t: (k) => k } }));

import {
  resolveErrorMessage,
  isHardUnauthorized,
  shouldEmit,
  _resetDedup,
} from './errorMessages';

// i18n is not initialized in the unit env, so t() returns the key — assertions
// target the resolved key, which is enough to verify the mapping branch taken.
describe('resolveErrorMessage', () => {
  it('maps a no-response axios error to the network key', () => {
    expect(resolveErrorMessage({ isAxiosError: true })).toBe(
      'console.errors.network',
    );
  });

  it('maps statuses to calm business keys', () => {
    expect(resolveErrorMessage({ response: { status: 401 } })).toBe(
      'console.errors.session_expired',
    );
    expect(resolveErrorMessage({ response: { status: 403 } })).toBe(
      'console.errors.forbidden',
    );
    expect(resolveErrorMessage({ response: { status: 404 } })).toBe(
      'console.errors.not_found',
    );
    expect(resolveErrorMessage({ response: { status: 429 } })).toBe(
      'console.errors.rate_limited',
    );
    expect(resolveErrorMessage({ response: { status: 503 } })).toBe(
      'console.errors.server',
    );
  });

  // Cycle-12 L4: 409 has no status mapping, so before this the user was
  // shown the backend's raw English sentence ("Session registry is disabled
  // on this deployment") in a zh-default console. The error_code is what the
  // copy hangs off, so a reworded backend message cannot silently change
  // what the user reads.
  it('maps error_code SESSION_REGISTRY_DISABLED to console copy, not the server sentence', () => {
    expect(
      resolveErrorMessage({
        response: {
          status: 409,
          data: {
            success: false,
            error_code: 'SESSION_REGISTRY_DISABLED',
            message: 'Session registry is disabled on this deployment',
          },
        },
      }),
    ).toBe('console.settings.session_registry_disabled');
  });

  it('still falls back to the backend message for an unmapped 409', () => {
    expect(
      resolveErrorMessage({
        response: {
          status: 409,
          data: { success: false, message: 'some other conflict' },
        },
      }),
    ).toBe('some other conflict');
  });

  it('never surfaces the internal "Layer C" technical 401 copy', () => {
    const msg = resolveErrorMessage({ response: { status: 401 } });
    expect(msg).not.toMatch(/Layer C/i);
    expect(msg).not.toMatch(/access token/i);
  });

  it('falls back to a backend message for unmapped statuses', () => {
    expect(
      resolveErrorMessage({
        response: { status: 422, data: { message: 'quota exhausted' } },
      }),
    ).toBe('quota exhausted');
  });
});

describe('isHardUnauthorized', () => {
  afterEach(() => localStorage.clear());

  it('is true on 401 with no client-side user', () => {
    localStorage.removeItem('user');
    expect(isHardUnauthorized({ response: { status: 401 } })).toBe(true);
  });

  it('is false on 401 when a user shim exists (transient blip)', () => {
    localStorage.setItem('user', JSON.stringify({ id: 1 }));
    expect(isHardUnauthorized({ response: { status: 401 } })).toBe(false);
  });

  it('is false for non-401 statuses', () => {
    localStorage.removeItem('user');
    expect(isHardUnauthorized({ response: { status: 500 } })).toBe(false);
  });
});

describe('shouldEmit (toast de-duplication)', () => {
  beforeEach(() => _resetDedup());

  it('emits the first occurrence and suppresses repeats within the window', () => {
    expect(shouldEmit('boom', 1000)).toBe(true);
    expect(shouldEmit('boom', 1200)).toBe(false); // within 1.5s window
  });

  it('re-emits the same message after the window elapses', () => {
    expect(shouldEmit('boom', 1000)).toBe(true);
    expect(shouldEmit('boom', 3000)).toBe(true); // window expired
  });

  it('does not suppress distinct messages', () => {
    expect(shouldEmit('a', 1000)).toBe(true);
    expect(shouldEmit('b', 1000)).toBe(true);
  });
});
