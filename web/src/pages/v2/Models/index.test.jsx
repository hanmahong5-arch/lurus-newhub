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
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

// react-router-dom stub — useNavigate returns a spy so tests can assert
// navigation calls without a full router context.
const mockNavigate = vi.fn();
vi.mock('react-router-dom', () => ({
  useNavigate: () => mockNavigate,
}));

// Mock helpers BEFORE importing the component — vi.mock is hoisted but the
// component module reads API.get at runtime so mocks resolve at first call.
// isRoot defaults to false — every existing (pre-L2) test exercises a
// non-root viewer and must see exactly the same single models-list fetch it
// always did; tests that need root behaviour call
// `isRoot.mockReturnValue(true)` themselves.
vi.mock('../../../helpers', () => ({
  API: {
    get: vi.fn(),
    post: vi.fn(),
  },
  showError: vi.fn(),
  showSuccess: vi.fn(),
  isRoot: vi.fn(() => false),
}));

// HFShell pulls TenantSwitcher → API helper chain → react-router. Stub it
// to a passthrough wrapper so the test focuses on Models UI/logic.
vi.mock('../../../components/hifi/HFShell', () => ({
  default: ({ children, actions }) =>
    React.createElement(
      'div',
      { 'data-testid': 'hf-shell' },
      React.createElement('div', { 'data-testid': 'shell-actions' }, actions),
      children,
    ),
}));

// Mirror i18next's en behaviour: return the English defaultValue (2nd arg)
// with {{var}} interpolation, falling back to the key when no default given.
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key, fallback, opts) => {
      const vars =
        typeof fallback === 'object' && fallback !== null ? fallback : opts;
      let out = typeof fallback === 'string' ? fallback : key;
      if (vars) {
        for (const [k, v] of Object.entries(vars)) {
          out = out.split('{{' + k + '}}').join(String(v));
        }
      }
      return out;
    },
  }),
  initReactI18next: { type: '3rdParty', init: () => {} },
}));

import HFModels, {
  describeModelAvailability,
  modelMatchesAllowlist,
} from './index';
import { API, isRoot, showError } from '../../../helpers';

beforeEach(() => {
  API.get.mockReset();
  API.post.mockReset();
  showError.mockReset();
  mockNavigate.mockReset();
  isRoot.mockReset();
  isRoot.mockReturnValue(false);
  window.localStorage.clear();
  window.localStorage.setItem('tenant_slug', 'acme');
});

const fakeModelsResponse = (items, vendorCounts = {}) => ({
  data: {
    success: true,
    data: {
      items,
      total: items.length,
      limit: 100,
      offset: 0,
      vendor_counts: vendorCounts,
    },
  },
});

