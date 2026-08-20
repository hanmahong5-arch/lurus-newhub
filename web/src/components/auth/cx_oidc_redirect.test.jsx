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
import { act, render, screen, waitFor } from '@testing-library/react';

// `t` is a spy, not a no-op: whether this screen consults i18n at all is one of
// the things asserted below.
const tSpy = vi.fn((k) => k);
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (...a) => tSpy(...a), i18n: { language: 'en' } }),
}));

// Loading is the only thing on screen until the bridge answers. Stub it to a
// real node: a `() => null` stub would let the component render an empty page
// and still look like a pass.
vi.mock('../common/ui/Loading', () => ({
  default: () => React.createElement('div', { 'data-testid': 'loading' }),
}));

vi.mock('@douyinfe/semi-ui', () => {
  const Typography = ({ children }) =>
    React.createElement('div', null, children);
  Typography.Text = ({ children, ...rest }) =>
    React.createElement('span', rest, children);
  return {
    Card: ({ children, ...rest }) =>
      React.createElement('section', rest, children),
    Typography,
  };
});

const apiPost = vi.fn();
vi.mock('../../helpers', () => ({
  API: { post: (...a) => apiPost(...a) },
}));

const setTenantSlug = vi.fn();
vi.mock('../../helpers/apiMode', () => ({
  setTenantSlug: (...a) => setTenantSlug(...a),
}));

import OidcRedirect from './OidcRedirect';

// jsdom's window.location cannot be reassigned, so swap it for a recording
// double. `origin` is fixed so the asserted URLs are exact strings.
const replace = vi.fn();
let hrefWrites = [];
const installLocation = () => {
  replace.mockClear();
  hrefWrites = [];
  const stub = {
    origin: 'https://hub.test',
    replace: (...a) => replace(...a),
  };
  Object.defineProperty(stub, 'href', {
    get: () => 'https://hub.test/console/v2/oidc',
    set: (v) => hrefWrites.push(v),
  });
  Object.defineProperty(window, 'location', {
    configurable: true,
    writable: true,
    value: stub,
  });
};

const realLocation = window.location;

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  installLocation();
});

afterEach(() => {
  vi.useRealTimers();
  Object.defineProperty(window, 'location', {
    configurable: true,
    writable: true,
    value: realLocation,
  });
});

const flush = async () => {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
};

