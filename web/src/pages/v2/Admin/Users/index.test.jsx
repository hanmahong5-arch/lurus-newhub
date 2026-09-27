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
import { describe, it, expect, vi, beforeAll, beforeEach } from 'vitest';
import {
  configure,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react';

// The page now lazily mounts SecureVerificationModal (force-disable 2FA
// step-up) — its Semi UI import chain pulls in lottie, which throws on
// jsdom's canvas stub-less getContext(). vi.hoisted runs before the imports
// below, early enough for lottie's own top-level code to find a context.
// Same stub as k3_english_render.test.jsx (OpenRouterSync) — a mock cannot
// tell us what the real modal renders, so this stubs the canvas, not Semi.
vi.hoisted(() => {
  const ctx = new Proxy(
    {},
    {
      get: (target, prop) =>
        prop in target ? target[prop] : () => ({ data: [] }),
      set: (target, prop, value) => ((target[prop] = value), true),
    },
  );
  HTMLCanvasElement.prototype.getContext = () => ctx;
});

// The 2FA step-up dialog's Semi UI import chain makes this the heaviest
// page test in the suite (see the vi.hoisted note above). Under a loaded
// full-suite run the default 1s waitFor window expired on two different
// tests here (2026-09-20; both green in isolation at ~2s), so the window is
// widened for this file only. It widens patience, not any assertion.
configure({ asyncUtilTimeout: 5000 });

vi.mock('../../../../helpers', () => ({
  API: {
    get: vi.fn(),
    post: vi.fn(),
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

import HFAdminUsers from './index';
import { API } from '../../../../helpers';

const makeUser = (overrides = {}) => ({
  id: 7,
  username: 'alice',
  display_name: 'Alice',
  email: 'alice@example.com',
  role: 1,
  status: 1,
  group: 'default',
  quota: 500000,
  used_quota: 250000,
  request_count: 5,
  ...overrides,
});

const listResponse = (users) => ({
  data: {
    success: true,
    data: { users, total: users.length, page: 1, page_size: 50 },
  },
});

// The page lazy-loads the 2FA step-up modal, and its first import (Semi UI
// plus lottie) is the slow part of this file. Paid inside whichever test
// first opened a dialog, it made "edits a user" time out at 5s under a
// loaded CI run while passing alone. Load it once here, with its own budget,
// so the tests time what the page does, not how long a module takes to load.
beforeAll(
  () => import('../../../../components/common/modals/SecureVerificationModal'),
  60_000,
);

beforeEach(() => {
  API.get.mockReset();
  API.post.mockReset();
  API.put.mockReset();
  API.delete.mockReset();
});

describe('Admin Users page', () => {
  it('renders users from GET with no secret columns', async () => {
    API.get.mockResolvedValue(listResponse([makeUser()]));

    render(<HFAdminUsers />);

    await waitFor(() => screen.getByText('Alice'));
    expect(screen.getByText('alice@example.com')).toBeTruthy();

    // The table must not expose any secret column.
    expect(screen.queryByText(/access.?token/i)).toBeNull();
    expect(screen.queryByText(/password/i)).toBeNull();
    expect(screen.queryByText(/secret/i)).toBeNull();
  });

  it('edits a user and PUTs role/status/quota/group', async () => {
    API.get.mockResolvedValue(listResponse([makeUser()]));
    API.put.mockResolvedValue({ data: { success: true, data: makeUser() } });

    render(<HFAdminUsers />);
    await waitFor(() => screen.getByTestId('user-edit-btn-7'));

    fireEvent.click(screen.getByTestId('user-edit-btn-7'));
    await waitFor(() => screen.getByTestId('edit-save'));

    // Change quota cap to $3 and save.
    fireEvent.change(screen.getByTestId('edit-quota'), {
      target: { value: '3' },
    });
    fireEvent.click(screen.getByTestId('edit-save'));

    await waitFor(() => {
      expect(API.put).toHaveBeenCalledWith(
        '/api/v2/admin/users/7',
        expect.objectContaining({
          role: 1,
          status: 1,
          quota: 1500000, // 3 * 500_000
          group: 'default',
        }),
      );
    });
  });

  it('the edit dialog is a labelled modal; Escape closes it and focus returns to the edit button', async () => {
    API.get.mockResolvedValue(listResponse([makeUser()]));

    render(<HFAdminUsers />);
    await waitFor(() => screen.getByTestId('user-edit-btn-7'));

    const opener = screen.getByTestId('user-edit-btn-7');
    opener.focus();
    fireEvent.click(opener);
    await waitFor(() => screen.getByTestId('edit-save'));

    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveAttribute('aria-modal', 'true');
    expect(dialog).toHaveAccessibleName('Edit · alice');
    expect(dialog.contains(screen.getByTestId('edit-role'))).toBe(true);

    fireEvent.keyDown(document.body, { key: 'Escape' });
    await waitFor(() => {
      expect(screen.queryByTestId('edit-save')).toBeNull();
    });
    expect(document.activeElement).toBe(opener);
  });

  it('deletes a user behind the typed-confirm dialog', async () => {
    API.get.mockResolvedValue(listResponse([makeUser()]));
    API.delete.mockResolvedValue({ data: { success: true } });

    render(<HFAdminUsers />);
    await waitFor(() => screen.getByTestId('user-delete-btn-7'));

    fireEvent.click(screen.getByTestId('user-delete-btn-7'));
    await waitFor(() => screen.getByTestId('confirm-dialog'));
    fireEvent.click(screen.getByTestId('confirm-ok'));

    await waitFor(() => {
      expect(API.delete).toHaveBeenCalledWith('/api/v2/admin/users/7');
    });
  });

  it("force-disables a user's 2FA behind a reason prompt and the acting root's own step-up", async () => {
    API.get.mockImplementation((url) => {
      if (url === '/api/verify/status') {
        return Promise.resolve({
          data: { success: true, data: { totp_enrolled: false } },
        });
      }
      return Promise.resolve(listResponse([makeUser()]));
    });
    API.post.mockImplementation((url) => {
      if (url === '/api/verify') {
        return Promise.resolve({ data: { success: true } });
      }
      if (url === '/api/v2/admin/security/users/7/totp/force-disable') {
        return Promise.resolve({ data: { success: true } });
      }
      return Promise.reject(new Error(`unexpected POST ${url}`));
    });

    render(<HFAdminUsers />);
    await waitFor(() => screen.getByTestId('user-disable-2fa-btn-7'));

    fireEvent.click(screen.getByTestId('user-disable-2fa-btn-7'));
    await waitFor(() => screen.getByTestId('disable-2fa-reason'));

    // The confirm button must not fire without a reason.
    expect(screen.getByTestId('disable-2fa-confirm')).toBeDisabled();
    fireEvent.change(screen.getByTestId('disable-2fa-reason'), {
      target: { value: 'support ticket #4242, user lost their device' },
    });
    expect(screen.getByTestId('disable-2fa-confirm')).not.toBeDisabled();
    fireEvent.click(screen.getByTestId('disable-2fa-confirm'));

    // The step-up modal appears (unenrolled acting root -> "session confirmation" tab).
    await waitFor(() => screen.getByText('Confirm'));
    fireEvent.click(screen.getByText('Confirm'));

    await waitFor(() => {
      expect(API.post).toHaveBeenCalledWith('/api/verify', {
        method: 'session',
        code: '',
      });
    });
    await waitFor(() => {
      expect(API.post).toHaveBeenCalledWith(
        '/api/v2/admin/security/users/7/totp/force-disable',
        { reason: 'support ticket #4242, user lost their device' },
      );
    });
    // The reason prompt closes on success and the list refetches.
    await waitFor(() => {
      expect(screen.queryByTestId('disable-2fa-reason')).toBeNull();
    });
  });

  // The step-up modal (Semi, portaled to <body>) opens ON TOP of the reason
  // prompt. An Escape pressed inside it must not also throw the reason away
  // — the operator would come back from a cancelled step-up to an empty
  // table with the typed reason gone.
  it('the 2FA reason prompt is the dialog itself and ignores an Escape pressed inside the step-up modal', async () => {
    API.get.mockResolvedValue(listResponse([makeUser()]));

    render(<HFAdminUsers />);
    await waitFor(() => screen.getByTestId('user-disable-2fa-btn-7'));
    fireEvent.click(screen.getByTestId('user-disable-2fa-btn-7'));
    await waitFor(() => screen.getByTestId('disable-2fa-reason'));

    // role + aria-modal sit on the panel that holds the form, not on the
    // backdrop around it.
    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveAttribute('aria-modal', 'true');
    expect(dialog).toHaveAccessibleName(/Disable 2FA/);
    expect(dialog.contains(screen.getByTestId('disable-2fa-reason'))).toBe(
      true,
    );
    expect(dialog.contains(screen.getByTestId('disable-2fa-confirm'))).toBe(
      true,
    );

    // Stand-in for the portaled step-up modal: a focused node outside the
    // panel.
    const stepUp = document.createElement('input');
    document.body.appendChild(stepUp);
    stepUp.focus();
    fireEvent.keyDown(stepUp, { key: 'Escape' });
    expect(screen.getByTestId('disable-2fa-reason')).toBeTruthy();
    stepUp.remove();

    // With nothing else on top, Escape dismisses the prompt.
    fireEvent.keyDown(document.body, { key: 'Escape' });
    await waitFor(() => {
      expect(screen.queryByTestId('disable-2fa-reason')).toBeNull();
    });
  });

  it('shows the forbidden panel on 403', async () => {
    API.get.mockRejectedValue({ response: { status: 403 } });

    render(<HFAdminUsers />);

    await waitFor(() => {
      expect(
        screen.getAllByText('Admin access required').length,
      ).toBeGreaterThan(0);
    });
  });

  it('keeps the create control disabled with an honest reason', async () => {
    API.get.mockResolvedValue(listResponse([]));

    render(<HFAdminUsers />);
    await waitFor(() => screen.getByTestId('new-user-btn'));

    const btn = screen.getByTestId('new-user-btn');
    expect(btn.disabled).toBe(true);
    expect(btn.getAttribute('title')).toMatch(/password\/invite flow/i);
  });
});

// ─── L7 (cycle 13): three-state load status ───────────────────────────────
//
// Before this lane, fetchUsers only special-cased a rejected 403 — every
// OTHER failure (a 500, a dropped connection, or the session-auth branch's
// 200 {success:false} refusal, which never even reaches the catch block)
// left `users` at its initial [] and rendered "No users yet.": the same
// copy a genuinely brand-new deployment would show.
describe('Admin Users page — three-state load status (rows / forbidden / error-with-retry)', () => {
  it('shows the forbidden panel, not the empty list, for a 200 {success:false} refusal', async () => {
    // internal/adapter/middleware/auth.go's minRole check answers HTTP 200
    // {"success":false,...} for a logged-in non-root session — this never
    // reaches fetchUsers' catch block at all.
    API.get.mockResolvedValue({
      data: { success: false, message: '无权进行此操作，权限不足' },
    });

    render(<HFAdminUsers />);

    await waitFor(() => {
      expect(
        screen.getAllByText('Admin access required').length,
      ).toBeGreaterThan(0);
    });
    expect(screen.queryByText('No users yet.')).toBeNull();
  });

  it('shows an error-with-retry panel, not the empty list or the forbidden panel, on a 500', async () => {
    API.get.mockRejectedValue({ response: { status: 500 } });

    render(<HFAdminUsers />);

    await waitFor(() => screen.getByTestId('users-retry-btn'));
    // Rendered in both the <h1> and the panel body, same as the 403 case
    // above — getAllByText, not getByText.
    expect(screen.getAllByText("Couldn't load users").length).toBeGreaterThan(
      0,
    );
    expect(screen.queryByText('No users yet.')).toBeNull();
    expect(screen.queryByText('Admin access required')).toBeNull();
  });

  it('shows an error-with-retry panel on a dropped/network-level rejection', async () => {
    API.get.mockRejectedValue(new Error('network down'));

    render(<HFAdminUsers />);

    await waitFor(() => screen.getByTestId('users-retry-btn'));
    expect(screen.queryByText('No users yet.')).toBeNull();
  });

  it('the retry control re-fetches and can recover into the rows state', async () => {
    // Flipped only by the explicit retry click below — the page also
    // schedules a debounced re-fetch on mount (keyword/statusFilter effect),
    // so a call-count-based fixture would race that timer instead of the
    // click this test means to exercise.
    let shouldSucceed = false;
    API.get.mockImplementation(() =>
      shouldSucceed
        ? Promise.resolve(listResponse([makeUser()]))
        : Promise.reject({ response: { status: 500 } }),
    );

    render(<HFAdminUsers />);
    await waitFor(() => screen.getByTestId('users-retry-btn'));

    shouldSucceed = true;
    fireEvent.click(screen.getByTestId('users-retry-btn'));

    await waitFor(() => screen.getByText('Alice'));
    expect(screen.queryByTestId('users-retry-btn')).toBeNull();
  });
});
