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

vi.mock('../../../../helpers', () => ({
  API: {
    get: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
  showError: vi.fn(),
  showSuccess: vi.fn(),
}));

vi.mock('../../../../components/hifi/HFShell', () => ({
  default: ({ children, actions }) =>
    React.createElement(
      'div',
      { 'data-testid': 'hf-shell' },
      React.createElement('div', { 'data-testid': 'hf-actions' }, actions),
      children,
    ),
}));

vi.mock('../../../../components/common/ConfirmDialog', () => ({
  default: ({ visible, onConfirm, onCancel, title }) =>
    visible
      ? React.createElement(
          'div',
          { 'data-testid': 'confirm-dialog' },
          React.createElement('span', null, title),
          React.createElement(
            'button',
            { 'data-testid': 'confirm-ok', onClick: onConfirm },
            'ok',
          ),
          React.createElement(
            'button',
            { 'data-testid': 'confirm-cancel', onClick: onCancel },
            'cancel',
          ),
        )
      : null,
}));

import HFModelRateLimits from './index';
import { API } from '../../../../helpers';

const makeRow = (overrides = {}) => ({
  id: 3,
  tenant_id: 'default',
  model: 'gpt-4o',
  rate_limit_rpm: 60,
  rate_limit_tpm: 1000,
  updated_at: '2026-07-11T00:00:00Z',
  ...overrides,
});

const tenantsResponse = () => ({
  data: {
    success: true,
    data: { tenants: [{ id: 'default', name: 'default' }] },
  },
});

const limitsResponse = (rows) => ({
  data: { success: true, data: rows },
});

const allowlistResponse = (overrides = {}) => ({
  data: {
    success: true,
    data: {
      configured: false,
      allowed_models: [],
      mode: 'observe',
      ...overrides,
    },
  },
});

// Branch the shared API.get mock by URL: the page fetches the tenant list,
// the selected tenant's model limits, AND the selected tenant's model
// allow-list from the same instance.
const wireGet = (rows, allowlist) => {
  API.get.mockImplementation((url) => {
    const u = String(url);
    if (u.includes('/model-allowlist')) {
      return Promise.resolve(allowlistResponse(allowlist));
    }
    if (u.includes('/model-limits')) {
      return Promise.resolve(limitsResponse(rows));
    }
    return Promise.resolve(tenantsResponse());
  });
};

beforeEach(() => {
  API.get.mockReset();
  API.put.mockReset();
  API.delete.mockReset();
});

describe('Admin ModelRateLimits page', () => {
  it('renders the tenant model limits from GET', async () => {
    wireGet([makeRow()]);

    render(<HFModelRateLimits />);

    await waitFor(() => screen.getByTestId('mrl-row-3'));
    expect(screen.getByText('gpt-4o')).toBeTruthy();
    expect(screen.getByText('60')).toBeTruthy();
    expect(screen.getByText('1000')).toBeTruthy();
  });

  it('renders "unlimited" for a zero cap', async () => {
    wireGet([makeRow({ rate_limit_rpm: 0, rate_limit_tpm: 500 })]);

    render(<HFModelRateLimits />);

    await waitFor(() => screen.getByTestId('mrl-row-3'));
    expect(screen.getByText('unlimited')).toBeTruthy();
    expect(screen.getByText('500')).toBeTruthy();
  });

  it('creates a model limit and PUTs model/rpm/tpm', async () => {
    wireGet([]);
    API.put.mockResolvedValue({ data: { success: true, data: makeRow() } });

    render(<HFModelRateLimits />);
    await waitFor(() => screen.getByTestId('mrl-new-btn'));

    fireEvent.click(screen.getByTestId('mrl-new-btn'));
    await waitFor(() => screen.getByTestId('mrl-save'));

    fireEvent.change(screen.getByTestId('mrl-model'), {
      target: { value: 'claude-3-5-sonnet' },
    });
    fireEvent.change(screen.getByTestId('mrl-rpm'), {
      target: { value: '120' },
    });
    fireEvent.change(screen.getByTestId('mrl-tpm'), {
      target: { value: '4000' },
    });
    fireEvent.click(screen.getByTestId('mrl-save'));

    await waitFor(() => {
      expect(API.put).toHaveBeenCalledWith(
        '/api/v2/admin/tenants/default/model-limits',
        {
          model: 'claude-3-5-sonnet',
          rate_limit_rpm: 120,
          rate_limit_tpm: 4000,
        },
      );
    });
  });

  it('edits an existing row with the model field locked', async () => {
    wireGet([makeRow()]);
    API.put.mockResolvedValue({ data: { success: true, data: makeRow() } });

    render(<HFModelRateLimits />);
    await waitFor(() => screen.getByTestId('mrl-edit-btn-3'));

    fireEvent.click(screen.getByTestId('mrl-edit-btn-3'));
    await waitFor(() => screen.getByTestId('mrl-save'));

    // Model is the row key — not editable on edit.
    expect(screen.getByTestId('mrl-model').disabled).toBe(true);

    fireEvent.change(screen.getByTestId('mrl-rpm'), {
      target: { value: '90' },
    });
    fireEvent.click(screen.getByTestId('mrl-save'));

    await waitFor(() => {
      expect(API.put).toHaveBeenCalledWith(
        '/api/v2/admin/tenants/default/model-limits',
        expect.objectContaining({ model: 'gpt-4o', rate_limit_rpm: 90 }),
      );
    });
  });

  it('deletes a row behind the typed-confirm dialog', async () => {
    wireGet([makeRow()]);
    API.delete.mockResolvedValue({ data: { success: true } });

    render(<HFModelRateLimits />);
    await waitFor(() => screen.getByTestId('mrl-delete-btn-3'));

    fireEvent.click(screen.getByTestId('mrl-delete-btn-3'));
    await waitFor(() => screen.getByTestId('confirm-dialog'));
    fireEvent.click(screen.getByTestId('confirm-ok'));

    await waitFor(() => {
      expect(API.delete).toHaveBeenCalledWith(
        '/api/v2/admin/tenants/default/model-limits?model=gpt-4o',
      );
    });
  });

  it('shows the forbidden panel on 403', async () => {
    API.get.mockRejectedValue({ response: { status: 403 } });

    render(<HFModelRateLimits />);

    await waitFor(() => {
      expect(
        screen.getAllByText('Admin access required').length,
      ).toBeGreaterThan(0);
    });
  });
});

describe('Admin ModelRateLimits page — model availability', () => {
  it('shows the unrestricted state when no allow-list row is configured', async () => {
    wireGet([]);

    render(<HFModelRateLimits />);

    await waitFor(() => {
      expect(screen.getByTestId('mrl-availability-unrestricted')).toBeTruthy();
    });
    expect(screen.getByTestId('mrl-availability-mode').dataset.mode).toBe(
      'observe',
    );
  });

  // The oracle this cycle exists for: observe and enforce must never render
  // the same way. Observe still answers a model off the list (only
  // counted); enforce refuses it (403). A UI that renders them alike would
  // tell an observe-mode tenant its model is blocked when it is not.
  it('renders observe mode as "still answers" — distinct from enforce', async () => {
    wireGet([], {
      configured: true,
      allowed_models: ['gpt-4o'],
      mode: 'observe',
    });

    render(<HFModelRateLimits />);

    const modeEl = await waitFor(() =>
      screen.getByTestId('mrl-availability-mode'),
    );
    expect(modeEl.dataset.mode).toBe('observe');
    expect(modeEl.textContent).toContain('still answers');
    expect(modeEl.textContent).not.toContain('refused');
  });

  it('renders enforce mode as "refused" — distinct from observe', async () => {
    wireGet([], {
      configured: true,
      allowed_models: ['gpt-4o'],
      mode: 'enforce',
    });

    render(<HFModelRateLimits />);

    const modeEl = await waitFor(() =>
      screen.getByTestId('mrl-availability-mode'),
    );
    expect(modeEl.dataset.mode).toBe('enforce');
    expect(modeEl.textContent).toContain('refused');
    expect(modeEl.textContent).not.toContain('still answers');
  });

  it('renders the configured allow-list entries from GET', async () => {
    wireGet([], {
      configured: true,
      allowed_models: ['gpt-4o', 'claude-3-5-sonnet'],
      mode: 'observe',
    });

    render(<HFModelRateLimits />);

    await waitFor(() => {
      expect(screen.getByTestId('mrl-availability-entry-gpt-4o')).toBeTruthy();
      expect(
        screen.getByTestId('mrl-availability-entry-claude-3-5-sonnet'),
      ).toBeTruthy();
    });
  });

  it('adds a draft entry and PUTs exactly allowed_models on save', async () => {
    wireGet([], {
      configured: true,
      allowed_models: ['gpt-4o'],
      mode: 'observe',
    });
    API.put.mockResolvedValue({
      data: {
        success: true,
        data: {
          configured: true,
          allowed_models: ['gpt-4o', 'gpt-4o-mini'],
          mode: 'observe',
        },
      },
    });

    render(<HFModelRateLimits />);
    await waitFor(() => screen.getByTestId('mrl-availability-entry-gpt-4o'));

    fireEvent.change(screen.getByTestId('mrl-availability-add-input'), {
      target: { value: 'gpt-4o-mini' },
    });
    fireEvent.click(screen.getByTestId('mrl-availability-add-btn'));
    await waitFor(() =>
      screen.getByTestId('mrl-availability-entry-gpt-4o-mini'),
    );

    fireEvent.click(screen.getByTestId('mrl-availability-save'));

    // Asserts the exact payload shape the handler parses
    // (UpsertTenantModelAllowlist: `{ allowed_models: *[]string }`), not
    // just that PUT fired — a body of { allowedModels: [...] } or
    // { allowed_models: 'gpt-4o' } would bind-fail server-side.
    await waitFor(() => {
      expect(API.put).toHaveBeenCalledWith(
        '/api/v2/admin/tenants/default/model-allowlist',
        { allowed_models: ['gpt-4o', 'gpt-4o-mini'] },
      );
    });
  });

  it('removes a draft entry before saving', async () => {
    wireGet([], {
      configured: true,
      allowed_models: ['gpt-4o', 'claude-3-5-sonnet'],
      mode: 'observe',
    });
    API.put.mockResolvedValue({
      data: {
        success: true,
        data: {
          configured: true,
          allowed_models: ['claude-3-5-sonnet'],
          mode: 'observe',
        },
      },
    });

    render(<HFModelRateLimits />);
    await waitFor(() => screen.getByTestId('mrl-availability-entry-gpt-4o'));

    fireEvent.click(screen.getByTestId('mrl-availability-remove-gpt-4o'));
    fireEvent.click(screen.getByTestId('mrl-availability-save'));

    await waitFor(() => {
      expect(API.put).toHaveBeenCalledWith(
        '/api/v2/admin/tenants/default/model-allowlist',
        { allowed_models: ['claude-3-5-sonnet'] },
      );
    });
  });

  it('saving an empty list goes through the typed-confirm dialog before PUT (deny-all guard)', async () => {
    wireGet([], {
      configured: true,
      allowed_models: ['gpt-4o'],
      mode: 'observe',
    });
    API.put.mockResolvedValue({
      data: {
        success: true,
        data: { configured: true, allowed_models: [], mode: 'observe' },
      },
    });

    render(<HFModelRateLimits />);
    await waitFor(() => screen.getByTestId('mrl-availability-entry-gpt-4o'));

    fireEvent.click(screen.getByTestId('mrl-availability-remove-gpt-4o'));
    fireEvent.click(screen.getByTestId('mrl-availability-save'));

    // The PUT must NOT fire until the confirm dialog is accepted.
    expect(API.put).not.toHaveBeenCalled();
    await waitFor(() => screen.getByTestId('confirm-dialog'));

    fireEvent.click(screen.getByTestId('confirm-ok'));

    await waitFor(() => {
      expect(API.put).toHaveBeenCalledWith(
        '/api/v2/admin/tenants/default/model-allowlist',
        { allowed_models: [] },
      );
    });
  });

  it('clearing the allow-list goes through the typed-confirm dialog then DELETEs', async () => {
    wireGet([], {
      configured: true,
      allowed_models: ['gpt-4o'],
      mode: 'enforce',
    });
    API.delete.mockResolvedValue({
      data: { success: true, data: { configured: false, allowed_models: [] } },
    });

    render(<HFModelRateLimits />);
    await waitFor(() => screen.getByTestId('mrl-availability-clear'));

    fireEvent.click(screen.getByTestId('mrl-availability-clear'));
    expect(API.delete).not.toHaveBeenCalled();
    await waitFor(() => screen.getByTestId('confirm-dialog'));

    fireEvent.click(screen.getByTestId('confirm-ok'));

    await waitFor(() => {
      expect(API.delete).toHaveBeenCalledWith(
        '/api/v2/admin/tenants/default/model-allowlist',
      );
    });
  });

  it('the clear button is disabled when the tenant has no allow-list configured', async () => {
    wireGet([]);

    render(<HFModelRateLimits />);

    await waitFor(() => {
      expect(screen.getByTestId('mrl-availability-clear').disabled).toBe(true);
    });
  });
});
