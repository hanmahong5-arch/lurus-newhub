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
  },
  showError: vi.fn(),
  showSuccess: vi.fn(),
}));

vi.mock('../../../../components/hifi/HFShell', () => ({
  default: ({ children }) =>
    React.createElement('div', { 'data-testid': 'hf-shell' }, children),
}));

import HFAdminSettings from './index';
import { API } from '../../../../helpers';

const optionsResponse = (pairs) => ({
  data: {
    success: true,
    data: Object.entries(pairs).map(([key, value]) => ({ key, value })),
  },
});

beforeEach(() => {
  API.get.mockReset();
  API.put.mockReset();
});

describe('Admin Settings page', () => {
  it('renders live values from GET /options', async () => {
    API.get.mockResolvedValue(
      optionsResponse({ SystemName: 'My Hub', Footer: 'foot' }),
    );

    render(<HFAdminSettings />);

    await waitFor(() => {
      expect(screen.getByTestId('field-SystemName').value).toBe('My Hub');
    });
  });

  it('toggling a key PUTs that single key with the new boolean', async () => {
    API.get.mockResolvedValue(optionsResponse({ GitHubOAuthEnabled: 'false' }));
    API.put.mockResolvedValue({ data: { success: true } });

    render(<HFAdminSettings />);
    await waitFor(() => screen.getByTestId('panel-auth'));

    // Switch to the Auth panel and flip the GitHub OAuth toggle.
    fireEvent.click(screen.getByTestId('panel-auth'));
    await waitFor(() => screen.getByTestId('toggle-GitHubOAuthEnabled'));
    fireEvent.click(screen.getByTestId('toggle-GitHubOAuthEnabled'));

    await waitFor(() => {
      const call = API.put.mock.calls.find(
        ([url]) => url === '/api/v2/admin/options',
      );
      expect(call).toBeTruthy();
      expect(call[1]).toEqual({ key: 'GitHubOAuthEnabled', value: true });
    });
  });

  it('surfaces the backend validation-failure message inline', async () => {
    API.get.mockResolvedValue(optionsResponse({ GitHubOAuthEnabled: 'false' }));
    API.put.mockResolvedValue({
      data: { success: false, message: 'please set GitHub Client Id first' },
    });

    render(<HFAdminSettings />);
    await waitFor(() => screen.getByTestId('panel-auth'));
    fireEvent.click(screen.getByTestId('panel-auth'));
    await waitFor(() => screen.getByTestId('toggle-GitHubOAuthEnabled'));
    fireEvent.click(screen.getByTestId('toggle-GitHubOAuthEnabled'));

    await waitFor(() => {
      const err = screen.getByTestId('error-GitHubOAuthEnabled');
      expect(err.textContent).toMatch(/GitHub Client Id/i);
    });
  });

  it('shows the forbidden panel on 403', async () => {
    API.get.mockRejectedValue({ response: { status: 403 } });

    render(<HFAdminSettings />);

    await waitFor(() => {
      expect(
        screen.getAllByText('Admin access required').length,
      ).toBeGreaterThan(0);
    });
  });

  /*
   * 401 is neither a transport failure nor a permission problem. L4 made the
   * v2 admin group answer 401 UNAUTHENTICATED when the session is missing or
   * invalid and 403 PERMISSION_DENIED when the role is short
   * (internal/adapter/middleware/admin_jwt_auth.go). "Contact a platform
   * administrator" is the wrong instruction for an expired cookie: the
   * operator has the permission, they need to sign in again.
   */
  it('shows a sign-in-again state on 401, not the permission panel', async () => {
    API.get.mockRejectedValue({ response: { status: 401 } });

    render(<HFAdminSettings />);

    await waitFor(() => screen.getByTestId('settings-signed-out'));
    expect(screen.getByTestId('settings-sign-in').getAttribute('href')).toBe(
      '/login',
    );
    expect(screen.queryByText('Admin access required')).toBeNull();
    expect(screen.queryByTestId('settings-error')).toBeNull();
    // And no switch is drawn at all — the failure that started this whole
    // item was a page full of unchecked boxes.
    expect(screen.queryByTestId('toggle-PasswordLoginEnabled')).toBeNull();
  });

  it('still shows the permission panel on 403', async () => {
    API.get.mockRejectedValue({ response: { status: 403 } });

    render(<HFAdminSettings />);

    await waitFor(() => {
      expect(
        screen.getAllByText('Admin access required').length,
      ).toBeGreaterThan(0);
    });
    expect(screen.queryByTestId('settings-signed-out')).toBeNull();
    expect(screen.queryByTestId('settings-error')).toBeNull();
  });

  /*
   * Cycle 12 L3. setOptions ran only on success, so any non-403 failure left
   * `options` as the initial {} — and isOn() reads `options[key] === 'true'`,
   * which renders every toggle UNCHECKED. A 502 therefore drew a page saying
   * password login, registration, GitHub OAuth, Telegram, WeChat and
   * Turnstile were all switched off. That is not a degraded view of the
   * settings; it is the opposite of several of them.
   */
  it('shows an error state with a retry, and no toggles at all, on a 502', async () => {
    API.get.mockRejectedValue({ response: { status: 502 } });

    render(<HFAdminSettings />);

    await waitFor(() => screen.getByTestId('settings-error'));
    expect(screen.getByTestId('settings-retry')).toBeTruthy();
    expect(screen.queryByTestId('toggle-PasswordLoginEnabled')).toBeNull();
    expect(screen.queryByTestId('toggle-GitHubOAuthEnabled')).toBeNull();
  });

  it('shows an error state on a 200 that carries success:false', async () => {
    API.get.mockResolvedValue({
      data: { success: false, message: 'option store unreachable' },
    });

    render(<HFAdminSettings />);

    await waitFor(() => screen.getByTestId('settings-error'));
    expect(screen.getByTestId('settings-error').textContent).toContain(
      'option store unreachable',
    );
  });

  it('retry refetches and renders the options once the call succeeds', async () => {
    API.get.mockRejectedValue({ response: { status: 502 } });

    render(<HFAdminSettings />);
    await waitFor(() => screen.getByTestId('settings-error'));

    API.get.mockResolvedValue(optionsResponse({ SystemName: 'Recovered' }));
    fireEvent.click(screen.getByTestId('settings-retry'));

    await waitFor(() => {
      expect(screen.getByTestId('field-SystemName').value).toBe('Recovered');
    });
    expect(screen.queryByTestId('settings-error')).toBeNull();
  });

  /*
   * The second half of the same lie: a key the backend did not return is not
   * a key that is switched off. Those toggles render indeterminate and say
   * so, instead of drawing an unchecked box that reads as a decision.
   */
  it('marks a toggle whose key the response omitted as unknown, not off', async () => {
    API.get.mockResolvedValue(
      optionsResponse({ PasswordLoginEnabled: 'true' }),
    );

    render(<HFAdminSettings />);
    await waitFor(() => screen.getByTestId('panel-general'));
    fireEvent.click(screen.getByTestId('panel-auth'));

    const known = screen.getByTestId('toggle-PasswordLoginEnabled');
    expect(known.getAttribute('data-known')).toBe('true');
    expect(known.checked).toBe(true);
    expect(known.indeterminate).toBe(false);

    const unknown = screen.getByTestId('toggle-GitHubOAuthEnabled');
    expect(unknown.getAttribute('data-known')).toBe('false');
    expect(unknown.indeterminate).toBe(true);
    expect(
      screen.getByTestId('unknown-GitHubOAuthEnabled').textContent,
    ).toMatch(/not reported/i);
  });

  it('a key the response returned as false is off, not unknown', async () => {
    API.get.mockResolvedValue(optionsResponse({ GitHubOAuthEnabled: 'false' }));

    render(<HFAdminSettings />);
    await waitFor(() => screen.getByTestId('panel-auth'));
    fireEvent.click(screen.getByTestId('panel-auth'));

    const off = screen.getByTestId('toggle-GitHubOAuthEnabled');
    expect(off.getAttribute('data-known')).toBe('true');
    expect(off.checked).toBe(false);
    expect(off.indeterminate).toBe(false);
    expect(screen.queryByTestId('unknown-GitHubOAuthEnabled')).toBeNull();
  });

  /*
   * Same lie, third shape, and the sharpest of the three: an absent
   * text/number key rendered as an empty box with nothing to distinguish it
   * from a box the operator cleared. On the quota panel that empty box sits
   * under "Quota for new user" and "USD exchange rate", where empty reads as
   * zero — a number an operator might well act on.
   */
  it('marks a number field whose key the response omitted as unknown, not empty', async () => {
    API.get.mockResolvedValue(optionsResponse({ QuotaForNewUser: '10000' }));

    render(<HFAdminSettings />);
    await waitFor(() => screen.getByTestId('panel-quota'));
    fireEvent.click(screen.getByTestId('panel-quota'));

    const known = screen.getByTestId('field-QuotaForNewUser');
    expect(known.getAttribute('data-known')).toBe('true');
    expect(known.value).toBe('10000');
    expect(screen.queryByTestId('unknown-QuotaForNewUser')).toBeNull();

    const unknown = screen.getByTestId('field-USDExchangeRate');
    expect(unknown.getAttribute('data-known')).toBe('false');
    expect(unknown.value).toBe('');
    expect(screen.getByTestId('unknown-USDExchangeRate').textContent).toMatch(
      /not reported/i,
    );
  });

  it('marks an absent textarea the same way', async () => {
    API.get.mockResolvedValue(optionsResponse({ SystemName: 'Acme' }));

    render(<HFAdminSettings />);
    await waitFor(() => screen.getByTestId('field-SystemName'));

    expect(
      screen.getByTestId('field-SystemName').getAttribute('data-known'),
    ).toBe('true');
    expect(screen.getByTestId('field-About').getAttribute('data-known')).toBe(
      'false',
    );
    expect(screen.getByTestId('unknown-About').textContent).toMatch(
      /not reported/i,
    );
  });
});
