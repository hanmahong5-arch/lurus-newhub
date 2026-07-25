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
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';

vi.mock('../../../../helpers', () => ({
  API: { get: vi.fn() },
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

import HFAdminAudit from './index';
import { API, showError, showSuccess } from '../../../../helpers';

const makeEvent = (overrides = {}) => ({
  id: 41,
  tenant_id: 'default',
  timestamp: 1750000000,
  actor_type: 'admin',
  actor_id: 3,
  action: 'token.created',
  resource: 'token',
  resource_id: 9,
  ip: '10.0.0.7',
  request_id: 'req-abc',
  prev_hash: 'a'.repeat(64),
  row_hash: 'b'.repeat(64),
  details: '{"name":"ci-token"}',
  ...overrides,
});

const eventsResponse = (events, total) => ({
  data: { success: true, data: { events, total: total ?? events.length } },
});

const actionsResponse = (actions) => ({
  data: { success: true, data: { actions } },
});

const chainResponse = (result) => ({ data: { success: true, data: result } });

// Route each endpoint by URL so tests declare only what they care about.
const routeGet = ({ events, actions, chain }) => {
  API.get.mockImplementation((url) => {
    if (url.includes('/audit/actions')) {
      return Promise.resolve(actionsResponse(actions ?? ['token.created']));
    }
    if (url.includes('/audit/chain-verify')) {
      return Promise.resolve(chainResponse(chain ?? {}));
    }
    return Promise.resolve(eventsResponse(events ?? [makeEvent()]));
  });
};

beforeEach(() => {
  API.get.mockReset();
  showError.mockReset();
  showSuccess.mockReset();
});

describe('Admin Audit page', () => {
  it('renders audit events with actor, action and truncated row hash', async () => {
    routeGet({ events: [makeEvent()] });

    render(<HFAdminAudit />);

    await waitFor(() => screen.getByTestId('audit-row-41'));
    // Scope to the row: the action name also appears in the filter dropdown.
    const row = within(screen.getByTestId('audit-row-41'));
    expect(row.getByText('token.created')).toBeTruthy();
    expect(row.getByText('10.0.0.7')).toBeTruthy();

    // Hash is shown truncated but the full value stays available on hover.
    const hashCell = screen.getByTestId('audit-hash-41');
    expect(hashCell.textContent).toBe(`${'b'.repeat(10)}…`);
    expect(hashCell.getAttribute('title')).toBe('b'.repeat(64));
  });

  it('marks pre-chain rows as legacy rather than showing an empty hash', async () => {
    routeGet({ events: [makeEvent({ id: 42, row_hash: '', prev_hash: '' })] });

    render(<HFAdminAudit />);

    await waitFor(() => screen.getByTestId('audit-hash-42'));
    expect(screen.getByTestId('audit-hash-42').textContent).toBe('legacy');
  });

  it('expands a row to show pretty-printed details and request id', async () => {
    routeGet({ events: [makeEvent()] });

    render(<HFAdminAudit />);
    await waitFor(() => screen.getByTestId('audit-details-btn-41'));

    fireEvent.click(screen.getByTestId('audit-details-btn-41'));

    await waitFor(() => screen.getByTestId('audit-details-41'));
    const panel = screen.getByTestId('audit-details-41');
    expect(panel.textContent).toContain('ci-token');
    expect(panel.textContent).toContain('req-abc');
  });

  it('reports an intact chain without raising an error toast', async () => {
    routeGet({
      chain: {
        checked: 120,
        legacy_rows: 4,
        hash_breaks: 0,
        link_breaks: 0,
        first_break: null,
        first_link_break: null,
      },
    });

    render(<HFAdminAudit />);
    await waitFor(() => screen.getByTestId('audit-verify-btn'));

    fireEvent.click(screen.getByTestId('audit-verify-btn'));

    await waitFor(() => screen.getByTestId('audit-verify-result'));
    expect(screen.getByTestId('audit-verify-verdict').textContent).toBe(
      'intact',
    );
    expect(screen.getByText('120')).toBeTruthy();
    expect(showSuccess).toHaveBeenCalled();
    expect(showError).not.toHaveBeenCalled();
  });

  // The failure path is the one that matters: a break must be loud and must
  // name the offending row, otherwise the verifier is decorative.
  it('surfaces chain breaks with the first offending row id', async () => {
    routeGet({
      chain: {
        checked: 88,
        legacy_rows: 0,
        hash_breaks: 2,
        link_breaks: 1,
        first_break: {
          id: 57,
          expected: 'c'.repeat(64),
          actual: 'd'.repeat(64),
        },
        first_link_break: {
          id: 58,
          expected: 'e'.repeat(64),
          actual: 'f'.repeat(64),
        },
      },
    });

    render(<HFAdminAudit />);
    await waitFor(() => screen.getByTestId('audit-verify-btn'));

    fireEvent.click(screen.getByTestId('audit-verify-btn'));

    await waitFor(() => screen.getByTestId('audit-first-break'));
    expect(screen.getByTestId('audit-verify-verdict').textContent).toBe(
      'BREAKS FOUND',
    );
    expect(screen.getByTestId('audit-first-break').textContent).toContain(
      '#57',
    );
    expect(showError).toHaveBeenCalled();
  });

  it('sends the selected action filter to the events endpoint', async () => {
    routeGet({ actions: ['token.created', 'auth.failed'] });

    render(<HFAdminAudit />);
    await waitFor(() => screen.getByTestId('audit-action-filter'));

    fireEvent.change(screen.getByTestId('audit-action-filter'), {
      target: { value: 'auth.failed' },
    });

    await waitFor(() => {
      expect(API.get).toHaveBeenCalledWith(
        '/api/v2/admin/audit/events',
        expect.objectContaining({
          params: expect.objectContaining({ action: 'auth.failed' }),
        }),
      );
    });
  });

  it('carries active filters into the CSV export link', async () => {
    routeGet({ actions: ['auth.failed'] });

    render(<HFAdminAudit />);
    await waitFor(() => screen.getByTestId('audit-action-filter'));

    fireEvent.change(screen.getByTestId('audit-action-filter'), {
      target: { value: 'auth.failed' },
    });

    await waitFor(() => {
      const href = screen.getByTestId('audit-export-btn').getAttribute('href');
      expect(href).toContain('format=csv');
      expect(href).toContain('action=auth.failed');
    });
  });

  it('shows a permission notice instead of an empty table on 403', async () => {
    API.get.mockRejectedValue({ response: { status: 403 } });

    render(<HFAdminAudit />);

    // The heading and the panel both carry the title; assert on the panel's
    // explanatory body, which is unique.
    await waitFor(() =>
      screen.getByText(/You do not have permission to read the audit trail/),
    );
    expect(screen.queryByTestId('audit-verify-btn')).toBeNull();
    expect(screen.queryByTestId('audit-export-btn')).toBeNull();
  });
});
