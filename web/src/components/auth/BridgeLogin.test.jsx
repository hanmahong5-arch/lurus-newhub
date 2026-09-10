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

const tSpy = vi.fn((k) => k);
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (...a) => tSpy(...a), i18n: { language: 'en' } }),
}));

vi.mock('@douyinfe/semi-ui', () => {
  const Typography = ({ children }) =>
    React.createElement('div', null, children);
  Typography.Text = ({ children, ...rest }) =>
    React.createElement('span', rest, children);
  Typography.Title = ({ children, ...rest }) =>
    React.createElement('h5', rest, children);
  return {
    Button: ({ children, onClick, ...rest }) =>
      React.createElement('button', { onClick, ...rest }, children),
    Card: ({ children, ...rest }) =>
      React.createElement('section', rest, children),
    Input: ({ value, onChange, placeholder, ...rest }) =>
      React.createElement('input', {
        value: value ?? '',
        placeholder,
        onChange: (e) => onChange?.(e.target.value),
        ...rest,
      }),
    Typography,
  };
});

const apiPost = vi.fn();
const apiGet = vi.fn();
vi.mock('../../helpers', () => ({
  API: { post: (...a) => apiPost(...a), get: (...a) => apiGet(...a) },
}));

const setTenantSlug = vi.fn();
vi.mock('../../helpers/apiMode', () => ({
  setTenantSlug: (...a) => setTenantSlug(...a),
}));

import BridgeLogin, { credentialsFromHash } from './BridgeLogin';

const replace = vi.fn();
const replaceState = vi.fn();
const installLocation = (hash) => {
  const stub = {
    origin: 'https://uat.test',
    pathname: '/bridge-login',
    hash,
    replace: (...a) => replace(...a),
    href: 'https://uat.test/bridge-login',
  };
  Object.defineProperty(window, 'location', {
    configurable: true,
    writable: true,
    value: stub,
  });
  Object.defineProperty(window, 'history', {
    configurable: true,
    writable: true,
    value: { replaceState: (...a) => replaceState(...a) },
  });
};

const bridgeStatus = (enabled) => ({
  data: { data: { login_methods: { bridge: { enabled } } } },
});

const okUser = {
  data: {
    success: true,
    data: { id: 1, username: 'root', role: 100, tenant_slug: 'lurus' },
  },
};

describe('BridgeLogin', () => {
  const realLocation = window.location;
  const realHistory = window.history;

  beforeEach(() => {
    apiPost.mockReset();
    apiGet.mockReset();
    setTenantSlug.mockReset();
    replace.mockReset();
    replaceState.mockReset();
    tSpy.mockClear();
    localStorage.clear();
  });

  afterEach(() => {
    Object.defineProperty(window, 'location', {
      configurable: true,
      writable: true,
      value: realLocation,
    });
    Object.defineProperty(window, 'history', {
      configurable: true,
      writable: true,
      value: realHistory,
    });
  });

  it('parses the token and user id out of the fragment, defaulting the user to 1', () => {
    expect(credentialsFromHash('#t=abc&u=7')).toEqual({
      token: 'abc',
      userId: '7',
    });
    expect(credentialsFromHash('#t=abc')).toEqual({
      token: 'abc',
      userId: '1',
    });
    expect(credentialsFromHash('')).toEqual({ token: '', userId: '1' });
  });

  it('a link that carries a token signs in with no further interaction', async () => {
    installLocation('#t=probe-token&u=1');
    apiGet.mockResolvedValue(bridgeStatus(true));
    apiPost.mockResolvedValue(okUser);

    render(<BridgeLogin />);

    await waitFor(() => expect(apiPost).toHaveBeenCalled());
    const [url] = apiPost.mock.calls[0];
    expect(url).toBe('/api/v2/bridge/exchange?token=probe-token&user_id=1');
    await waitFor(() =>
      expect(replace).toHaveBeenCalledWith(
        'https://uat.test/console/v2/dashboard',
      ),
    );
    expect(JSON.parse(localStorage.getItem('user')).username).toBe('root');
    // The slug comes from the response, never from a literal in this file.
    expect(setTenantSlug).toHaveBeenCalledWith('lurus');
    // The credential must not survive in the address bar.
    expect(replaceState).toHaveBeenCalledWith(null, '', '/bridge-login');
  });

  it('does not store a tenant slug the server could not name', async () => {
    installLocation('#t=probe-token');
    apiGet.mockResolvedValue(bridgeStatus(true));
    apiPost.mockResolvedValue({
      data: {
        success: true,
        data: { id: 1, username: 'root', tenant_slug: '' },
      },
    });

    render(<BridgeLogin />);

    await waitFor(() => expect(replace).toHaveBeenCalled());
    expect(setTenantSlug).not.toHaveBeenCalled();
  });

  it('says so and never calls the exchange where the instance has no bridge route', async () => {
    installLocation('#t=probe-token');
    apiGet.mockResolvedValue(bridgeStatus(false));

    render(<BridgeLogin />);

    await waitFor(() =>
      expect(screen.getByText('此实例未开放验收登录')).toBeTruthy(),
    );
    expect(apiPost).not.toHaveBeenCalled();
    expect(replace).not.toHaveBeenCalled();
  });

  it('reports a rejected token instead of navigating', async () => {
    installLocation('#t=wrong-token');
    apiGet.mockResolvedValue(bridgeStatus(true));
    apiPost.mockRejectedValue({ response: { status: 403 } });

    render(<BridgeLogin />);

    await waitFor(() =>
      expect(screen.getByTestId('bridge-login-error').textContent).toBe(
        '登录令牌无效',
      ),
    );
    expect(replace).not.toHaveBeenCalled();
    expect(localStorage.getItem('user')).toBeNull();
  });

  it('offers the token field when the link carries none', async () => {
    installLocation('');
    apiGet.mockResolvedValue(bridgeStatus(true));

    render(<BridgeLogin />);

    await waitFor(() =>
      expect(screen.getByTestId('bridge-login-token')).toBeTruthy(),
    );
    expect(apiPost).not.toHaveBeenCalled();
  });
});
