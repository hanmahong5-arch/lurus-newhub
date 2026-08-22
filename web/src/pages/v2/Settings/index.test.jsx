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
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react';

// Mock helpers BEFORE importing the component.
vi.mock('../../../helpers', () => ({
  API: {
    get: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
  showError: vi.fn(),
  showSuccess: vi.fn(),
}));

vi.mock('react-router-dom', () => ({
  useNavigate: () => vi.fn(),
}));

// HFShell passthrough — isolates Settings from shell/router dependencies.
vi.mock('../../../components/hifi/HFShell', () => ({
  default: ({ children }) =>
    React.createElement('div', { 'data-testid': 'hf-shell' }, children),
}));

// WIPBanner passthrough — renders its reason so tests can assert its presence.
vi.mock('../../../components/hifi/WIPBanner', () => ({
  default: ({ reason }) =>
    React.createElement(
      'div',
      { 'data-testid': 'wip-banner' },
      reason ?? 'WIP',
    ),
}));

// ConfirmDialog stub — renders a visible modal with a confirm button.
vi.mock('../../../components/common/ConfirmDialog', () => ({
  default: ({ visible, title, onConfirm, onCancel }) =>
    visible
      ? React.createElement(
          'div',
          { 'data-testid': 'confirm-dialog' },
          React.createElement('span', null, title),
          React.createElement(
            'button',
            {
              'data-testid': 'confirm-dialog-confirm',
              onClick: onConfirm,
            },
            'confirm',
          ),
          React.createElement(
            'button',
            { 'data-testid': 'confirm-dialog-cancel', onClick: onCancel },
            'cancel',
          ),
        )
      : null,
}));

// Mirror i18next's en behaviour: return the English defaultValue (2nd arg)
// with {{var}} interpolation, falling back to the key when no default given.
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key, fallback, opts) => {
      const vars =
        typeof fallback === 'object' && fallback !== null ? fallback : opts;
      let out = typeof fallback === 'string' ? fallback : key;
      if (vars) {
        for (const [k, v] of Object.entries(vars)) {
          out = out.split(`{{${k}}}`).join(String(v));
        }
      }
      return out;
    },
  }),
}));

import HFSettings from './index';
import { API } from '../../../helpers';

const fakeProfile = {
  display_name: 'Alice',
  email: 'alice@test.local',
  username: 'alice',
  role: 'user',
  tenant_id: 'acme',
  used_quota: 0,
  request_count: 0,
};

const fakeSessionsResponse = (items) => ({
  data: {
    success: true,
    data: { items, total: items.length },
  },
});

const fakeBillingSummary = {
  balance: 1210.5,
  frozen: 0,
  available: 1210.5,
  lifetime_topup: 2000,
  lifetime_spend: 789.5,
  active_pre_auths: 0,
  pending_orders: 0,
  // intentionally NOT setting wallet_balance_cny — exercise the .balance branch
  mtd_spend_cny: 42.18,
};

const fakeTopups = [
  {
    id: 1,
    content: 'Wallet transfer: 100.00 CNY -> 50000000 quota',
    quota: 50_000_000,
    created_at: Math.floor(Date.now() / 1000) - 86400,
  },
  {
    id: 2,
    content: 'Wallet transfer: 50.00 CNY -> 25000000 quota',
    quota: 25_000_000,
    created_at: Math.floor(Date.now() / 1000) - 172800,
  },
];

beforeEach(() => {
  API.get.mockReset();
  API.put.mockReset();
  API.delete.mockReset();
  window.localStorage.clear();
  window.localStorage.setItem('tenant_slug', 'acme');

  // Default: profile fetch succeeds; sessions not fetched until security tab.
  API.get.mockImplementation((url) => {
    if (url.includes('/user/me')) {
      return Promise.resolve({ data: { success: true, data: fakeProfile } });
    }
    if (url.includes('/sessions')) {
      return Promise.resolve(
        fakeSessionsResponse([
          {
            id: 'current',
            current: true,
            auth_method: 'oidc',
            active_tokens: 3,
            request_count: 42,
            last_seen: Math.floor(Date.now() / 1000) - 30,
          },
        ]),
      );
    }
    if (url.includes('/user/billing/summary')) {
      return Promise.resolve({
        data: { success: true, data: fakeBillingSummary },
      });
    }
    if (url.includes('/billing/topups')) {
      return Promise.resolve({
        data: {
          success: true,
          data: { items: fakeTopups, total: fakeTopups.length },
        },
      });
    }
    return Promise.resolve({ data: { success: false } });
  });
});

