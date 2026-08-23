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
import { API, showSuccess } from '../../../helpers';

const fakePricingResponse = (models) => ({
  data: {
    success: true,
    data: {
      pricing: models,
      vendors: [...new Set(models.map((m) => m.vendor).filter(Boolean))],
      group_ratio: { default: 1.0, vip: 0.85 },
    },
  },
});

const THREE_MODELS = [
  {
    model_name: 'gpt-4o',
    vendor: 'OpenAI',
    quota_type: 0,
    model_ratio: 2.5,
    completion_ratio: 1.0,
    model_price: 0,
    enable_groups: ['default', 'vip'],
    supported_endpoint_types: ['chat'],
  },
  {
    model_name: 'claude-3.5-sonnet',
    vendor: 'Anthropic',
    quota_type: 1,
    model_ratio: 0,
    model_price: 3.0,
    enable_groups: ['vip'],
    supported_endpoint_types: ['chat'],
  },
  {
    model_name: 'gemini-1.5-pro',
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
        'gpt-4o',
      );
      expect(screen.getByTestId('pricing-table').textContent).toContain(
        'claude-3.5-sonnet',
      );
      expect(screen.getByTestId('pricing-table').textContent).toContain(
        'gemini-1.5-pro',
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

  // 3. Save button becomes enabled after editing a field, then posts and refreshes.
  it('save changes posts and refreshes list', async () => {
    API.get.mockResolvedValue(fakePricingResponse(THREE_MODELS));
    API.post.mockResolvedValue({
      data: { success: true, data: { updated_count: 1 } },
    });

    render(<PricingPage />);

    // Wait for the table to render with model data.
    await waitFor(() => {
      expect(screen.getByTestId('pricing-table').textContent).toContain(
        'gpt-4o',
      );
    });

    // Edit model_ratio for gpt-4o (quota_type 0 → ratio input is visible).
    const ratioField = screen.getByTestId('field-model_ratio-gpt-4o');
    fireEvent.change(ratioField, { target: { value: '3.0' } });

    // Save button must now be enabled.
    const saveBtn = screen.getByTestId('pricing-save');
    await waitFor(() => expect(saveBtn).not.toBeDisabled());

    // Click save.
    fireEvent.click(saveBtn);

    // POST should be called with the changed row.
    await waitFor(() => {
      expect(API.post).toHaveBeenCalledWith(
        '/api/v2/acme/pricing',
        expect.arrayContaining([
          expect.objectContaining({ model_name: 'gpt-4o', model_ratio: 3.0 }),
        ]),
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
});
