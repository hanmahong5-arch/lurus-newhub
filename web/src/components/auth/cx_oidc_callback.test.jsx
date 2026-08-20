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
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (k) => k, i18n: { language: 'en' } }),
}));

const navigate = vi.fn();
vi.mock('react-router-dom', () => ({ useNavigate: () => navigate }));

// Loading is the "still working" marker. Stub it to a visible node — a
// `() => null` stub would let the component render nothing at all and still
// look like a pass.
vi.mock('../common/ui/Loading', () => ({
  default: () => React.createElement('div', { 'data-testid': 'loading' }),
}));

const apiGet = vi.fn();
const showError = vi.fn();
const showSuccess = vi.fn();
const updateAPI = vi.fn();
const setUserData = vi.fn();
const clearTenantSlug = vi.fn();

vi.mock('../../helpers', () => ({
  API: { get: (...a) => apiGet(...a) },
  showError: (...a) => showError(...a),
  showSuccess: (...a) => showSuccess(...a),
  updateAPI: (...a) => updateAPI(...a),
  setUserData: (...a) => setUserData(...a),
  clearTenantSlug: (...a) => clearTenantSlug(...a),
}));

const userDispatch = vi.fn();
vi.mock('../../context/User', () => ({
  UserContext: React.createContext([{}, (...a) => userDispatch(...a)]),
}));

import OidcCallback from './OidcCallback';

const USER = { id: 7, username: 'alice', role: 1 };

beforeEach(() => {
  localStorage.clear();
  navigate.mockReset();
  apiGet.mockReset();
  showError.mockReset();
  showSuccess.mockReset();
  updateAPI.mockReset();
  setUserData.mockReset();
  clearTenantSlug.mockReset();
  userDispatch.mockReset();
});

afterEach(() => {
  vi.useRealTimers();
});

describe('OidcCallback — successful session exchange', () => {
  beforeEach(() => {
    apiGet.mockResolvedValue({
      data: { success: true, message: '', data: USER },
    });
  });

  it('asks the v2 session endpoint with the global error handler suppressed', async () => {
    render(<OidcCallback />);

    await waitFor(() => expect(apiGet).toHaveBeenCalledTimes(1));
    expect(apiGet).toHaveBeenCalledWith('/api/v2/auth/session-info', {
      skipErrorHandler: true,
    });
  });

  it('drops any stale tenant slug so the session lands on the v1 routes', async () => {
    render(<OidcCallback />);
    await waitFor(() => expect(clearTenantSlug).toHaveBeenCalledTimes(1));
  });

  it('persists the returned user into context, localStorage and the API client', async () => {
    render(<OidcCallback />);

    await waitFor(() => expect(userDispatch).toHaveBeenCalled());
    expect(userDispatch).toHaveBeenCalledWith({
      type: 'login',
      payload: USER,
    });
    expect(JSON.parse(localStorage.getItem('user'))).toEqual(USER);
    expect(setUserData).toHaveBeenCalledWith(USER);
    expect(updateAPI).toHaveBeenCalledTimes(1);
  });

  it('lands the user on the console, not the login page', async () => {
    render(<OidcCallback />);

    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/console'));
    expect(showSuccess).toHaveBeenCalledTimes(1);
    expect(showError).not.toHaveBeenCalled();
  });

  it('shows the loading indicator and no error text on the happy path', async () => {
    render(<OidcCallback />);

    await waitFor(() => expect(navigate).toHaveBeenCalled());
    expect(screen.getByTestId('loading')).toBeInTheDocument();
    expect(document.body.textContent).not.toContain('正在返回登录页...');
  });
});

describe('OidcCallback — rejected session', () => {
  it('does NOT establish a session when the backend answers success:false', async () => {
    apiGet.mockResolvedValue({
      data: { success: false, message: 'account disabled', data: null },
    });

    render(<OidcCallback />);

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('account disabled'),
    );
    // The security-relevant invariant: a refused login must leave no trace of
    // a logged-in user anywhere.
    expect(userDispatch).not.toHaveBeenCalled();
    expect(localStorage.getItem('user')).toBeNull();
    expect(setUserData).not.toHaveBeenCalled();
    expect(updateAPI).not.toHaveBeenCalled();
    expect(navigate).not.toHaveBeenCalledWith('/console');
  });

  it('renders the failure message and the "returning to login" notice', async () => {
    apiGet.mockResolvedValue({
      data: { success: false, message: 'account disabled' },
    });

    render(<OidcCallback />);

    await waitFor(() =>
      expect(screen.queryByTestId('loading')).not.toBeInTheDocument(),
    );
    expect(document.body.textContent).toContain('account disabled');
    expect(document.body.textContent).toContain('正在返回登录页...');
  });

  it('falls back to a generic message when the backend sends none', async () => {
    apiGet.mockResolvedValue({ data: { success: false } });

    render(<OidcCallback />);
    await waitFor(() => expect(showError).toHaveBeenCalledWith('登录失败'));
  });

  it('prefers the HTTP body message over the thrown error text', async () => {
    apiGet.mockRejectedValue({
      response: { data: { message: 'token expired' } },
      message: 'Request failed with status code 401',
    });

    render(<OidcCallback />);
    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('token expired'),
    );
  });

  it('bounces back to /login after the grace period', async () => {
    vi.useFakeTimers();
    apiGet.mockResolvedValue({ data: { success: false, message: 'nope' } });

    render(<OidcCallback />);
    await vi.advanceTimersByTimeAsync(0);

    expect(navigate).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(3000);
    expect(navigate).toHaveBeenCalledWith('/login');
  });
});

describe('OidcCallback — unmount safety', () => {
  it('does not dispatch a login for a response that lands after unmount', async () => {
    let resolveFn;
    apiGet.mockReturnValue(
      new Promise((res) => {
        resolveFn = res;
      }),
    );

    const { unmount } = render(<OidcCallback />);
    unmount();
    resolveFn({ data: { success: true, data: USER } });
    await Promise.resolve();
    await Promise.resolve();

    expect(userDispatch).not.toHaveBeenCalled();
    expect(navigate).not.toHaveBeenCalled();
  });
});