describe('Settings page', () => {
  // 1. Security section fetches sessions on mount (when security tab is active).
  //    We render with a controlled tab by clicking the "Security" nav item.
  it('fetches sessions on mount and renders auth_method', async () => {
    render(<HFSettings />);

    // Click the Security section in the left nav.
    const securityNav = screen.getByText('Security');
    securityNav.click();

    await waitFor(() => {
      // Sessions API must have been called with the correct slug.
      const calls = API.get.mock.calls.map((c) => c[0]);
      expect(calls.some((u) => u.includes('/acme/sessions'))).toBe(true);
    });

    await waitFor(() => {
      // auth_method value rendered in the sessions table.
      expect(screen.getByTestId('sessions-table').textContent).toContain(
        'oidc',
      );
    });
  });

  // 2. Notifications section still has WIPBanner.
  it('shows WIPBanner on notifications section', async () => {
    render(<HFSettings />);

    const notifNav = screen.getByText('Notifications');
    notifNav.click();

    await waitFor(() => {
      const banners = screen.getAllByTestId('wip-banner');
      const texts = banners.map((b) => b.textContent);
      expect(texts.some((t) => /notification/i.test(t))).toBe(true);
    });
  });

  // 3. Team section still has WIPBanner.
  it('shows WIPBanner on team section', async () => {
    render(<HFSettings />);

    const teamNav = screen.getByText('Team & roles');
    teamNav.click();

    await waitFor(() => {
      const banners = screen.getAllByTestId('wip-banner');
      const texts = banners.map((b) => b.textContent);
      expect(texts.some((t) => /team/i.test(t))).toBe(true);
    });
  });

  // 4. Clicking "revoke" on a session row opens the ConfirmDialog.
  it('opens ConfirmDialog when revoke button clicked', async () => {
    render(<HFSettings />);

    // Navigate to security section where sessions are shown.
    screen.getByText('Security').click();

    await waitFor(() => {
      expect(screen.getByTestId('sessions-table')).toBeTruthy();
    });

    // Before click: dialog must be absent.
    expect(screen.queryByTestId('confirm-dialog')).toBeNull();

    // Click the revoke button.
    fireEvent.click(screen.getByTestId('revoke-session-btn'));

    // ConfirmDialog must now be visible.
    await waitFor(() => {
      expect(screen.getByTestId('confirm-dialog')).toBeTruthy();
    });
  });

  // 5. Danger zone delete button is disabled with scope-cut tooltip.
  it('danger delete button is disabled with title tooltip', async () => {
    render(<HFSettings />);

    screen.getByText('Danger zone').click();

    await waitFor(() => {
      const btn = screen.getByTestId('danger-delete-btn');
      expect(btn).toBeTruthy();
      expect(btn.disabled).toBe(true);
      expect(btn.title).toMatch(/deferred to v3/i);
    });
  });

  // 6. Subscription tab — Wave A Squad 5A read-only.
  //    Asserts: loading state then data renders; upgrade button is disabled
  //    with the Wave B tooltip. Profile is already in state from mount, so
  //    fetchSubscription enriches via /user/billing/summary.
  it('subscription tab loads tier and disables upgrade button', async () => {
    render(<HFSettings />);

    screen.getByText('Subscription').click();

    // Loading state appears
    await waitFor(() => {
      expect(screen.getByTestId('subscription-section')).toBeTruthy();
    });

    // Data renders — tier badge appears
    await waitFor(() => {
      expect(screen.getByTestId('subscription-tier-badge')).toBeTruthy();
    });

    // Upgrade button is disabled with Wave B tooltip
    const upgradeBtn = screen.getByTestId('subscription-upgrade-btn');
    expect(upgradeBtn.disabled).toBe(true);
    expect(upgradeBtn.title).toMatch(/wave b/i);
  });

  // 7. Billing tab — Wave A Squad 5A read-only.
  //    Asserts: loading -> balance + 30d numbers + transactions table;
  //    "View full history" button disabled with Wave C tooltip.
  it('billing tab loads balance + transactions, disables full-history button', async () => {
    render(<HFSettings />);

    screen.getByText('Billing').click();

    await waitFor(() => {
      expect(screen.getByTestId('billing-section')).toBeTruthy();
    });

    // Balance + 30d rendered
    await waitFor(() => {
      expect(screen.getByTestId('billing-balance').textContent).toMatch(
        /1,210\.50/,
      );
      expect(screen.getByTestId('billing-30d').textContent).toMatch(/42\.18/);
    });

    // Transactions table present with rows
    await waitFor(() => {
      const table = screen.getByTestId('billing-txn-table');
      expect(table).toBeTruthy();
      // Header + 2 fakeTopups data rows = 3 tr elements
      expect(table.querySelectorAll('tr').length).toBeGreaterThanOrEqual(3);
    });

    // "View full history" disabled with Wave C tooltip
    const fullBtn = screen.getByTestId('billing-view-full-btn');
    expect(fullBtn.disabled).toBe(true);
    expect(fullBtn.title).toMatch(/wave c/i);

    // API was called with the right endpoints
    const calls = API.get.mock.calls.map((c) => c[0]);
    expect(calls.some((u) => u.includes('/user/billing/summary'))).toBe(true);
    expect(calls.some((u) => u.includes('/acme/billing/topups'))).toBe(true);
  });

  // 7b. A customer with no billing history gets the empty panel, not a spinner.
  //
  // The section effects were guarded on "is the data still absent?", which is
  // true again the moment a fetch resolves empty — so the effect re-ran without
  // bound. That is not only a failure mode: a customer who has never topped up
  // receives a successful 200 carrying a null summary and an empty items array,
  // which re-arms the guard identically. Measured against the running console
  // in that state before the fix: 670 requests in 5 seconds, and the tab never
  // left "Loading…".
  //
  // HONEST LIMIT OF THIS TEST: it does NOT catch that loop. Verified by
  // restoring the original guard and dependency array — this file stayed green,
  // because jsdom with a mocked, already-resolved API does not reproduce the
  // re-fire the browser exhibits. What it does pin is the state the fix has to
  // produce: the empty panel, resolved, once. The loop's actual regression
  // guard is the counting assertion in
  // tests/e2e/verify-console-settings-and-money.spec.ts, which was verified to
  // fail against the old code (670 requests) and pass against the new one (0).
  it('settles a customer with no billing history onto the empty panel', async () => {
    API.get.mockImplementation((url) => {
      if (url.includes('/user/billing/summary')) {
        return Promise.resolve({ data: { success: true, data: null } });
      }
      if (url.includes('/billing/topups')) {
        return Promise.resolve({
          data: { success: true, data: { items: [] } },
        });
      }
      return Promise.resolve({ data: { success: true, data: {} } });
    });

    render(<HFSettings />);
    screen.getByText('Billing').click();

    await waitFor(() => {
      expect(screen.getByTestId('billing-section')).toBeTruthy();
    });

    // Real elapsed time, not a trivially-satisfied waitFor. Each turn of the
    // loop costs one promise resolution plus a commit, so the count only grows
    // if the effect genuinely re-arms. The first draft of this test used
    // `waitFor(() => expect(true).toBe(true))`, which returns on its first
    // check without ever flushing the re-fire — it passed against the bug.
    const countSummaryCalls = () =>
      API.get.mock.calls.filter((c) =>
        String(c[0]).includes('/user/billing/summary'),
      ).length;

    await act(async () => {
      await new Promise((r) => setTimeout(r, 200));
    });
    const afterFirstSettle = countSummaryCalls();

    await act(async () => {
      await new Promise((r) => setTimeout(r, 200));
    });
    expect(countSummaryCalls()).toBe(afterFirstSettle);
    expect(afterFirstSettle).toBe(1);

    // And it must settle on the empty panel rather than a permanent spinner.
    expect(screen.queryByText(/Loading…/)).toBeNull();
  });

  // 8. Notifications tab — Wave A Squad 5A: stub -> read-only.
  //    Asserts: WIPBanner still present; all 3 channel toggle buttons are
  //    disabled with the Wave B tooltip.
  it('notifications tab renders 3 channels with disabled toggles + WIP banner', async () => {
    render(<HFSettings />);

    screen.getByText('Notifications').click();

    // WIPBanner still shows
    await waitFor(() => {
      const banners = screen.getAllByTestId('wip-banner');
      const texts = banners.map((b) => b.textContent);
      expect(texts.some((t) => /notification/i.test(t))).toBe(true);
    });

    // All 3 channels rendered with disabled toggles + Wave B tooltip
    for (const key of ['email', 'webhook', 'inapp']) {
      const btn = screen.getByTestId(`notif-toggle-${key}`);
      expect(btn).toBeTruthy();
      expect(btn.disabled).toBe(true);
      expect(btn.title).toMatch(/wave b/i);
    }
  });

  // 9. Honesty: Integrations must NOT paint fake "connected · ok" status.
  //    Every channel reads "not configured"; no green status dot; connect
  //    buttons are disabled (no real integration registry behind them).
  it('integrations section shows no fake connected/ok status', async () => {
    render(<HFSettings />);

    screen.getByText('Integrations').click();

    await waitFor(() => {
      expect(screen.getAllByText('not configured').length).toBeGreaterThan(0);
    });

    // No "connected" text anywhere in the section.
    expect(screen.queryAllByText(/connected/i).length).toBe(0);
    // No green status dots (class "dot ok") in the rendered section.
    expect(document.querySelectorAll('.dot.ok').length).toBe(0);
    // Every connect button is disabled (registry deferred).
    const connectButtons = screen.getAllByText('connect');
    expect(connectButtons.length).toBeGreaterThan(0);
    connectButtons.forEach((b) =>
      expect(b.closest('button').disabled).toBe(true),
    );
  });

  // 10. Honesty: Region must not claim a fabricated "current" residency.
  it('region section claims no current data-residency region', async () => {
    render(<HFSettings />);

    screen.getByText('Region & data').click();

    await waitFor(() => {
      expect(screen.getByText('data residency')).toBeTruthy();
    });

    // No region labelled "current" — all are merely "available".
    expect(screen.queryByText('current')).toBeNull();
    expect(screen.getAllByText('available').length).toBe(3);
  });
});