describe('Models page', () => {
  it('fetches models on mount', async () => {
    const items = [
      { id: 1, model_name: 'gpt-4o', vendor: 'OpenAI', status: 1 },
      {
        id: 2,
        model_name: 'claude-3.5-sonnet',
        vendor: 'Anthropic',
        status: 1,
      },
      { id: 3, model_name: 'gemini-1.5-pro', vendor: 'Google', status: 1 },
    ];
    // One fetch, not two: useTenantSlug resolves the slug before the first
    // render, so the mount no longer fires a throwaway request against the
    // placeholder 'default' tenant first.
    API.get.mockResolvedValue(fakeModelsResponse(items));

    render(<HFModels />);

    // API called at least once; the final call must use the slug from localStorage.
    await waitFor(() => {
      expect(API.get).toHaveBeenCalledTimes(1);
    });
    // Last call must target the correct tenant slug.
    const lastCall = API.get.mock.calls.at(-1);
    expect(lastCall[0]).toContain('/api/v2/acme/models');

    // All 3 model names should appear
    await waitFor(() => {
      expect(screen.getByTestId('model-card-gpt-4o')).toBeDefined();
      expect(screen.getByTestId('model-card-claude-3.5-sonnet')).toBeDefined();
      expect(screen.getByTestId('model-card-gemini-1.5-pro')).toBeDefined();
    });
  });

  // Lock for the hook's error effect (Models/index.jsx:84-92) — the page's
  // only failure surface for a load that came back but reported failure.
  it('shows an error toast when the models hook reports success:false', async () => {
    API.get.mockResolvedValue({
      data: { success: false, message: 'tenant not found' },
    });

    render(<HFModels />);

    await waitFor(() => {
      expect(showError).toHaveBeenCalledWith('tenant not found');
    });
  });

  it('filters by vendor', async () => {
    const allItems = [
      { id: 1, model_name: 'gpt-4o', vendor: 'OpenAI', status: 1 },
      { id: 2, model_name: 'gpt-4o-mini', vendor: 'OpenAI', status: 1 },
      {
        id: 3,
        model_name: 'claude-3.5-sonnet',
        vendor: 'Anthropic',
        status: 1,
      },
    ];
    // useTenantSlug triggers 2 initial fetches (default → acme), then 1 more on
    // vendor click — mock responds with appropriate data for each call.
    // Call 1 (slug=default): unfiltered
    API.get.mockResolvedValueOnce(
      fakeModelsResponse(allItems, { OpenAI: 2, Anthropic: 1 }),
    );
    // Call 2 (slug=acme): unfiltered
    API.get.mockResolvedValueOnce(
      fakeModelsResponse(allItems, { OpenAI: 2, Anthropic: 1 }),
    );
    // Call 3 (slug=acme, vendor=OpenAI): filtered
    API.get.mockResolvedValueOnce(
      fakeModelsResponse(
        allItems.filter((m) => m.vendor === 'OpenAI'),
        { OpenAI: 2, Anthropic: 1 },
      ),
    );

    render(<HFModels />);

    // Wait for vendor pills to appear after initial fetch
    await waitFor(() => {
      expect(screen.getByTestId('vendor-filter-OpenAI')).toBeDefined();
    });

    // Click OpenAI vendor pill
    fireEvent.click(screen.getByTestId('vendor-filter-OpenAI'));

    await waitFor(() => {
      // Mount (1) + the vendor filter change (2). Previously 3, the extra
      // one being the mount's placeholder-slug fetch.
      expect(API.get).toHaveBeenCalledTimes(2);
    });

    // Last call must include vendor=OpenAI
    const lastCall = API.get.mock.calls.at(-1);
    expect(lastCall[0]).toContain('vendor=OpenAI');
  });

  it('carries no WIP banner — the add button is present and enabled', async () => {
    API.get.mockResolvedValue(fakeModelsResponse([]));

    render(<HFModels />);

    await waitFor(() => {
      const addBtn = screen.getByTestId('models-add-btn');
      expect(addBtn).toBeDefined();
      expect(addBtn.disabled).toBe(false);
    });

    // Neither banner this lane removed is rendered anywhere. Presence of
    // either would mean a real, shipped capability is still being told to
    // the user as "deferred to v3".
    expect(screen.queryByText(/deferred to v3/i, { exact: false })).toBeNull();
  });

  it('a non-root viewer never sees the admin availability link', async () => {
    isRoot.mockReturnValue(false);
    API.get.mockResolvedValue(
      fakeModelsResponse([
        { id: 1, model_name: 'gpt-4o', vendor: 'OpenAI', status: 1 },
      ]),
    );

    render(<HFModels />);

    await waitFor(() => {
      expect(screen.getByTestId('model-card-gpt-4o')).toBeDefined();
    });
    expect(screen.queryByTestId('models-manage-availability-link')).toBeNull();
    // Non-root never reaches user/me or the admin allow-list endpoint —
    // the models-list fetch is the only call.
    expect(API.get).toHaveBeenCalledTimes(1);
  });

  it('role >= 100 sees the admin availability link and it navigates to the admin page', async () => {
    isRoot.mockReturnValue(true);
    API.get.mockImplementation((url) => {
      const u = String(url);
      if (u.includes('/user/me')) {
        return Promise.resolve({
          data: { success: true, data: { tenant_id: 't-1' } },
        });
      }
      if (u.includes('/model-allowlist')) {
        return Promise.resolve({
          data: {
            success: true,
            data: { configured: false, allowed_models: [], mode: 'observe' },
          },
        });
      }
      return Promise.resolve(fakeModelsResponse([]));
    });

    render(<HFModels />);

    await waitFor(() => {
      expect(
        screen.getByTestId('models-manage-availability-link'),
      ).toBeDefined();
    });

    fireEvent.click(screen.getByTestId('models-manage-availability-link'));
    expect(mockNavigate).toHaveBeenCalledWith('/console/v2/admin/model-limits');
  });

  it('a non-root fetch failure (stale client-side role) shows no availability badge, not a crash', async () => {
    isRoot.mockReturnValue(true);
    API.get.mockImplementation((url) => {
      const u = String(url);
      if (u.includes('/user/me') || u.includes('/model-allowlist')) {
        return Promise.reject({ response: { status: 403 } });
      }
      return Promise.resolve(
        fakeModelsResponse([
          { id: 1, model_name: 'gpt-4o', vendor: 'OpenAI', status: 1 },
        ]),
      );
    });

    render(<HFModels />);

    await waitFor(() => {
      expect(screen.getByTestId('model-card-gpt-4o')).toBeDefined();
    });
    expect(screen.queryByTestId('model-availability-gpt-4o')).toBeNull();
  });

  // The oracle this cycle exists for: an observe-mode "not on the list"
  // model must never render like an enforce-mode blocked one — observe
  // still answers (internal/adapter/middleware/distributor.go), only
  // counted.
  it('renders observe-mode "off the list" distinctly from enforce-mode "blocked"', async () => {
    isRoot.mockReturnValue(true);
    const items = [
      { id: 1, model_name: 'gpt-4o', vendor: 'OpenAI', status: 1 },
    ];
    API.get.mockImplementation((url) => {
      const u = String(url);
      if (u.includes('/user/me')) {
        return Promise.resolve({
          data: { success: true, data: { tenant_id: 't-1' } },
        });
      }
      if (u.includes('/model-allowlist')) {
        return Promise.resolve({
          data: {
            success: true,
            data: {
              configured: true,
              allowed_models: ['claude-3-5-sonnet'],
              mode: 'observe',
            },
          },
        });
      }
      return Promise.resolve(fakeModelsResponse(items));
    });

    const { unmount } = render(<HFModels />);
    const observedEl = await waitFor(() =>
      screen.getByTestId('model-availability-gpt-4o'),
    );
    expect(observedEl.dataset.availability).toBe('observed');
    expect(observedEl.textContent).toContain('still answers');
    unmount();

    // Re-render with the same allow-list but enforce mode.
    API.get.mockImplementation((url) => {
      const u = String(url);
      if (u.includes('/user/me')) {
        return Promise.resolve({
          data: { success: true, data: { tenant_id: 't-1' } },
        });
      }
      if (u.includes('/model-allowlist')) {
        return Promise.resolve({
          data: {
            success: true,
            data: {
              configured: true,
              allowed_models: ['claude-3-5-sonnet'],
              mode: 'enforce',
            },
          },
        });
      }
      return Promise.resolve(fakeModelsResponse(items));
    });

    render(<HFModels />);
    const blockedEl = await waitFor(() =>
      screen.getByTestId('model-availability-gpt-4o'),
    );
    expect(blockedEl.dataset.availability).toBe('blocked');
    expect(blockedEl.textContent).not.toContain('still answers');
  });

  // Wave 3 Phase 1 — "add model opens modal and posts"
  it('add model opens modal and posts', async () => {
    // useTenantSlug fires two fetches (default → acme), then one after submit.
    API.get.mockResolvedValue(fakeModelsResponse([]));
    API.post.mockResolvedValueOnce({
      data: {
        success: true,
        data: { id: 42, model_name: 'my-model', status: 1 },
      },
    });

    render(<HFModels />);

    // Wait for component to settle after initial fetches.
    await waitFor(() => expect(API.get).toHaveBeenCalled());

    // Click "+ 添加模型" to open modal.
    fireEvent.click(screen.getByTestId('models-add-btn'));

    // Dialog should appear (jsdom doesn't run showModal but the element renders).
    expect(screen.getByTestId('models-add-dialog')).toBeDefined();

    // Fill in model name.
    const nameInput = screen.getByTestId('add-model-name');
    fireEvent.change(nameInput, { target: { value: 'my-model' } });

    // Submit.
    fireEvent.click(screen.getByTestId('add-model-submit'));

    await waitFor(() => {
      expect(API.post).toHaveBeenCalledTimes(1);
    });

    const [url, body] = API.post.mock.calls[0];
    expect(url).toContain('/api/v2/acme/models');
    expect(body.model_name).toBe('my-model');
  });

  // Wave 3 Phase 1 — "试用 navigates with prefill_model param"
  it('试用 navigates with prefill param', async () => {
    const items = [
      { id: 1, model_name: 'gpt-4o', vendor: 'OpenAI', status: 1 },
    ];
    API.get.mockResolvedValue(fakeModelsResponse(items));

    render(<HFModels />);

    // Wait for model card to appear.
    await waitFor(() => {
      expect(screen.getByTestId('model-card-gpt-4o')).toBeDefined();
    });

    // Click 试用 button for gpt-4o.
    fireEvent.click(screen.getByTestId('model-try-gpt-4o'));

    expect(mockNavigate).toHaveBeenCalledTimes(1);
    const navArg = mockNavigate.mock.calls[0][0];
    expect(navArg).toContain('/console/v2/playground');
    expect(navArg).toContain('prefill_model=gpt-4o');
  });
});

