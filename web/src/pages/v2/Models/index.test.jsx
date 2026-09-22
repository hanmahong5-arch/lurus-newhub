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

// isRoot defaults to false; tests that need root behaviour set it.
vi.mock('../../../helpers', () => ({
  API: {
    get: vi.fn(),
    post: vi.fn(),
  },
  showError: vi.fn(),
  showSuccess: vi.fn(),
  isRoot: vi.fn(() => false),
  getServerAddress: () => 'https://hub.example.test',
}));

// Vendor logos come from a ~4 MB pack loaded on first render; nothing here
// depends on it.
vi.mock('@lobehub/icons', () => ({}));

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

// The page reads four endpoints; each test states what they answer.
//   catalogue  GET ~/models?...           (admin metadata table)
//   routable   GET ~/models/routable      (what this caller can call)
//   pricing    GET ~/pricing
//   usage      GET ~/analytics/rankings   (tenant-admin gated)
const ok = (data) => Promise.resolve({ data: { success: true, data } });
const serve = ({
  catalogue = [],
  routable = [],
  pricing = [],
  groupRatio = { default: 1 },
  usage = null,
  extra = () => null,
} = {}) =>
  API.get.mockImplementation((url) => {
    const u = String(url);
    const hit = extra(u);
    if (hit) return hit;
    if (u.includes('/models/routable')) return ok({ items: routable });
    if (u.includes('/models')) {
      return ok({
        items: catalogue,
        total: catalogue.length,
        limit: 100,
        offset: 0,
        vendor_counts: {},
      });
    }
    if (u.includes('/pricing')) {
      return ok({ pricing, vendors: [], group_ratio: groupRatio });
    }
    if (u.includes('/analytics/rankings')) {
      return usage
        ? ok({ rows: usage })
        : Promise.reject({ response: { status: 403 } });
    }
    return Promise.reject(new Error('unexpected GET ' + u));
  });

// UAT, 2026-09-22: the catalogue table empty, two models routable and
// priced. The old page read only the table and said "0 models".
const UAT = {
  catalogue: [],
  routable: [
    {
      id: 'deepseek-chat',
      owned_by: 'deepseek',
      supported_endpoint_types: ['openai'],
    },
    { id: 'faultsim-music', owned_by: 'custom', supported_endpoint_types: [] },
  ],
  pricing: [
    {
      model_name: 'deepseek-chat',
      vendor: 'DeepSeek',
      quota_type: 0,
      model_ratio: 0.135,
      completion_ratio: 4,
      cache_ratio: 0.25,
      description: 'DeepSeek V3 chat model',
      supported_endpoint_types: ['openai', 'anthropic'],
    },
    { model_name: 'faultsim-music', quota_type: 1, model_price: 0.1 },
    {
      model_name: 'gpt-4o',
      vendor: 'OpenAI',
      quota_type: 0,
      model_ratio: 1.25,
    },
  ],
};

const rootAllowlist = (list) => (u) => {
  if (u.includes('/user/me')) return ok({ tenant_id: 't-1' });
  if (u.includes('/model-allowlist')) return ok(list);
  return null;
};

beforeEach(() => {
  API.get.mockReset();
  API.post.mockReset();
  showError.mockReset();
  mockNavigate.mockReset();
  isRoot.mockReset();
  isRoot.mockReturnValue(false);
  window.localStorage.clear();
});

