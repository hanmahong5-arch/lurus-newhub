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

import InvitesDrawer, {
  inviteLink,
  inviteStatusLabel,
  isRevocable,
} from './InvitesDrawer';

vi.mock('../../../helpers', () => {
  const API = {
    get: vi.fn(),
    post: vi.fn(),
    delete: vi.fn(),
  };
  return {
    API,
    showError: vi.fn(),
    showSuccess: vi.fn(),
  };
});

import { API, showError, showSuccess } from '../../../helpers';

const okGet = (data) => Promise.resolve({ data: { success: true, data } });
const okPost = (data) => Promise.resolve({ data: { success: true, data } });
const httpErr = (status, body) =>
  Promise.reject({ response: { status, data: body } });

const PENDING = {
  id: 1,
  code_prefix: 'abcd1234',
  status: 1,
  expired_time: 0,
  consumed_by_account_id: null,
  created_by_user_id: 7,
  created_at: '2026-09-19T00:00:00Z',
};

const CONSUMED = {
  id: 2,
  code_prefix: 'ffff9999',
  status: 2,
  expired_time: 1999999999,
  consumed_by_account_id: 4242,
  created_by_user_id: 7,
  created_at: '2026-09-18T00:00:00Z',
};

beforeEach(() => {
  API.get.mockReset();
  API.post.mockReset();
  API.delete.mockReset();
  showError.mockReset();
  showSuccess.mockReset();
  Object.defineProperty(window, 'location', {
    configurable: true,
    writable: true,
    value: { origin: 'https://hub.test' },
  });
  Object.assign(navigator, {
    clipboard: { writeText: vi.fn(() => Promise.resolve()) },
  });
});

// ─── Pure helpers (no DOM) ──────────────────────────────────────────────────

describe('InvitesDrawer pure helpers', () => {
  it('inviteLink builds the /login?invite= delivery URL', () => {
    expect(inviteLink('https://hub.test', 'abc123')).toBe(
      'https://hub.test/login?invite=abc123',
    );
  });

  it('inviteLink percent-encodes special characters in the code', () => {
    expect(inviteLink('https://hub.test', 'a b/c')).toBe(
      'https://hub.test/login?invite=' + encodeURIComponent('a b/c'),
    );
  });

  it('inviteStatusLabel maps the three known statuses plus unknown', () => {
    expect(inviteStatusLabel(1)).toBe('pending');
    expect(inviteStatusLabel(2)).toBe('consumed');
    expect(inviteStatusLabel(3)).toBe('revoked');
    expect(inviteStatusLabel(99)).toBe('unknown');
  });

  it('isRevocable is true only for pending invites', () => {
    expect(isRevocable(PENDING)).toBe(true);
    expect(isRevocable(CONSUMED)).toBe(false);
    expect(isRevocable(null)).toBe(false);
  });
});

// ─── Drawer render flows ────────────────────────────────────────────────────

