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
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';

import {
  isV2Mode,
  getTenantSlug,
  setTenantSlug,
  clearTenantSlug,
  v2Url,
  ensureTenantSlug,
} from './apiMode';

// These helpers persist their one piece of state in jsdom's localStorage
// (not network/timers), so behavior is fully deterministic per test as long
// as we clear it between cases.
describe('tenant slug helpers (apiMode)', () => {
  afterEach(() => clearTenantSlug());

  it('isV2Mode is false with no tenant slug set', () => {
    clearTenantSlug();
    expect(isV2Mode()).toBe(false);
  });

  it('getTenantSlug returns an empty string when unset', () => {
    clearTenantSlug();
    expect(getTenantSlug()).toBe('');
  });

  it('setTenantSlug persists the slug and flips isV2Mode to true', () => {
    setTenantSlug('acme');
    expect(getTenantSlug()).toBe('acme');
    expect(isV2Mode()).toBe(true);
  });

  it('setTenantSlug ignores a falsy value (does not clear or overwrite)', () => {
    setTenantSlug('acme');
    setTenantSlug('');
    expect(getTenantSlug()).toBe('acme');
    setTenantSlug(null);
    expect(getTenantSlug()).toBe('acme');
  });

  it('clearTenantSlug removes the slug and flips isV2Mode back to false', () => {
    setTenantSlug('acme');
    clearTenantSlug();
    expect(getTenantSlug()).toBe('');
    expect(isV2Mode()).toBe(false);
  });
});

describe('v2Url', () => {
  afterEach(() => clearTenantSlug());

  it('builds the path using the configured tenant slug', () => {
    setTenantSlug('acme');
    expect(v2Url('/tokens?p=1&size=10')).toBe(
      '/api/v2/acme/tokens?p=1&size=10',
    );
  });

  it('invents no tenant when no slug is set', () => {
    // It used to answer '/api/v2/lurus/tenants' — this deployment's own
    // tenant slug, compiled into the frontend, for a browser that had never
    // been told which tenant it belongs to. The single caller
    // (helpers/token.js) gates on isV2Mode(), i.e. "a slug is stored", so
    // this path is not one a request travels; what matters is that no
    // deployment-specific name is minted here.
    clearTenantSlug();
    expect(v2Url('/tenants')).not.toContain('lurus');
    expect(v2Url('/tenants')).toBe('/api/v2//tenants');
  });

  it('appends an empty path segment unchanged', () => {
    setTenantSlug('acme');
    expect(v2Url('')).toBe('/api/v2/acme');
  });
});

// GET /api/v2/auth/session-info is the console's only way to learn its own
// routing slug that does not depend on a platform cookie — see
// handler/oauth.go GetSessionInfo, which resolves it from the user's tenant
// when the session does not carry one.
describe('ensureTenantSlug', () => {
  beforeEach(() => {
    localStorage.clear();
    vi.stubGlobal('fetch', vi.fn());
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    clearTenantSlug();
  });

  const answers = (slug, ok = true) =>
    fetch.mockResolvedValue({
      ok,
      json: async () => ({ success: true, data: { tenant_slug: slug } }),
    });

  it('returns the stored slug without asking the server', async () => {
    setTenantSlug('acme');
    await expect(ensureTenantSlug()).resolves.toBe('acme');
    expect(fetch).not.toHaveBeenCalled();
  });

  it('asks the server when nothing is stored, and stores the answer', async () => {
    answers('lurus');
    await expect(ensureTenantSlug()).resolves.toBe('lurus');
    expect(getTenantSlug()).toBe('lurus');
    expect(String(fetch.mock.calls[0][0])).toContain(
      '/api/v2/auth/session-info',
    );
  });

  it('re-asks under force, because the stored slug is what was just rejected', async () => {
    setTenantSlug('default');
    answers('lurus');
    await expect(ensureTenantSlug({ force: true })).resolves.toBe('lurus');
    expect(getTenantSlug()).toBe('lurus');
  });

  it('collapses concurrent callers into a single request', async () => {
    answers('lurus');
    const all = await Promise.all([
      ensureTenantSlug(),
      ensureTenantSlug(),
      ensureTenantSlug(),
    ]);
    expect(all).toEqual(['lurus', 'lurus', 'lurus']);
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it('answers "" — and stores nothing — when the session is not valid', async () => {
    fetch.mockResolvedValue({ ok: false, status: 401, json: async () => ({}) });
    await expect(ensureTenantSlug()).resolves.toBe('');
    expect(getTenantSlug()).toBe('');
  });

  it('answers "" when the request itself fails', async () => {
    fetch.mockRejectedValue(new Error('offline'));
    await expect(ensureTenantSlug()).resolves.toBe('');
    expect(getTenantSlug()).toBe('');
  });

  it('releases the in-flight slot after a failure, so a later call can retry', async () => {
    fetch.mockRejectedValueOnce(new Error('offline'));
    await expect(ensureTenantSlug()).resolves.toBe('');
    answers('lurus');
    await expect(ensureTenantSlug()).resolves.toBe('lurus');
    expect(fetch).toHaveBeenCalledTimes(2);
  });
});