describe('describeModelAvailability / modelMatchesAllowlist', () => {
  it('reports unrestricted when the tenant has no allow-list row', () => {
    expect(describeModelAvailability(null, 'gpt-4o')).toBe('unrestricted');
    expect(describeModelAvailability({ configured: false }, 'gpt-4o')).toBe(
      'unrestricted',
    );
  });

  it('matches an exact entry and a trailing-wildcard prefix', () => {
    expect(modelMatchesAllowlist(['gpt-4o'], 'gpt-4o')).toBe(true);
    expect(modelMatchesAllowlist(['gpt-4o'], 'gpt-4o-mini')).toBe(false);
    expect(modelMatchesAllowlist(['gpt-4*'], 'gpt-4o-mini')).toBe(true);
    expect(modelMatchesAllowlist(['claude-*'], 'gpt-4o')).toBe(false);
  });

  it('reports allowed for a model on the list regardless of mode', () => {
    const allowlist = {
      configured: true,
      allowed_models: ['gpt-4o'],
      mode: 'enforce',
    };
    expect(describeModelAvailability(allowlist, 'gpt-4o')).toBe('allowed');
  });

  it('reports observed (not blocked) for an off-list model under observe', () => {
    const allowlist = {
      configured: true,
      allowed_models: ['gpt-4o'],
      mode: 'observe',
    };
    expect(describeModelAvailability(allowlist, 'claude-3-5-sonnet')).toBe(
      'observed',
    );
  });

  it('reports blocked for an off-list model under enforce', () => {
    const allowlist = {
      configured: true,
      allowed_models: ['gpt-4o'],
      mode: 'enforce',
    };
    expect(describeModelAvailability(allowlist, 'claude-3-5-sonnet')).toBe(
      'blocked',
    );
  });
});