describe('InvitesDrawer render flows', () => {
  it('renders the empty state when the list has no invites', async () => {
    API.get.mockImplementationOnce(() => okGet({ invites: [] }));

    render(
      <InvitesDrawer tenantId='t-acme' tenantName='Acme' onClose={() => {}} />,
    );

    await waitFor(() =>
      expect(screen.getByText(/no invites issued yet/i)).toBeInTheDocument(),
    );
    expect(API.get).toHaveBeenCalledWith(
      '/api/v2/admin/tenants/t-acme/invites',
    );
  });

  it('renders rows from code_prefix — never the full code', async () => {
    API.get.mockImplementationOnce(() =>
      okGet({ invites: [PENDING, CONSUMED] }),
    );

    render(
      <InvitesDrawer tenantId='t-acme' tenantName='Acme' onClose={() => {}} />,
    );

    await waitFor(() =>
      expect(screen.getByTestId('invite-row-1')).toBeInTheDocument(),
    );
    expect(screen.getByTestId('invite-row-1')).toHaveTextContent('abcd1234');
    expect(screen.getByTestId('invite-row-2')).toHaveTextContent('ffff9999');
    // Only the pending row offers revoke.
    expect(screen.getByTestId('invite-revoke-btn-1')).toBeInTheDocument();
    expect(screen.queryByTestId('invite-revoke-btn-2')).not.toBeInTheDocument();
  });

  it('issuing an invite shows the one-time link built from origin + code', async () => {
    API.get
      .mockImplementationOnce(() => okGet({ invites: [] }))
      .mockImplementationOnce(() => okGet({ invites: [PENDING] }));
    API.post.mockImplementationOnce(() =>
      okPost({ id: 1, code: 'brandnewcode1234567890abcd', status: 1 }),
    );

    render(
      <InvitesDrawer tenantId='t-acme' tenantName='Acme' onClose={() => {}} />,
    );
    await waitFor(() =>
      expect(screen.getByTestId('invite-issue-submit')).not.toBeDisabled(),
    );

    fireEvent.change(screen.getByTestId('invite-ttl-hours'), {
      target: { value: '24' },
    });
    fireEvent.submit(screen.getByTestId('invite-issue-form'));

    await waitFor(() =>
      expect(screen.getByTestId('invite-issued-link')).toBeInTheDocument(),
    );
    expect(screen.getByTestId('invite-issued-link').value).toBe(
      'https://hub.test/login?invite=brandnewcode1234567890abcd',
    );
    expect(API.post).toHaveBeenCalledWith(
      '/api/v2/admin/tenants/t-acme/invites',
      { ttl_hours: 24 },
    );
    // Never displays the plaintext code by itself outside the link.
    expect(screen.getByText(/shown once/i)).toBeInTheDocument();
  });

  it('the copy button copies the exact issued link', async () => {
    API.get
      .mockImplementationOnce(() => okGet({ invites: [] }))
      .mockImplementationOnce(() => okGet({ invites: [] }));
    API.post.mockImplementationOnce(() =>
      okPost({ id: 5, code: 'copycode1234567890abcd12345678', status: 1 }),
    );

    render(
      <InvitesDrawer tenantId='t-acme' tenantName='Acme' onClose={() => {}} />,
    );
    await waitFor(() =>
      expect(screen.getByTestId('invite-issue-submit')).not.toBeDisabled(),
    );
    fireEvent.submit(screen.getByTestId('invite-issue-form'));
    await waitFor(() =>
      expect(screen.getByTestId('invite-issued-copy')).toBeInTheDocument(),
    );

    fireEvent.click(screen.getByTestId('invite-issued-copy'));

    await waitFor(() =>
      expect(navigator.clipboard.writeText).toHaveBeenCalledWith(
        'https://hub.test/login?invite=copycode1234567890abcd12345678',
      ),
    );
  });

  it('revoke issues a DELETE then refetches the list', async () => {
    API.get
      .mockImplementationOnce(() => okGet({ invites: [PENDING] }))
      .mockImplementationOnce(() => okGet({ invites: [] }));
    API.delete.mockImplementationOnce(() =>
      Promise.resolve({ data: { success: true } }),
    );

    render(
      <InvitesDrawer tenantId='t-acme' tenantName='Acme' onClose={() => {}} />,
    );
    await waitFor(() =>
      expect(screen.getByTestId('invite-revoke-btn-1')).toBeInTheDocument(),
    );

    fireEvent.click(screen.getByTestId('invite-revoke-btn-1'));

    await waitFor(() =>
      expect(API.delete).toHaveBeenCalledWith(
        '/api/v2/admin/tenants/t-acme/invites/1',
      ),
    );
    // Refetch happened — second GET call, list now empty.
    await waitFor(() => expect(API.get).toHaveBeenCalledTimes(2));
  });

  it('a load failure renders a distinct message, not the empty state', async () => {
    API.get.mockImplementationOnce(() =>
      Promise.reject({ response: { status: 500 } }),
    );

    render(
      <InvitesDrawer tenantId='t-acme' tenantName='Acme' onClose={() => {}} />,
    );

    await waitFor(() =>
      expect(screen.getByTestId('invite-list-load-failed')).toBeInTheDocument(),
    );
    expect(
      screen.queryByText(/no invites issued yet/i),
    ).not.toBeInTheDocument();
  });

  it('a success:false list response also renders the load-failed message', async () => {
    API.get.mockImplementationOnce(() =>
      Promise.resolve({ data: { success: false, message: 'nope' } }),
    );

    render(
      <InvitesDrawer tenantId='t-acme' tenantName='Acme' onClose={() => {}} />,
    );

    await waitFor(() =>
      expect(screen.getByTestId('invite-list-load-failed')).toBeInTheDocument(),
    );
  });

  it('issuing with ttl_hours=0 sends an empty body (never-expires)', async () => {
    API.get
      .mockImplementationOnce(() => okGet({ invites: [] }))
      .mockImplementationOnce(() => okGet({ invites: [] }));
    API.post.mockImplementationOnce(() =>
      okPost({ id: 9, code: 'neverexpirescode1234567890ab', status: 1 }),
    );

    render(
      <InvitesDrawer tenantId='t-acme' tenantName='Acme' onClose={() => {}} />,
    );
    await waitFor(() =>
      expect(screen.getByTestId('invite-issue-submit')).not.toBeDisabled(),
    );

    fireEvent.change(screen.getByTestId('invite-ttl-hours'), {
      target: { value: '0' },
    });
    fireEvent.submit(screen.getByTestId('invite-issue-form'));

    await waitFor(() =>
      expect(API.post).toHaveBeenCalledWith(
        '/api/v2/admin/tenants/t-acme/invites',
        {},
      ),
    );
  });

  it('a failed issue shows an error toast and does not render a link box', async () => {
    API.get.mockImplementationOnce(() => okGet({ invites: [] }));
    API.post.mockImplementationOnce(() =>
      httpErr(500, { success: false, message: 'boom' }),
    );

    render(
      <InvitesDrawer tenantId='t-acme' tenantName='Acme' onClose={() => {}} />,
    );
    await waitFor(() =>
      expect(screen.getByTestId('invite-issue-submit')).not.toBeDisabled(),
    );
    fireEvent.submit(screen.getByTestId('invite-issue-form'));

    await waitFor(() => expect(showError).toHaveBeenCalled());
    expect(screen.queryByTestId('invite-issued-link')).not.toBeInTheDocument();
  });
});
