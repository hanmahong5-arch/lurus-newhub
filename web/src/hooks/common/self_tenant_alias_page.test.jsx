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

// The self-tenant alias, against a real v2 page rather than the hook alone.
//
// Two live states broke every panel of a signed-in console because the page
// routed by a slug stored in this browser:
//   - hub.lurus.cn 2026-09-22: `user` present, `tenant_slug` absent (only a
//     successful login bootstrap ever wrote it). Every request went to
//     /api/v2/default/... and was answered 404 — 20 in one window.
//   - the root tenant switcher: `tenant_slug` set to another tenant's slug.
//     The server takes the tenant from the session, so every request was
//     answered 403 TENANT_MISMATCH (reproduced on UAT the same day).
// With the alias, neither state changes a single request path, and there is
// nothing to resolve before the first render.
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
  getServerAddress: () => 'https://hub.example.test',
}));

// The Models page renders vendor logos from a ~4 MB pack; not under test.
vi.mock('@lobehub/icons', () => ({}));

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

const emptyModels = {
  data: { success: true, data: { items: [], total: 0, vendor_counts: {} } },
};

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

const renderAndCollectUrls = async () => {
  render(<HFModels />);
  await waitFor(() => expect(API.get).toHaveBeenCalled());
  return API.get.mock.calls.map(([url]) => String(url));
};

const expectOnlyAliasPaths = (urls) => {
  const tenantScoped = urls.filter((u) => u.startsWith('/api/v2/'));
  expect(tenantScoped.length).toBeGreaterThan(0);
  for (const u of tenantScoped) {
    expect(u.startsWith('/api/v2/~/')).toBe(true);
  }
};

describe('v2 console requests carry the self-tenant alias', () => {
  it('signed in with no stored slug: no /api/v2/default/, no lookup first', async () => {
    localStorage.setItem('user', JSON.stringify({ id: 14, role: 1 }));
    const urls = await renderAndCollectUrls();
    expectOnlyAliasPaths(urls);
    expect(urls.some((u) => u.includes('/api/v2/default/'))).toBe(false);
    // Nothing had to be learned before the page could ask for its data.
    expect(fetch).not.toHaveBeenCalled();
  });

  it('a stored slug naming another tenant does not reach a request path', async () => {
    localStorage.setItem('user', JSON.stringify({ id: 1, role: 100 }));
    localStorage.setItem('tenant_slug', 'switch');
    const urls = await renderAndCollectUrls();
    expectOnlyAliasPaths(urls);
    expect(urls.some((u) => u.includes('/api/v2/switch/'))).toBe(false);
  });

  it('a correct stored slug is not used for routing either', async () => {
    localStorage.setItem('user', JSON.stringify({ id: 14, role: 1 }));
    localStorage.setItem('tenant_slug', 'lurus');
    const urls = await renderAndCollectUrls();
    expectOnlyAliasPaths(urls);
  });
});
