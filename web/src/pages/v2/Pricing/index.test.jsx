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
import { render, screen, waitFor, fireEvent } from '@testing-library/react';

// Mock helpers BEFORE importing the component.
vi.mock('../../../helpers', () => ({
  API: {
    get: vi.fn(),
    post: vi.fn(),
  },
  showError: vi.fn(),
  showSuccess: vi.fn(),
}));

// Stub HFShell — avoids react-router / TenantSwitcher chain.
vi.mock('../../../components/hifi/HFShell', () => ({
  default: ({ children, actions }) =>
    React.createElement(
      'div',
      { 'data-testid': 'hf-shell' },
      React.createElement(
        'div',
        { 'data-testid': 'hf-shell-actions' },
        actions,
      ),
      children,
    ),
}));

// Stub useFormDraft — use real implementation but provide storage mock via localStorage.
vi.mock('../../../hooks/common/useFormDraft', () => {
  const { useState } = require('react');
  return {
    default: (_key, initialValue) => {
      const [draft, setDraftState] = useState(initialValue);
      const isDirty =
        draft !== null &&
        typeof draft === 'object' &&
        Object.keys(draft).length > 0;
      const setDraft = (updater) => {
        setDraftState((prev) =>
          typeof updater === 'function' ? updater(prev) : updater,
        );
      };
      const clear = () => setDraftState(initialValue);
      return [draft, setDraft, clear, isDirty, false];
    },
  };
});

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
          out = out.split(`{{${k}}}`).join(String(v));
        }
      }
      return out;
    },
  }),
}));

import PricingPage from './index';
import { API, showError, showSuccess } from '../../../helpers';

const fakePricingResponse = (models, version = 5) => ({
  data: {
    success: true,
    data: {
      pricing: models,
      vendors: [...new Set(models.map((m) => m.vendor).filter(Boolean))],
      group_ratio: { default: 1.0, vip: 0.85 },
      version,
    },
  },
});

const THREE_MODELS = [
  {
    model_name: 'model-a',
    vendor: 'OpenAI',
    quota_type: 0,
    model_ratio: 2.5,
    completion_ratio: 1.0,
    model_price: 0,
    enable_groups: ['default', 'vip'],
    supported_endpoint_types: ['chat'],
  },
  {
    model_name: 'model-b',
    vendor: 'Anthropic',
    quota_type: 1,
    model_ratio: 0,
    model_price: 3.0,
    enable_groups: ['vip'],
    supported_endpoint_types: ['chat'],
  },
  {
    model_name: 'model-c',
    vendor: 'Google',
    quota_type: 0,
    model_ratio: 1.25,
    completion_ratio: 1.0,
    model_price: 0,
    enable_groups: ['default'],
    supported_endpoint_types: ['chat'],
  },
];

beforeEach(() => {
  API.get.mockReset();
  API.post.mockReset();
  showSuccess.mockReset?.();
  showError.mockReset?.();
  // Default: empty pricing so tests that don't exercise data still mount cleanly.
  API.get.mockResolvedValue(fakePricingResponse([]));
  window.localStorage.clear();
  window.localStorage.setItem('tenant_slug', 'acme');
});

