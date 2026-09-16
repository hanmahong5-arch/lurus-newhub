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

const mockNavigate = vi.fn();
vi.mock('react-router-dom', () => ({
  useNavigate: () => mockNavigate,
}));

// HFShell passthrough — isolates Settings from shell/router dependencies.
// TotpService is the real security signal this panel reports; stub it so each
// test can state exactly which enrolment state it is asserting about.
const mockTotpStatus = vi.fn();
vi.mock('../../../services/secureVerification', () => ({
  TotpService: { getStatus: (...a) => mockTotpStatus(...a) },
}));

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
  // These pages now reach the shared money helpers, which pull in the i18n
  // module; that calls .use(initReactI18next), so the mock has to carry it.
  initReactI18next: { type: '3rdParty', init: () => {} },
  Trans: ({ children }) => children,
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
  mockNavigate.mockReset();
  mockTotpStatus.mockReset();
  // Default: never enrolled. A test that means "enabled" must say so.
  mockTotpStatus.mockResolvedValue({ enrolled: false, pending: false });
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

  // 2. RE-INVERTED: the retirement this test used to lock was itself
  // wrong — PUT /api/user/setting (user.go:521-535) and its dispatch path
  // (app.NotifyUser, internal/app/user_notify.go:53, called from
  // checkAndSendQuotaNotify on the live relay path) already existed, and
  // components/settings/PersonalSetting.jsx was already a working consumer
  // of the same store. This locks the restored panel: seeded from
  // profile.setting, and the PUT body uses exactly the field names in
  // user.go:521-531 — mirroring
  // components/settings/q1_personal_setting.test.jsx:374-429's key-name
  // and type assertions.
  it('notifications section is seeded from profile.setting and saves with the exact backend field names', async () => {
    API.get.mockImplementation((url) => {
      if (url.includes('/user/me')) {
        return Promise.resolve({
          data: {
            success: true,
            data: {
              ...fakeProfile,
              setting: JSON.stringify({
                notify_type: 'gotify',
                quota_warning_threshold: 250000,
                gotify_url: 'https://gotify.example.test',
                gotify_token: 'tok-abc',
                gotify_priority: 7,
                // Opposite booleans on purpose. This panel has no editor for
                // either field, so it must send back what the server gave it;
                // hardcoding a value here instead of carrying it through would
                // silently reset a user's preference on every save. Same
                // technique as q1_personal_setting.test.jsx.
                accept_unset_model_ratio_model: true,
                record_ip_log: false,
              }),
            },
          },
        });
      }
      return Promise.resolve({ data: { success: false } });
    });
    API.put.mockResolvedValue({ data: { success: true } });

    render(<HFSettings />);

    screen.getByText('Notifications').click();

    await waitFor(() => {
      expect(screen.getByTestId('notifications-section')).toBeTruthy();
    });

    // Seeded from profile.setting: gotify was the saved type, so its
    // fields — not webhook/email/bark — are the ones rendered, with the
    // saved values, not the useState defaults ('email' / 100000).
    await waitFor(() => {
      expect(screen.getByTestId('notify-type-select').value).toBe('gotify');
    });
    expect(screen.getByTestId('notify-threshold-input').value).toBe('250000');
    expect(screen.getByTestId('notify-gotify-url-input').value).toBe(
      'https://gotify.example.test',
    );
    expect(screen.getByTestId('notify-gotify-token-input').value).toBe(
      'tok-abc',
    );
    expect(screen.getByTestId('notify-gotify-priority-input').value).toBe('7');

    fireEvent.click(screen.getByTestId('notify-save-btn'));

    await waitFor(() => {
      expect(API.put.mock.calls.some((c) => c[0] === '/api/user/setting')).toBe(
        true,
      );
    });

    const body = API.put.mock.calls.find(
      (c) => c[0] === '/api/user/setting',
    )[1];
    expect(Object.keys(body).sort()).toEqual([
      'accept_unset_model_ratio_model',
      'bark_url',
      'gotify_priority',
      'gotify_token',
      'gotify_url',
      'notification_email',
      'notify_type',
      'quota_warning_threshold',
      'record_ip_log',
      'webhook_secret',
      'webhook_url',
    ]);
    expect(body.notify_type).toBe('gotify');
    expect(body.gotify_url).toBe('https://gotify.example.test');
    expect(body.gotify_token).toBe('tok-abc');
    expect(body.quota_warning_threshold).toBe(250000);
    expect(typeof body.quota_warning_threshold).toBe('number');
    expect(body.gotify_priority).toBe(7);
    expect(typeof body.gotify_priority).toBe('number');
    // Carried through from the seed, not re-derived from a default.
    expect(body.accept_unset_model_ratio_model).toBe(true);
    expect(body.record_ip_log).toBe(false);
  });

  // The form is seeded from GET /user/me. If that call fails, profile stays
  // null, the form shows its useState defaults, and PUT /api/user/setting
  // replaces the whole blob — so an enabled save button would overwrite the
  // user's real webhook/gotify configuration with 'email' / 100000 / empty.
  it('notifications save is disabled until the form has been seeded from the server', async () => {
    API.get.mockImplementation((url) => {
      if (url.includes('/user/me')) {
        return Promise.reject(new Error('network down'));
      }
      return Promise.resolve({ data: { success: false } });
    });

    render(<HFSettings />);
    screen.getByText('Notifications').click();

    await waitFor(() => {
      expect(screen.getByTestId('notifications-section')).toBeTruthy();
    });

    const btn = screen.getByTestId('notify-save-btn');
    expect(btn.disabled).toBe(true);

    fireEvent.click(btn);
    await new Promise((r) => setTimeout(r, 0));
    expect(API.put.mock.calls.some((c) => c[0] === '/api/user/setting')).toBe(
      false,
    );
  });

  // 3. RE-INVERTED: the interim "managed on platform identity" copy plus
  // outbound link this test used to lock was itself a false claim —
  // identity.lurus.cn has no
  // customer-facing team/member surface (its authenticated nav is
  // wallet/topup/subscriptions/invoices/refunds/redeem/account/
  // data-privacy; the only org-membership screen is an internal /admin/v1
  // tool). The section now states plainly that the capability is not
  // available, with no outbound link at all.
  it('team section states plainly that team management is not available, with no link and no WIPBanner', async () => {
    render(<HFSettings />);

    const teamNav = screen.getByText('Team & roles');
    teamNav.click();

    await waitFor(() => {
      expect(screen.getByTestId('team-section')).toBeTruthy();
    });

    expect(screen.queryByTestId('wip-banner')).toBeNull();
    expect(screen.queryByTestId('team-identity-link')).toBeNull();
    expect(
      screen.getByText(
        'Per-tenant team management — invites, roles, removal — is not available in this product yet.',
      ),
    ).toBeTruthy();
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

  // 4b. L7: multiple registered devices render a device column (masked
  // ip/user_agent_family) and a "sign out other devices" button — neither
  // exists with just the single legacy synthetic row (test 1 above).
  it('renders multiple sessions with device info and a sign-out-other-devices button', async () => {
    API.get.mockImplementation((url) => {
      if (url.includes('/user/me')) {
        return Promise.resolve({ data: { success: true, data: fakeProfile } });
      }
      if (url.includes('/sessions')) {
        return Promise.resolve(
          fakeSessionsResponse([
            {
              id: 1,
              current: true,
              is_current: true,
              auth_method: 'session',
              active_tokens: 1,
              request_count: 5,
              last_seen: Math.floor(Date.now() / 1000) - 10,
              ip: '203.0.113.0',
              user_agent_family: 'Chrome',
            },
            {
              id: 2,
              current: false,
              is_current: false,
              auth_method: 'session',
              active_tokens: 1,
              request_count: 5,
              last_seen: Math.floor(Date.now() / 1000) - 3600,
              ip: '198.51.100.0',
              user_agent_family: 'Firefox',
            },
          ]),
        );
      }
      return Promise.resolve({ data: { success: false } });
    });

    render(<HFSettings />);
    screen.getByText('Security').click();

    await waitFor(() => {
      expect(screen.getByTestId('sessions-table')).toBeTruthy();
    });

    const tableText = screen.getByTestId('sessions-table').textContent;
    expect(tableText).toContain('203.0.113.0');
    expect(tableText).toContain('Chrome');
    expect(tableText).toContain('198.51.100.0');
    expect(tableText).toContain('Firefox');
    expect(screen.getByTestId('revoke-others-btn')).toBeTruthy();
  });

  // 4c. L7: revoking a NON-current device calls DELETE .../sessions/:id
  // (not .../sessions/current) and refetches the list — the caller stays
  // on the page, no navigate() to /login.
  it('revoking a non-current session calls DELETE .../sessions/:id and refetches', async () => {
    let deleteCalls = [];
    API.get.mockImplementation((url) => {
      if (url.includes('/user/me')) {
        return Promise.resolve({ data: { success: true, data: fakeProfile } });
      }
      if (url.includes('/sessions')) {
        return Promise.resolve(
          fakeSessionsResponse([
            { id: 1, current: true, is_current: true, auth_method: 'session' },
            {
              id: 2,
              current: false,
              is_current: false,
              auth_method: 'session',
            },
          ]),
        );
      }
      return Promise.resolve({ data: { success: false } });
    });
    API.delete.mockImplementation((url) => {
      deleteCalls.push(url);
      return Promise.resolve({ data: {} }); // 204 No Content shape
    });

    render(<HFSettings />);
    screen.getByText('Security').click();

    await waitFor(() => {
      expect(screen.getByTestId('sessions-table')).toBeTruthy();
    });

    // Row 2 is the non-current device — its revoke button carries the row id.
    fireEvent.click(screen.getByTestId('revoke-session-btn-2'));
    await waitFor(() => {
      expect(screen.getByTestId('confirm-dialog')).toBeTruthy();
    });
    fireEvent.click(screen.getByTestId('confirm-dialog-confirm'));

    await waitFor(() => {
      expect(deleteCalls.some((u) => u.includes('/sessions/2'))).toBe(true);
    });
    // Must NOT hit /sessions/current, and must not navigate away.
    expect(deleteCalls.some((u) => u.includes('/sessions/current'))).toBe(
      false,
    );
    expect(mockNavigate).not.toHaveBeenCalled();
  });

  // 4d. L7: "sign out other devices" calls DELETE .../sessions/others.
  it('sign-out-other-devices button calls DELETE .../sessions/others', async () => {
    let deleteCalls = [];
    API.get.mockImplementation((url) => {
      if (url.includes('/user/me')) {
        return Promise.resolve({ data: { success: true, data: fakeProfile } });
      }
      if (url.includes('/sessions')) {
        return Promise.resolve(
          fakeSessionsResponse([
            { id: 1, current: true, is_current: true, auth_method: 'session' },
            {
              id: 2,
              current: false,
              is_current: false,
              auth_method: 'session',
            },
          ]),
        );
      }
      return Promise.resolve({ data: { success: false } });
    });
    API.delete.mockImplementation((url) => {
      deleteCalls.push(url);
      return Promise.resolve({ data: { success: true, data: { revoked: 1 } } });
    });

    render(<HFSettings />);
    screen.getByText('Security').click();

    await waitFor(() => {
      expect(screen.getByTestId('revoke-others-btn')).toBeTruthy();
    });
    fireEvent.click(screen.getByTestId('revoke-others-btn'));

    await waitFor(() => {
      expect(screen.getByTestId('confirm-dialog')).toBeTruthy();
    });
    fireEvent.click(screen.getByTestId('confirm-dialog-confirm'));

    await waitFor(() => {
      expect(deleteCalls.some((u) => u.includes('/sessions/others'))).toBe(
        true,
      );
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

  // 6. Subscription tab — reads only the account's routing `group` from
  //    /user/me (cached from mount) and states plainly that no entitlement
  //    terms are published. No fabricated tier/SLA/audit/support numbers,
  //    and no separate call to /user/billing/summary (that stays the
  //    Billing tab's call — see the source lock in Billing/index.test.jsx).
  it('subscription tab shows the literal routing group and an unpublished-entitlements line', async () => {
    API.get.mockImplementation((url) => {
      if (url.includes('/user/me')) {
        return Promise.resolve({
          data: { success: true, data: { ...fakeProfile, group: 'vip' } },
        });
      }
      if (url.includes('/user/billing/summary')) {
        return Promise.resolve({
          data: { success: true, data: fakeBillingSummary },
        });
      }
      return Promise.resolve({ data: { success: false } });
    });

    render(<HFSettings />);

    screen.getByText('Subscription').click();

    await waitFor(() => {
      expect(screen.getByTestId('subscription-section')).toBeTruthy();
    });

    await waitFor(() => {
      expect(screen.getByTestId('subscription-group').textContent).toBe('vip');
    });

    const section = screen.getByTestId('subscription-section');
    for (const fabricated of [
      '99.5',
      '99.95',
      'dedicated',
      'business hours',
      '365',
      'community',
      'Free',
    ]) {
      expect(section.textContent).not.toContain(fabricated);
    }
    // The panel used to call the routing group a "plan"/"套餐" — that claim
    // must not silently come back.
    expect(section.textContent).not.toMatch(/plan|套餐/i);

    expect(
      screen.getByTestId('subscription-entitlements-unpublished'),
    ).toBeTruthy();

    // The caption naming the routing group as the source of the badge value
    // must render — it is the honesty statement this tab exists to make.
    expect(section.textContent).toContain("from your account's routing group");

    const upgradeBtn = screen.getByTestId('subscription-upgrade-btn');
    expect(upgradeBtn.disabled).toBe(true);
    expect(upgradeBtn.title).toMatch(/administrator/i);

    expect(
      API.get.mock.calls.some((c) =>
        String(c[0]).includes('/user/billing/summary'),
      ),
    ).toBe(false);
  });

  // No group on the account → an explicit placeholder, never "Free".
  it('subscription tab shows "no group assigned" when the account has no group', async () => {
    API.get.mockImplementation((url) => {
      if (url.includes('/user/me')) {
        return Promise.resolve({
          data: { success: true, data: { ...fakeProfile, group: '' } },
        });
      }
      return Promise.resolve({ data: { success: false } });
    });

    render(<HFSettings />);

    screen.getByText('Subscription').click();

    await waitFor(() => {
      expect(screen.getByTestId('subscription-group').textContent).toBe(
        'no group assigned',
      );
    });
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

  // 8. This is the page-wide oracle for this cycle's rule: Settings renders
  // zero WIPBanners, in any section — including the restored Notifications
  // section (real form now, not the old 3 hard-coded email/webhook/in-app
  // channels with disabled toggles behind a WIPBanner that this test used
  // to lock).
  it('renders zero WIPBanners in any section, and the old placeholder notification channels are gone', async () => {
    render(<HFSettings />);

    for (const label of [
      'Profile',
      'Security',
      'Subscription',
      'Billing',
      'Notifications',
      'Team & roles',
      'Integrations',
      'Region & data',
      'Danger zone',
    ]) {
      // getAllByText, not getByText: once a nav item's own section is
      // active, its label also appears as the page <h1> title — the nav
      // entry is always first in DOM order (left column renders before
      // the right content pane).
      screen.getAllByText(label)[0].click();
      // Wait for the section switch to actually land (the <h1> title is a
      // heading, unlike the nav item, so this cannot resolve against a
      // stale pre-click render) before asserting on what it rendered —
      // otherwise a `waitFor` whose very first synchronous check already
      // sees zero banners (because the PREVIOUS section had none either)
      // resolves without ever re-checking post-click, and the assertion
      // below would pass on a page that had not switched sections yet.
      await waitFor(() => {
        expect(
          screen.getByRole('heading', { level: 1, name: label }),
        ).toBeTruthy();
      });
      expect(screen.queryAllByTestId('wip-banner').length).toBe(0);
    }

    // The old hard-coded 3-channel placeholder toggles are gone — the
    // section itself is back (asserted above via the heading loop and by
    // test 2), just rendering the real form instead.
    expect(screen.queryByTestId('notif-toggle-email')).toBeNull();
    expect(screen.queryByTestId('notif-toggle-webhook')).toBeNull();
    expect(screen.queryByTestId('notif-toggle-inapp')).toBeNull();
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

// ── MFA status (2026-09-03) ─────────────────────────────────────────────────
//
// The panel used to render a green dot and "authenticator app · enabled"
// unconditionally, with the button disabled because "MFA infra pending —
// totp_seed table not yet provisioned". That reason was false: the backend has
// /api/user/totp/{status,enroll,confirm,disable} and the enrolment UI is live
// at /console/personal. So the one security control a user might check before
// trusting us reported protection they did not have.
describe('Settings — MFA reflects real TOTP status', () => {
  const openSecurity = async () => {
    render(<HFSettings />);
    screen.getByText('Security').click();
    await waitFor(() => expect(screen.getByTestId('mfa-status')).toBeTruthy());
  };

  it('reports "not enabled" with no green dot for a user who never enrolled', async () => {
    mockTotpStatus.mockResolvedValue({ enrolled: false, pending: false });

    await openSecurity();

    await waitFor(() => {
      expect(screen.getByTestId('mfa-status').textContent).toContain(
        'not enabled',
      );
    });
    // The green dot is the whole claim; it must not be lit.
    expect(screen.getByTestId('mfa-dot').className).not.toContain('ok');
  });

  it('lights the dot only once enrolment is confirmed', async () => {
    mockTotpStatus.mockResolvedValue({ enrolled: true, pending: false });

    await openSecurity();

    await waitFor(() => {
      expect(screen.getByTestId('mfa-status').textContent).toContain('enabled');
    });
    expect(screen.getByTestId('mfa-dot').className).toContain('ok');
  });

  it('does not treat a pending, unconfirmed enrolment as protection', async () => {
    mockTotpStatus.mockResolvedValue({ enrolled: false, pending: true });

    await openSecurity();

    await waitFor(() => {
      expect(screen.getByTestId('mfa-status').textContent).toContain(
        'not confirmed',
      );
    });
    // A secret was issued but never confirmed — the account is not protected.
    expect(screen.getByTestId('mfa-dot').className).not.toContain('ok');
  });

  it('says the status is unavailable when the call fails, never "enabled"', async () => {
    mockTotpStatus.mockRejectedValue(new Error('boom'));

    await openSecurity();

    await waitFor(() => {
      expect(screen.getByTestId('mfa-status').textContent).toContain(
        'unavailable',
      );
    });
    expect(screen.getByTestId('mfa-dot').className).not.toContain('ok');
  });

  it('offers a working route to the enrolment UI instead of a disabled button', async () => {
    mockTotpStatus.mockResolvedValue({ enrolled: false, pending: false });

    await openSecurity();

    const btn = await screen.findByTestId('mfa-manage');
    // The old button was permanently disabled behind a false reason.
    expect(btn.disabled).toBe(false);
    btn.click();
    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith('/console/personal');
    });
  });
});
