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

// Branch the shared API.get mock by URL: the page fetches the tenant list AND
// the selected tenant's model limits from the same instance.
const wireGet = (rows) => {
  API.get.mockImplementation((url) => {
    if (String(url).includes('/model-limits')) {
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