describe('OidcRedirect — session bridge', () => {
  it('shows the loading indicator while the bridge call is in flight', async () => {
    apiPost.mockReturnValue(new Promise(() => {}));
    render(React.createElement(OidcRedirect, null));
    expect(screen.getByTestId('loading')).toBeInTheDocument();
    // The 3s fallback card must NOT be visible immediately.
    expect(screen.queryByText(/正在跳转到统一登录/)).not.toBeInTheDocument();
  });

  it('calls the bridge with the global error handler suppressed', async () => {
    apiPost.mockResolvedValue({ data: { success: false } });
    render(React.createElement(OidcRedirect, null));
    await flush();
    expect(apiPost).toHaveBeenCalledWith(
      '/api/v2/auth/zita-bootstrap',
      {},
      { skipErrorHandler: true },
    );
  });

  it('stores the bridged user, persists the tenant slug and lands on the dashboard', async () => {
    apiPost.mockResolvedValue({
      data: {
        success: true,
        data: { id: 42, username: 'bob', tenant_slug: 'acme' },
      },
    });
    render(React.createElement(OidcRedirect, null));
    await flush();

    expect(JSON.parse(localStorage.getItem('user'))).toEqual({
      id: 42,
      username: 'bob',
      tenant_slug: 'acme',
    });
    expect(setTenantSlug).toHaveBeenCalledWith('acme');
    expect(replace).toHaveBeenCalledWith(
      'https://hub.test/console/v2/dashboard',
    );
    // Having succeeded, it must not also kick off the identity login redirect.
    expect(hrefWrites).toEqual([]);
  });

  it('does not write a tenant slug when the bridge omits one', async () => {
    apiPost.mockResolvedValue({ data: { success: true, data: { id: 7 } } });
    render(React.createElement(OidcRedirect, null));
    await flush();

    expect(setTenantSlug).not.toHaveBeenCalled();
    expect(replace).toHaveBeenCalledWith(
      'https://hub.test/console/v2/dashboard',
    );
  });

  it('treats a success envelope with no user id as "not signed in" and goes to identity login', async () => {
    apiPost.mockResolvedValue({ data: { success: true, data: {} } });
    render(React.createElement(OidcRedirect, null));
    await flush();

    expect(localStorage.getItem('user')).toBeNull();
    expect(replace).not.toHaveBeenCalled();
    expect(hrefWrites).toHaveLength(1);
    expect(hrefWrites[0]).toBe(
      '/api/v2/auth/zita-login?return_to=' +
        encodeURIComponent('https://hub.test/console/v2/dashboard'),
    );
  });

  it('sends an unauthenticated visitor to identity login rather than looping', async () => {
    apiPost.mockRejectedValue({ response: { status: 401 } });
    render(React.createElement(OidcRedirect, null));
    await flush();

    expect(hrefWrites).toHaveLength(1);
    expect(hrefWrites[0]).toContain('/api/v2/auth/zita-login?return_to=');
    expect(replace).not.toHaveBeenCalled();
  });

  it('sends a network failure to identity login too (no user cached)', async () => {
    apiPost.mockRejectedValue(new Error('offline'));
    render(React.createElement(OidcRedirect, null));
    await flush();

    expect(localStorage.getItem('user')).toBeNull();
    expect(hrefWrites).toHaveLength(1);
  });

  it('parks a disabled account on the terminal page instead of retrying login forever', async () => {
    apiPost.mockRejectedValue({ response: { status: 403 } });
    render(React.createElement(OidcRedirect, null));
    await flush();

    expect(replace).toHaveBeenCalledWith(
      'https://hub.test/console/v2/account-disabled',
    );
    // Crucially it must NOT also start the identity-login redirect, which is
    // what would produce the bounce loop this branch exists to prevent.
    expect(hrefWrites).toEqual([]);
    expect(localStorage.getItem('user')).toBeNull();
  });

  it('reveals the "still redirecting" card only after the 3s grace period', async () => {
    vi.useFakeTimers();
    apiPost.mockRejectedValue({ response: { status: 401 } });
    render(React.createElement(OidcRedirect, null));
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(screen.queryByText(/正在跳转到统一登录/)).not.toBeInTheDocument();

    await act(async () => {
      vi.advanceTimersByTime(2999);
    });
    expect(screen.queryByText(/正在跳转到统一登录/)).not.toBeInTheDocument();

    await act(async () => {
      vi.advanceTimersByTime(1);
    });
    expect(screen.getByText(/正在跳转到统一登录/)).toBeInTheDocument();
  });

  it('does not navigate after the component has been unmounted mid-flight', async () => {
    let resolvePost;
    apiPost.mockReturnValue(
      new Promise((r) => {
        resolvePost = r;
      }),
    );
    const { unmount } = render(React.createElement(OidcRedirect, null));
    unmount();
    await act(async () => {
      resolvePost({
        data: { success: true, data: { id: 9, tenant_slug: 't' } },
      });
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(replace).not.toHaveBeenCalled();
    expect(hrefWrites).toEqual([]);
    expect(setTenantSlug).not.toHaveBeenCalled();
  });

  it('does not leave an account in localStorage when the bridge answers after unmount', async () => {
    let resolvePost;
    apiPost.mockReturnValue(
      new Promise((r) => {
        resolvePost = r;
      }),
    );
    const { unmount } = render(React.createElement(OidcRedirect, null));
    unmount();
    await act(async () => {
      resolvePost({ data: { success: true, data: { id: 9 } } });
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(localStorage.getItem('user')).toBeNull();
  });

  // DEFECT (cosmetic/i18n): every other screen in this console runs its copy
  // through `useTranslation()`, but the redirect fallback hard-codes
  // "正在跳转到统一登录，请稍候..." — this component never imports i18n at all.
  // A visitor whose UI language is English/French/Japanese gets Chinese on the
  // one screen that appears when login is already going wrong. The it.skip
  // below is the contract.
  //
  // The pin that used to sit here asserted `tSpy` was NEVER called — it locked
  // the absence of i18n in place, so wiring the copy through `t()` would have
  // turned it red and the fix would have arrived looking like a regression. It
  // was deleted rather than rewritten: everything it observed besides that
  // assertion (the card appears, and only after the 3s grace period) is
  // already asserted by "reveals the 'still redirecting' card only after the
  // 3s grace period" above, which survives the fix untouched because the
  // Chinese source string doubles as the i18n key here as it does everywhere
  // else in this console.
  it.skip('the redirect fallback copy must go through i18n like every other screen', async () => {
    vi.useFakeTimers();
    apiPost.mockRejectedValue({ response: { status: 401 } });
    render(React.createElement(OidcRedirect, null));
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
    await act(async () => {
      vi.advanceTimersByTime(3000);
    });
    expect(tSpy).toHaveBeenCalled();
  });
});