describe('Models marketplace', () => {
  it('lists what the caller can call even when the catalogue table is empty', async () => {
    serve(UAT);
    render(<HFModels />);
    await waitFor(() => {
      expect(screen.getByTestId('model-card-deepseek-chat')).toBeDefined();
      expect(screen.getByTestId('model-card-faultsim-music')).toBeDefined();
    });
    expect(screen.queryByTestId('models-empty')).toBeNull();
    // Priced but outside the caller's groups: listed, and said so.
    expect(screen.getByTestId('model-not-callable-gpt-4o')).toBeDefined();
    expect(screen.getByTestId('model-callable-deepseek-chat')).toBeDefined();
    expect(API.get.mock.calls.map((c) => String(c[0]))).toEqual(
      expect.arrayContaining([
        expect.stringContaining('/api/v2/~/models?'),
        expect.stringContaining('/api/v2/~/models/routable'),
        expect.stringContaining('/api/v2/~/pricing'),
      ]),
    );
  });

  it('states prices per 1M tokens (ratio x 2, output x completion ratio)', async () => {
    serve(UAT);
    render(<HFModels />);
    const card = await waitFor(() =>
      screen.getByTestId('model-card-deepseek-chat'),
    );
    await waitFor(() => expect(card.textContent).toContain('$0.27'));
    expect(card.textContent).toContain('$1.08');
    expect(card.textContent).toContain('DeepSeek V3 chat model');
    const perCall = screen.getByTestId('model-card-faultsim-music');
    expect(perCall.textContent).toContain('$0.1');
    expect(perCall.textContent).toContain('per call');
  });

  it('applies the caller group multiplier to the prices shown', async () => {
    window.localStorage.setItem('user', JSON.stringify({ group: 'vip' }));
    serve({ ...UAT, groupRatio: { default: 1, vip: 2 } });
    render(<HFModels />);
    const card = await waitFor(() =>
      screen.getByTestId('model-card-deepseek-chat'),
    );
    await waitFor(() => expect(card.textContent).toContain('$0.54'));
  });

  it('shows 7-day usage when the caller may read it, and 0 when refused', async () => {
    serve({
      ...UAT,
      usage: [{ name: 'deepseek-chat', total_tokens: 1_234_000, requests: 9 }],
    });
    const { unmount } = render(<HFModels />);
    await waitFor(() =>
      expect(
        screen.getByTestId('model-card-deepseek-chat').textContent,
      ).toContain('1.2M'),
    );
    unmount();
    serve(UAT); // rankings answers 403
    render(<HFModels />);
    const card = await waitFor(() =>
      screen.getByTestId('model-card-deepseek-chat'),
    );
    expect(card.textContent).not.toContain('1.2M');
    expect(showError).not.toHaveBeenCalled();
  });

  it('filters by search, vendor facet and callable-only', async () => {
    serve(UAT);
    render(<HFModels />);
    await waitFor(() => screen.getByTestId('model-card-gpt-4o'));

    fireEvent.change(screen.getByTestId('models-search'), {
      target: { value: 'v3 chat' },
    });
    expect(screen.queryByTestId('model-card-faultsim-music')).toBeNull();
    expect(screen.getByTestId('model-card-deepseek-chat')).toBeDefined();
    fireEvent.change(screen.getByTestId('models-search'), {
      target: { value: '' },
    });

    const openai = () =>
      screen.getByTestId('vendor-facet-OpenAI').querySelector('input');
    fireEvent.click(openai());
    expect(screen.getByTestId('model-card-gpt-4o')).toBeDefined();
    expect(screen.queryByTestId('model-card-deepseek-chat')).toBeNull();
    fireEvent.click(openai());

    fireEvent.click(screen.getByTestId('models-callable-only'));
    expect(screen.queryByTestId('model-card-gpt-4o')).toBeNull();
    expect(screen.getByTestId('model-card-deepseek-chat')).toBeDefined();
  });

  it('switches to a table with one row per model', async () => {
    serve(UAT);
    render(<HFModels />);
    await waitFor(() => screen.getByTestId('model-card-gpt-4o'));
    fireEvent.click(screen.getByTestId('models-view-table'));
    expect(screen.getByTestId('models-table')).toBeDefined();
    expect(screen.getByTestId('model-row-deepseek-chat').textContent).toContain(
      '$0.27',
    );
    expect(screen.getAllByTestId(/^model-row-/)).toHaveLength(3);
  });

  it('opens a detail drawer with prices and a quick start naming the model', async () => {
    serve(UAT);
    render(<HFModels />);
    await waitFor(() =>
      expect(
        screen.getByTestId('model-card-deepseek-chat').textContent,
      ).toContain('$0.27'),
    );
    fireEvent.click(screen.getByTestId('model-card-deepseek-chat'));
    const drawer = screen.getByTestId('model-drawer');
    expect(screen.getByTestId('model-drawer-prices').textContent).toContain(
      '$1.08',
    );
    const code = screen.getByTestId('model-drawer-code').textContent;
    expect(code).toContain('"model": "deepseek-chat"');
    expect(code).toContain('https://hub.example.test/v1/chat/completions');
    expect(drawer.textContent).toContain('POST /v1/messages');

    fireEvent.click(screen.getByTestId('model-drawer-try'));
    expect(mockNavigate).toHaveBeenCalledWith(
      '/console/v2/playground?prefill_model=deepseek-chat',
    );
  });

  it('try on a callable card navigates with the prefill param; an uncallable one offers no try', async () => {
    serve(UAT);
    render(<HFModels />);
    await waitFor(() => screen.getByTestId('model-card-gpt-4o'));
    expect(screen.queryByTestId('model-try-gpt-4o')).toBeNull();
    fireEvent.click(screen.getByTestId('model-try-deepseek-chat'));
    expect(mockNavigate).toHaveBeenCalledTimes(1);
    expect(mockNavigate.mock.calls[0][0]).toContain(
      'prefill_model=deepseek-chat',
    );
    // The try button does not also open the drawer.
    expect(screen.queryByTestId('model-drawer')).toBeNull();
  });

  it('says there are no models only when every source is empty', async () => {
    serve({});
    render(<HFModels />);
    const empty = await waitFor(() => screen.getByTestId('models-empty'));
    expect(empty.textContent).toMatch(/No models yet/);
  });

  // Lock for the hook's error effect — the page's only failure surface for
  // a catalogue load that came back but reported failure.
  it('shows an error toast when the models hook reports success:false', async () => {
    serve({
      extra: (u) =>
        u.includes('/models?')
          ? Promise.resolve({
              data: { success: false, message: 'tenant not found' },
            })
          : null,
    });
    render(<HFModels />);
    await waitFor(() => {
      expect(showError).toHaveBeenCalledWith('tenant not found');
    });
  });

  it('carries no WIP banner — the add button is present and enabled', async () => {
    serve({});
    render(<HFModels />);
    await waitFor(() => {
      const addBtn = screen.getByTestId('models-add-btn');
      expect(addBtn.disabled).toBe(false);
    });
    expect(screen.queryByText(/deferred to v3/i, { exact: false })).toBeNull();
  });

  it('a non-root viewer never sees the admin availability link or fetches the allow-list', async () => {
    serve(UAT);
    render(<HFModels />);
    await waitFor(() => screen.getByTestId('model-card-deepseek-chat'));
    expect(screen.queryByTestId('models-manage-availability-link')).toBeNull();
    const urls = API.get.mock.calls.map((c) => String(c[0]));
    expect(urls.some((u) => u.includes('/model-allowlist'))).toBe(false);
    expect(urls.some((u) => u.includes('/user/me'))).toBe(false);
  });

  it('role >= 100 sees the admin availability link and it navigates to the admin page', async () => {
    isRoot.mockReturnValue(true);
    serve({
      ...UAT,
      extra: rootAllowlist({
        configured: false,
        allowed_models: [],
        mode: 'observe',
      }),
    });
    render(<HFModels />);
    await waitFor(() =>
      expect(
        screen.getByTestId('models-manage-availability-link'),
      ).toBeDefined(),
    );
    fireEvent.click(screen.getByTestId('models-manage-availability-link'));
    expect(mockNavigate).toHaveBeenCalledWith('/console/v2/admin/model-limits');
  });

  it('a non-root fetch failure (stale client-side role) shows no availability badge, not a crash', async () => {
    isRoot.mockReturnValue(true);
    serve({
      ...UAT,
      extra: (u) =>
        u.includes('/user/me') || u.includes('/model-allowlist')
          ? Promise.reject({ response: { status: 403 } })
          : null,
    });
    render(<HFModels />);
    await waitFor(() => screen.getByTestId('model-card-deepseek-chat'));
    expect(screen.queryByTestId('model-availability-deepseek-chat')).toBeNull();
  });

  // Both root-only enrichment fetches must pass skipErrorHandler so a
  // failure on this decorative badge never fires the global toast/401-heal.
  it('the allow-list enrichment fetches opt out of the global error handler', async () => {
    isRoot.mockReturnValue(true);
    serve({
      extra: rootAllowlist({
        configured: false,
        allowed_models: [],
        mode: 'observe',
      }),
    });
    render(<HFModels />);
    await waitFor(() => {
      expect(
        API.get.mock.calls.some(
          (c) => String(c[0]).includes('/user/me') && c[1]?.skipErrorHandler,
        ),
      ).toBe(true);
    });
    expect(
      API.get.mock.calls.some(
        (c) =>
          String(c[0]).includes('/model-allowlist') && c[1]?.skipErrorHandler,
      ),
    ).toBe(true);
  });

  it('a malformed isRoot() throw degrades to non-root instead of crashing the page', async () => {
    isRoot.mockImplementation(() => {
      throw new SyntaxError('Unexpected token in JSON');
    });
    serve(UAT);
    render(<HFModels />);
    await waitFor(() => screen.getByTestId('model-card-deepseek-chat'));
    expect(screen.queryByTestId('models-manage-availability-link')).toBeNull();
    expect(screen.queryByTestId('model-availability-deepseek-chat')).toBeNull();
  });

  // An observe-mode "not on the list" model must never render like an
  // enforce-mode blocked one — observe still answers, only counted.
  it('renders observe-mode "off the list" distinctly from enforce-mode "blocked"', async () => {
    isRoot.mockReturnValue(true);
    const list = (mode) => ({
      configured: true,
      allowed_models: ['claude-3-5-sonnet'],
      mode,
    });
    serve({ ...UAT, extra: rootAllowlist(list('observe')) });
    const { unmount } = render(<HFModels />);
    const observedEl = await waitFor(() =>
      screen.getByTestId('model-availability-deepseek-chat'),
    );
    expect(observedEl.dataset.availability).toBe('observed');
    expect(observedEl.textContent).toContain('still answers');
    unmount();

    serve({ ...UAT, extra: rootAllowlist(list('enforce')) });
    render(<HFModels />);
    const blockedEl = await waitFor(() =>
      screen.getByTestId('model-availability-deepseek-chat'),
    );
    expect(blockedEl.dataset.availability).toBe('blocked');
    expect(blockedEl.textContent).not.toContain('still answers');
  });

  it('add model opens modal and posts', async () => {
    serve({});
    API.post.mockResolvedValueOnce({
      data: { success: true, data: { id: 42, model_name: 'my-model' } },
    });
    render(<HFModels />);
    await waitFor(() => expect(API.get).toHaveBeenCalled());
    fireEvent.click(screen.getByTestId('models-add-btn'));
    expect(screen.getByTestId('models-add-dialog')).toBeDefined();
    fireEvent.change(screen.getByTestId('add-model-name'), {
      target: { value: 'my-model' },
    });
    fireEvent.click(screen.getByTestId('add-model-submit'));
    await waitFor(() => expect(API.post).toHaveBeenCalledTimes(1));
    const [url, body] = API.post.mock.calls[0];
    expect(url).toContain('/api/v2/~/models');
    expect(body.model_name).toBe('my-model');
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
