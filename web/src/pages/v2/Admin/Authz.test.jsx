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

  // Cycle-9 L1: an expired-but-unrevoked row is visually distinct from
  // both an active row and a revoked row, and it still offers a revoke
  // button (revoked_at is genuinely still null server-side).
  it('marks an expired grant distinctly from active and revoked, and shows its expires_at', async () => {
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
            expires_at: 1700003600,
            expired: false,
            revoked_at: null,
          },
          {
            id: 2,
            user_id: 43,
            resource: 'audit',
            action: 'read',
            created_at: 1700000000,
            expires_at: 1700000100,
            expired: true,
            revoked_at: null,
          },
          {
            id: 3,
            user_id: 44,
            resource: 'audit',
            action: 'read',
            created_at: 1700000000,
            expires_at: null,
            expired: false,
            revoked_at: 1700000200,
          },
        ]),
      );
    });

    render(<V2AdminAuthz />);

    await waitFor(() => screen.getByTestId('authz-row-1'));
    expect(screen.getByTestId('authz-status-1').textContent).toBe('active');
    expect(screen.getByTestId('authz-status-2').textContent).toBe('expired');
    expect(screen.getByTestId('authz-status-3').textContent).toBe('revoked');

    // The expired row still shows a revoke button (revoked_at is still
    // null); the already-revoked row does not.
    expect(screen.getByTestId('authz-revoke-2')).toBeTruthy();
    expect(screen.queryByTestId('authz-revoke-3')).toBeNull();

    // Rows with an expires_at render it; the never-expiring row does not
    // show a raw null/undefined.
    expect(screen.getByTestId('authz-expires-1').textContent).not.toBe('');
    expect(screen.getByTestId('authz-expires-3').textContent).toBe('never');
  });

  // Cycle-9 L1: the create form's optional ttl input is sent as
  // ttl_seconds only when filled in — an empty ttl field must not send
  // ttl_seconds:0 or ttl_seconds:null, it must OMIT the key, so a grant
  // created without it behaves exactly as before this field existed.
  it('submits ttl_seconds only when the optional ttl field is filled in', async () => {
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
    fireEvent.change(screen.getByTestId('authz-input-ttl'), {
      target: { value: '3600' },
    });
    fireEvent.click(screen.getByTestId('authz-submit'));

    await waitFor(() => expect(API.post).toHaveBeenCalledTimes(1));
    expect(API.post).toHaveBeenCalledWith('/api/v2/admin/authz/grants', {
      user_id: 42,
      resource: 'audit',
      action: 'read',
      tenant_id: null,
      ttl_seconds: 3600,
    });
  });

  // R4 (cycle-9 L1 repair ruling, A-6/B-6): a parsed 0 must still be SENT
  // as ttl_seconds:0 — not silently reinterpreted as "field empty" into a
  // permanent grant — so the server's own 400 GRANT_INVALID is what the
  // operator sees, not a grant that never expires.
  it('sends ttl_seconds:0 (not omitted) when the ttl field holds 0', async () => {
    API.get.mockImplementation((url) => {
      if (url.includes('/catalog')) return Promise.resolve(catalogResponse());
      return Promise.resolve(grantsResponse([]));
    });
    API.post.mockRejectedValue({
      response: { status: 400, data: { error_code: 'GRANT_INVALID' } },
    });

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
    fireEvent.change(screen.getByTestId('authz-input-ttl'), {
      target: { value: '0' },
    });
    // fireEvent.submit on the form itself (rather than clicking the submit
    // button) dispatches the 'submit' event directly — the number input's
    // own min='1' native constraint validation only runs on the button-
    // activation path, so this exercises submitGrant's own ttl parsing in
    // isolation from that separate defense.
    fireEvent.submit(screen.getByTestId('authz-create-form'));

    await waitFor(() => expect(API.post).toHaveBeenCalledTimes(1));
    expect(API.post).toHaveBeenCalledWith('/api/v2/admin/authz/grants', {
      user_id: 42,
      resource: 'audit',
      action: 'read',
      tenant_id: null,
      ttl_seconds: 0,
    });
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
