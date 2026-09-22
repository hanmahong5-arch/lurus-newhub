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

// The app-start tenant-slug gate, against a real v2 page rather than the
// hook alone — the defect this locks was invisible from the hook's own
// tests: they seeded a slug, so the fallback never ran.
//
// Production state, hub.lurus.cn 2026-09-22: `user` present (the shell
// renders as signed in), `tenant_slug` absent (only a successful bootstrap
// ever wrote it, and the bootstrap was 401ing). Every page then put the
// hook's fallback in the path and TenantSlugGuard 404'd it — 20 such
// requests in one window, all on /api/v2/default/..., all reported to the
// operator as "could not load".
import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, waitFor } from '@testing-library/react';

const mockNavigate = vi.fn();
vi.mock('react-router-dom', () => ({ useNavigate: () => mockNavigate }));

vi.mock('../../helpers', () => ({
  API: { get: vi.fn(), post: vi.fn() },
  showError: vi.fn(),
  showSuccess: vi.fn(),
  isRoot: vi.fn(() => false),
}));

vi.mock('../../components/hifi/HFShell', () => ({
  default: ({ children }) => React.createElement('div', null, children),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key, fallback) => (typeof fallback === 'string' ? fallback : key),
  }),
  initReactI18next: { type: '3rdParty', init: () => {} },
}));

import HFModels from '../../pages/v2/Models';
import { API } from '../../helpers';
import { prepareTenantSlug } from '../../helpers/apiMode';

const emptyModels = {
  data: { success: true, data: { items: [], total: 0, vendor_counts: {} } },
};

const sessionInfo = (slug) => ({
  ok: true,
  json: async () => ({ success: true, data: { id: 14, tenant_slug: slug } }),
});

beforeEach(() => {
  localStorage.clear();
  API.get.mockReset();
  API.get.mockResolvedValue(emptyModels);
  API.post.mockReset();
  mockNavigate.mockReset();
  vi.stubGlobal('fetch', vi.fn());
});

afterEach(() => {
  vi.unstubAllGlobals();
});

const requestedUrls = () => API.get.mock.calls.map(([url]) => url);

describe('tenant slug resolution at app start', () => {
  it('resolves the slug before anything renders, so a page mounts against the real tenant', async () => {
    localStorage.setItem('user', JSON.stringify({ id: 14, role: 1 }));
    // No tenant_slug — the production state.
    fetch.mockResolvedValue(sessionInfo('lurus'));

    // What src/index.jsx waits on before root.render().
    await prepareTenantSlug();
    render(<HFModels />);

    await waitFor(() => expect(API.get).toHaveBeenCalled());
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(String(fetch.mock.calls[0][0])).toContain(
      '/api/v2/auth/session-info',
    );
    expect(requestedUrls().every((u) => u.startsWith('/api/v2/lurus/'))).toBe(
      true,
    );
  });

  it('asks nothing when no one is signed in', async () => {
    // No `user` shim: there is no session to name a tenant, and a
    // session-info call would just be a 401 on every cold load of /login.
    await prepareTenantSlug();
    expect(fetch).not.toHaveBeenCalled();
  });

  it('asks once for a burst of concurrent callers', async () => {
    localStorage.setItem('user', JSON.stringify({ id: 14, role: 1 }));
    fetch.mockResolvedValue(sessionInfo('lurus'));

    const slugs = await Promise.all([
      prepareTenantSlug(),
      prepareTenantSlug(),
      prepareTenantSlug(),
    ]);

    expect(slugs).toEqual(['lurus', 'lurus', 'lurus']);
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it('never invents a tenant name when the slug could not be resolved', async () => {
    // The gate ran and came back empty (no session, offline, timed out).
    // Whatever the page puts in the path now, it must not be a literal that
    // looks like a tenant: 'default' is the tenant *id* this deployment
    // uses, its routing slug is 'lurus', and asking for the id is exactly
    // the 404 this file exists to keep out. The placeholder is unroutable on
    // purpose — TenantSlugGuard answers 404 TENANT_NOT_FOUND, which is what
    // helpers/api.js repairs and replays.
    localStorage.setItem('user', JSON.stringify({ id: 14, role: 1 }));
    fetch.mockResolvedValue({ ok: false, status: 401, json: async () => ({}) });

    expect(await prepareTenantSlug()).toBe('');
    render(<HFModels />);

    await waitFor(() => expect(API.get).toHaveBeenCalled());
    const urls = requestedUrls();
    expect(urls.some((u) => u.includes('/api/v2/default/'))).toBe(false);
    expect(urls.every((u) => u.startsWith('/api/v2/-/'))).toBe(true);
  });
});
