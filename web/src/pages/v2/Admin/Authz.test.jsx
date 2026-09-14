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

vi.mock('../../../helpers', () => ({
  API: { get: vi.fn(), post: vi.fn(), delete: vi.fn() },
  showError: vi.fn(),
  showSuccess: vi.fn(),
}));

vi.mock('../../../components/hifi/HFShell', () => ({
  default: ({ children }) =>
    React.createElement('div', { 'data-testid': 'hf-shell' }, children),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key, fallback) => (typeof fallback === 'string' ? fallback : key),
  }),
  initReactI18next: { type: '3rdParty', init: () => {} },
}));

import V2AdminAuthz from './Authz';
import { API, showSuccess } from '../../../helpers';

const catalogResponse = () => ({
  data: {
    success: true,
    data: {
      resources: [{ resource: 'audit', actions: ['read'] }],
      roles: [
        { name: 'tenant-admin', min_role: 10 },
        { name: 'root', min_role: 100 },
      ],
    },
  },
});

const grantsResponse = (grants) => ({
  data: { success: true, data: grants },
});

beforeEach(() => {
  API.get.mockReset();
  API.post.mockReset();
  API.delete.mockReset();
});

describe('Admin permission grants page', () => {
  it('lists an active grant and a revoked one with distinct status badges', async () => {
    API.get.mockImplementation((url) => {
      if (url.includes('/catalog')) return Promise.resolve(catalogResponse());
      return Promise.resolve(
        grantsResponse([
          {
            id: 1,
            user_id: 42,
            resource: 'audit',
            action: 'read',
            created_at: 1700000000,
            revoked_at: null,
          },
          {
            id: 2,
            user_id: 43,
            resource: 'audit',
            action: 'read',
            created_at: 1700000000,
            revoked_at: 1700000100,
          },
        ]),
      );
    });

    render(<V2AdminAuthz />);

    await waitFor(() => screen.getByTestId('authz-row-1'));
    expect(screen.getByTestId('authz-status-1').textContent).toBe('active');
    expect(screen.getByTestId('authz-status-2').textContent).toBe('revoked');
    // Only the active grant gets a revoke button.
    expect(screen.getByTestId('authz-revoke-1')).toBeTruthy();
    expect(screen.queryByTestId('authz-revoke-2')).toBeNull();
  });

  it('submits a new grant with tenant_id null and refreshes the list', async () => {
    API.get.mockImplementation((url) => {
      if (url.includes('/catalog')) return Promise.resolve(catalogResponse());
      return Promise.resolve(grantsResponse([]));
    });
    API.post.mockResolvedValue({ data: { success: true, data: { id: 9 } } });

    render(<V2AdminAuthz />);

    await waitFor(() => screen.getByTestId('authz-empty'));

    fireEvent.change(screen.getByTestId('authz-input-user-id'), {
      target: { value: '42' },
    });
    fireEvent.change(screen.getByTestId('authz-select-resource'), {
      target: { value: 'audit' },
    });
    fireEvent.change(screen.getByTestId('authz-select-action'), {
      target: { value: 'read' },
    });
    fireEvent.click(screen.getByTestId('authz-submit'));

    await waitFor(() => expect(API.post).toHaveBeenCalledTimes(1));
    expect(API.post).toHaveBeenCalledWith('/api/v2/admin/authz/grants', {
      user_id: 42,
      resource: 'audit',
      action: 'read',
      tenant_id: null,
    });
    expect(showSuccess).toHaveBeenCalled();
    // Refetch after create: catalog + grants called twice each (mount + refresh).
    await waitFor(() =>
      expect(
        API.get.mock.calls.filter((c) => c[0].includes('/grants')).length,
      ).toBe(2),
    );
  });

  it('revokes a grant', async () => {
    API.get.mockImplementation((url) => {
      if (url.includes('/catalog')) return Promise.resolve(catalogResponse());
      return Promise.resolve(
        grantsResponse([
          {
            id: 1,
            user_id: 42,
            resource: 'audit',
            action: 'read',
            created_at: 1700000000,
            revoked_at: null,
          },
        ]),
      );
    });
    API.delete.mockResolvedValue({ data: { success: true } });

    render(<V2AdminAuthz />);

    await waitFor(() => screen.getByTestId('authz-revoke-1'));
    fireEvent.click(screen.getByTestId('authz-revoke-1'));

    await waitFor(() =>
      expect(API.delete).toHaveBeenCalledWith('/api/v2/admin/authz/grants/1'),
    );
    expect(showSuccess).toHaveBeenCalled();
  });

  it('shows a permission notice on 403', async () => {
    API.get.mockRejectedValue({ response: { status: 403 } });

    render(<V2AdminAuthz />);

    await waitFor(() =>
      screen.getByText(/Only root can manage delegated permission grants/),
    );
    expect(screen.queryByTestId('authz-create-form')).toBeNull();
  });

  // A-F8 (cycle-8 L4 repair round): grant MANAGEMENT is root-only server-side
  // via RootJWTAuth (adminRoute) — a non-root admin's session-path rejection
  // is HTTP 200 {success:false,...}, not a thrown 403 (auth.go's
  // roleVal<minRole branch, verified verbatim in the M2 mutation output for
  // this same handler group). Without also treating success:false as
  // forbidden, a direct visit by a non-root admin showed the create form and
  // "No grants issued yet." instead of the notice — the test above alone
  // never caught this because axios resolves (not rejects) a 200 response.
  it('shows a permission notice when the response resolves with success:false (real RootJWTAuth session-path shape)', async () => {
    API.get.mockResolvedValue({
      data: { success: false, message: '无权进行此操作，权限不足' },
    });

    render(<V2AdminAuthz />);

    await waitFor(() =>
      screen.getByText(/Only root can manage delegated permission grants/),
    );
    expect(screen.queryByTestId('authz-create-form')).toBeNull();
  });
});