describe('Pricing page', () => {
  // 1. Fetches pricing on mount — 3 model rows render in the table.
  it('fetches pricing on mount', async () => {
    API.get.mockResolvedValue(fakePricingResponse(THREE_MODELS));

    render(<PricingPage />);

    await waitFor(() => {
      expect(API.get).toHaveBeenCalledWith('/api/v2/acme/pricing');
    });

    await waitFor(() => {
      expect(screen.getByTestId('pricing-table').textContent).toContain(
        'model-a',
      );
      expect(screen.getByTestId('pricing-table').textContent).toContain(
        'model-b',
      );
      expect(screen.getByTestId('pricing-table').textContent).toContain(
        'model-c',
      );
    });
  });

  // 2. Save button is disabled when there are no unsaved edits.
  it('disables save button when no edits pending', async () => {
    API.get.mockResolvedValue(fakePricingResponse([]));
    render(<PricingPage />);

    const saveBtn = screen.getByTestId('pricing-save');
    expect(saveBtn).toBeDisabled();
  });

  // 3. Save button becomes enabled after editing a field, then posts (with
  // the If-Match-Pricing-Version header from the last GET) and refreshes.
  it('save changes posts with the version header and refreshes list', async () => {
    API.get.mockResolvedValue(fakePricingResponse(THREE_MODELS, 5));
    API.post.mockResolvedValue({
      data: { success: true, data: { updated_count: 1, new_version: 6 } },
    });

    render(<PricingPage />);

    // Wait for the table to render with model data.
    await waitFor(() => {
      expect(screen.getByTestId('pricing-table').textContent).toContain(
        'model-a',
      );
    });

    // Edit model_ratio for model-a (quota_type 0 → ratio input is visible).
    const ratioField = screen.getByTestId('field-model_ratio-model-a');
    fireEvent.change(ratioField, { target: { value: '3.0' } });

    // Save button must now be enabled.
    const saveBtn = screen.getByTestId('pricing-save');
    await waitFor(() => expect(saveBtn).not.toBeDisabled());

    // Click save.
    fireEvent.click(saveBtn);

    // POST should be called with the changed row AND the version header
    // read from the last GET response (data.version === 5).
    await waitFor(() => {
      expect(API.post).toHaveBeenCalledWith(
        '/api/v2/acme/pricing',
        expect.arrayContaining([
          expect.objectContaining({ model_name: 'model-a', model_ratio: 3.0 }),
        ]),
        { headers: { 'If-Match-Pricing-Version': '5' } },
      );
    });

    // After successful POST, GET should be called again to refresh the list.
    // useTenantSlug triggers 2 fetches on mount (default→acme); the save adds 1 more.
    await waitFor(() => {
      // Mount (1) + the post-save refresh (2). Previously 3, the extra one
      // being the mount's placeholder-slug fetch.
      expect(API.get).toHaveBeenCalledTimes(2);
    });
  });

  // 4. A 409 PRICING_VERSION_CONFLICT shows the conflict toast and refetches
  // instead of treating the save as successful.
  it('shows the version-conflict toast and refetches on 409', async () => {
    API.get.mockResolvedValue(fakePricingResponse(THREE_MODELS, 5));
    API.post.mockRejectedValue({
      response: {
        status: 409,
        data: {
          success: false,
          error_code: 'PRICING_VERSION_CONFLICT',
          current_version: 6,
        },
      },
    });

    render(<PricingPage />);

    await waitFor(() => {
      expect(screen.getByTestId('pricing-table').textContent).toContain(
        'model-a',
      );
    });

    const ratioField = screen.getByTestId('field-model_ratio-model-a');
    fireEvent.change(ratioField, { target: { value: '3.0' } });

    const saveBtn = screen.getByTestId('pricing-save');
    await waitFor(() => expect(saveBtn).not.toBeDisabled());
    fireEvent.click(saveBtn);

    await waitFor(() => {
      expect(showError).toHaveBeenCalledWith(
        'Pricing changed since you last loaded it — refreshing',
      );
    });

    // The conflict path refetches (mount x2 + the post-conflict refresh).
    await waitFor(() => {
      expect(API.get).toHaveBeenCalledTimes(2);
    });
  });

  // 5. Preview renders one diff row per changed field and never calls save's
  // route — the diff table is populated straight from the preview response.
  it('preview renders a diff row per changed field', async () => {
    API.get.mockResolvedValue(fakePricingResponse(THREE_MODELS, 5));
    API.post.mockResolvedValue({
      data: {
        success: true,
        data: {
          version: 5,
          updated_count: 2,
          diffs: [
            {
              model_name: 'model-a',
              field: 'model_ratio',
              old: 2.5,
              new: 3.0,
            },
            {
              model_name: 'model-b',
              field: 'model_price',
              old: 3.0,
              new: 4.0,
            },
          ],
        },
      },
    });

    render(<PricingPage />);

    await waitFor(() => {
      expect(screen.getByTestId('pricing-table').textContent).toContain(
        'model-a',
      );
    });

    fireEvent.change(screen.getByTestId('field-model_ratio-model-a'), {
      target: { value: '3.0' },
    });
    fireEvent.change(screen.getByTestId('field-model_price-model-b'), {
      target: { value: '4.0' },
    });

    const previewBtn = screen.getByTestId('pricing-preview');
    await waitFor(() => expect(previewBtn).not.toBeDisabled());
    fireEvent.click(previewBtn);

    await waitFor(() => {
      expect(API.post).toHaveBeenCalledWith(
        '/api/v2/acme/pricing/preview',
        expect.arrayContaining([
          expect.objectContaining({ model_name: 'model-a' }),
          expect.objectContaining({ model_name: 'model-b' }),
        ]),
      );
    });

    await waitFor(() => {
      const diffTable = screen.getByTestId('pricing-preview-diff');
      expect(diffTable.textContent).toContain('model-a');
      expect(diffTable.textContent).toContain('model-b');
      expect(diffTable.textContent).toContain('model_ratio');
      expect(diffTable.textContent).toContain('model_price');
    });
  });

  // 6. The cache_ratio input prefills from the row's own value when GET
  // pricing projects one, instead of starting blank — a model with no
  // configured cache_ratio still starts blank.
  it('prefills the cache_ratio input from the row when GET returns one', async () => {
    API.get.mockResolvedValue(
      fakePricingResponse([
        { ...THREE_MODELS[0], cache_ratio: 0.42 },
        THREE_MODELS[1],
      ]),
    );

    render(<PricingPage />);

    await waitFor(() => {
      expect(screen.getByTestId('pricing-table').textContent).toContain(
        'model-a',
      );
    });

    expect(screen.getByTestId('field-cache_ratio-model-a').value).toBe('0.42');
    expect(screen.getByTestId('field-cache_ratio-model-b').value).toBe('');
  });

  // 7. Context-tiers editor: toggle badge shows the configured tier count,
  // hidden entirely for a per-call (quota_type=1) model, and the "add tier"
  // + field edits build a context_tiers entry that Save posts alongside the
  // flat ratio fields.
  it('context-tiers editor: toggle, add a tier, edit it, and save posts context_tiers', async () => {
    API.get.mockResolvedValue(
      fakePricingResponse(
        [
          {
            ...THREE_MODELS[0],
            context_tiers: [{ threshold_tokens: 0, model_ratio: 1 }],
          },
          THREE_MODELS[1], // quota_type: 1 — per-call model, no tier editor
        ],
        5,
      ),
    );
    API.post.mockResolvedValue({
      data: { success: true, data: { updated_count: 1, new_version: 6 } },
    });

    render(<PricingPage />);

    await waitFor(() => {
      expect(screen.getByTestId('pricing-table').textContent).toContain(
        'model-a',
      );
    });

    // The toggle button is rendered for the ratio-based model (the test
    // i18n mock does not interpolate the plural-count key's real text — see
    // the module's `model_count`/`toast_saved` calls, also untested for
    // their literal string here — so this checks presence, not the "1
    // tier" wording an English/Chinese build actually renders)...
    expect(
      screen.getByTestId('context-tiers-toggle-model-a'),
    ).toBeInTheDocument();
    // ...and is entirely absent for the per-call model.
    expect(
      screen.queryByTestId('context-tiers-toggle-model-b'),
    ).not.toBeInTheDocument();

    // Expand the editor and add a second tier.
    fireEvent.click(screen.getByTestId('context-tiers-toggle-model-a'));
    expect(
      screen.getByTestId('context-tiers-editor-model-a'),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByTestId('tier-add-model-a'));

    // Fill in the new (index 1) tier's threshold and model_ratio.
    fireEvent.change(screen.getByTestId('tier-threshold-model-a-1'), {
      target: { value: '3000' },
    });
    fireEvent.change(screen.getByTestId('tier-model_ratio-model-a-1'), {
      target: { value: '2' },
    });

    const saveBtn = screen.getByTestId('pricing-save');
    await waitFor(() => expect(saveBtn).not.toBeDisabled());
    fireEvent.click(saveBtn);

    await waitFor(() => {
      expect(API.post).toHaveBeenCalledWith(
        '/api/v2/acme/pricing',
        expect.arrayContaining([
          expect.objectContaining({
            model_name: 'model-a',
            context_tiers: [
              { threshold_tokens: 0, model_ratio: 1 },
              { threshold_tokens: 3000, model_ratio: 2 },
            ],
          }),
        ]),
        { headers: { 'If-Match-Pricing-Version': '5' } },
      );
    });
  });

  // 8. Removing the only tier sends an explicit empty context_tiers array
  // (the server's "clear this model's tiers" signal), not an omitted field.
  it('removing the last tier posts an explicit empty context_tiers array', async () => {
    API.get.mockResolvedValue(
      fakePricingResponse(
        [
          {
            ...THREE_MODELS[0],
            context_tiers: [{ threshold_tokens: 0, model_ratio: 1 }],
          },
        ],
        5,
      ),
    );
    API.post.mockResolvedValue({
      data: { success: true, data: { updated_count: 1, new_version: 6 } },
    });

    render(<PricingPage />);

    await waitFor(() => {
      expect(screen.getByTestId('pricing-table').textContent).toContain(
        'model-a',
      );
    });

    fireEvent.click(screen.getByTestId('context-tiers-toggle-model-a'));
    fireEvent.click(screen.getByTestId('tier-remove-model-a-0'));

    const saveBtn = screen.getByTestId('pricing-save');
    await waitFor(() => expect(saveBtn).not.toBeDisabled());
    fireEvent.click(saveBtn);

    await waitFor(() => {
      expect(API.post).toHaveBeenCalledWith(
        '/api/v2/acme/pricing',
        expect.arrayContaining([
          expect.objectContaining({
            model_name: 'model-a',
            context_tiers: [],
          }),
        ]),
        { headers: { 'If-Match-Pricing-Version': '5' } },
      );
    });
  });
});
