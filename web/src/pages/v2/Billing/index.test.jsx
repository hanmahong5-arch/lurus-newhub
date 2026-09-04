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
  API: {
    get: vi.fn(),
    post: vi.fn(),
  },
  showError: vi.fn(),
  showSuccess: vi.fn(),
}));

vi.mock('../../../components/hifi/HFShell', () => ({
  default: ({ children, actions }) =>
    React.createElement(
      'div',
      { 'data-testid': 'hf-shell' },
      React.createElement('div', null, actions),
      children,
    ),
}));

// WIPBanner renders inline — keep real implementation so test can assert on it.
vi.mock('../../../components/hifi/WIPBanner', () => ({
  default: ({ reason }) =>
    React.createElement(
      'span',
      { 'data-testid': 'wip-banner', 'data-reason': reason },
      reason,
    ),
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

import HFBilling from './index';
import { API, showError } from '../../../helpers';

const fakeInvoices = [
  {
    month: '2026-05',
    quota: 8420400,
    amount_cny: 16.84,
    amount_usd: 16.84,
    request_count: 120,
  },
  {
    month: '2026-04',
    quota: 9842200,
    amount_cny: 19.68,
    amount_usd: 19.68,
    request_count: 200,
  },
];
const fakeSummary = {
  wallet_balance_cny: 1210.5,
  mtd_spend_cny: 16.84,
  subscription_plan: 'pro',
};

const fakePaymentMethods = [
  { id: 'alipay', name: 'Alipay', provider: 'alipay', type: 'redirect' },
  { id: 'wechat', name: 'WeChat Pay', provider: 'wechat', type: 'qr' },
];

// Shared API.get mock: routes invoices / summary / payment-methods by URL so
// each test only has to override the branch it cares about.
const mockGetDefaults = ({
  invoices = fakeInvoices,
  summary = fakeSummary,
  methods = fakePaymentMethods,
  summaryRejects = false,
  methodsRejects = false,
} = {}) => {
  API.get.mockImplementation((url) => {
    if (url.includes('/billing/invoices')) {
      return Promise.resolve({
        data: { success: true, data: { items: invoices } },
      });
    }
    if (url.includes('/billing/payment-methods')) {
      return methodsRejects
        ? Promise.reject(new Error('network error'))
        : Promise.resolve({ data: { success: true, data: methods } });
    }
    if (url.includes('/billing/summary')) {
      return summaryRejects
        ? Promise.reject(new Error('503 Service Unavailable'))
        : Promise.resolve({ data: { success: true, data: summary } });
    }
    return Promise.resolve({ data: { success: true, data: {} } });
  });
};

beforeEach(() => {
  API.get.mockReset();
  API.post.mockReset();
  showError.mockReset();
  window.localStorage.clear();
  window.localStorage.setItem('tenant_slug', 'acme');
});

describe('Billing page', () => {
  // 1. On mount, fetches invoices + summary + payment methods; rendered
  //    content reflects mock data.
  it('fetches invoices + summary + payment methods on mount', async () => {
    // mockImplementation (sticky) so repeated calls due to slug
    // re-initialisation from localStorage all resolve successfully.
    mockGetDefaults();

    render(<HFBilling />);

    // Wait until the acme-slug fetch has been called (may fire twice due to
    // slug state initialising from localStorage asynchronously).
    await waitFor(() => {
      const calls = API.get.mock.calls.map(([url]) => url);
      expect(calls).toContain('/api/v2/acme/billing/invoices');
    });

    const calls = API.get.mock.calls.map(([url]) => url);
    expect(calls).toContain('/api/v2/user/billing/summary');
    expect(calls).toContain('/api/v2/user/billing/payment-methods');

    // Invoice row for 2026-05 should appear in the DOM.
    await waitFor(() => {
      expect(screen.getByText('2026-05')).toBeTruthy();
    });

    // Server-driven payment methods rendered (not the old hardcoded Alipay).
    await waitFor(() => {
      expect(screen.getByTestId('billing-method-alipay')).toBeTruthy();
      expect(screen.getByTestId('billing-method-wechat')).toBeTruthy();
    });
  });

  // 2. Clicking recharge calls POST /api/v2/user/billing/checkout with the
  //    user-selected amount/method and on success navigates to pay_url (the
  //    actual DTO field — checkout_url was never real) via window.location.href.
  it('navigates to checkout on recharge click, using selected amount + method', async () => {
    mockGetDefaults({ invoices: [] });
    API.post.mockResolvedValueOnce({
      data: {
        success: true,
        data: { pay_url: 'https://pay.example.com/order/123' },
      },
    });

    // Intercept window.location.href assignment without actual navigation.
    const originalLocation = window.location;
    delete window.location;
    let navigatedTo = null;
    window.location = {
      ...originalLocation,
      set href(url) {
        navigatedTo = url;
      },
    };

    render(<HFBilling />);

    // Wait for payment methods to load so a method is selected by default.
    await waitFor(() => {
      expect(screen.getByTestId('billing-method-wechat')).toBeTruthy();
    });

    // User picks a non-default amount preset and payment method.
    fireEvent.click(screen.getByTestId('billing-amount-preset-500'));
    fireEvent.click(screen.getByTestId('billing-method-wechat'));

    const btn = screen.getByTestId('billing-recharge');
    fireEvent.click(btn);

    await waitFor(() => {
      expect(API.post).toHaveBeenCalledTimes(1);
    });

    const [url, body] = API.post.mock.calls[0];
    expect(url).toBe('/api/v2/user/billing/checkout');
    expect(body.amount_cny).toBe(500);
    expect(body.payment_method).toBe('wechat');

    await waitFor(() => {
      expect(navigatedTo).toBe('https://pay.example.com/order/123');
    });

    window.location = originalLocation;
  });

  // 2b. A platform 400 (bad amount / method not configured) surfaces its real
  //     message via showError and must NOT flip the platform-down banner —
  //     that banner is reserved for actual outages (network/5xx).
  it('surfaces the platform 400 message without triggering the platform-down banner', async () => {
    mockGetDefaults({ invoices: [] });
    const err = new Error('Request failed with status code 400');
    err.response = {
      status: 400,
      data: {
        success: false,
        message: 'method not available: provider not configured',
      },
    };
    API.post.mockRejectedValueOnce(err);

    render(<HFBilling />);

    await waitFor(() => {
      expect(screen.getByTestId('billing-method-alipay')).toBeTruthy();
    });

    fireEvent.click(screen.getByTestId('billing-recharge'));

    await waitFor(() => {
      expect(showError).toHaveBeenCalledWith(
        'method not available: provider not configured',
      );
    });
    expect(screen.queryByTestId('billing-platform-down')).toBeNull();
  });

  // 2c. Empty payment-methods list (loaded successfully, but platform has
  //     none configured) renders the honest empty state and disables recharge
  //     instead of a broken/hardcoded-alipay button.
  it('renders an honest empty state when no payment methods are configured', async () => {
    mockGetDefaults({ invoices: [], methods: [] });

    render(<HFBilling />);

    await waitFor(() => {
      expect(screen.getByTestId('billing-no-payment-methods')).toBeTruthy();
    });

    const btn = screen.getByTestId('billing-recharge');
    expect(btn.disabled).toBe(true);

    fireEvent.click(btn);
    expect(API.post).not.toHaveBeenCalled();
  });

  // 3. PDF download buttons are disabled with scope-cut tooltip; edit payment
  //    is now a real link to identity.lurus.cn/wallet.
  it('PDF buttons disabled; payment is a link to identity.lurus.cn', async () => {
    mockGetDefaults();

    render(<HFBilling />);

    await waitFor(() => {
      expect(screen.getByText('2026-05')).toBeTruthy();
    });

    // PDF buttons must be disabled with the scope-cut title.
    const pdfBtn = screen.getByTestId('billing-download-0');
    expect(pdfBtn.disabled).toBe(true);
    expect(pdfBtn.title).toMatch(/Phase 2/i);

    // "manage payment ↗" link must point to identity.lurus.cn.
    const payLink = screen.getByTestId('billing-edit-payment');
    expect(payLink.tagName.toLowerCase()).toBe('a');
    expect(payLink.href).toContain('identity.lurus.cn');
  });

  // 4. Platform summary 503 → honest "billing temporarily unavailable" banner,
  //    while the tenant-local invoices table still renders (allSettled isolation).
  it('shows an honest platform-down banner when billing summary is unreachable', async () => {
    API.get.mockImplementation((url) => {
      if (url.includes('/billing/invoices')) {
        return Promise.resolve({
          data: { success: true, data: { items: fakeInvoices } },
        });
      }
      // summary call rejects like a 503 from the platform billing service
      return Promise.reject(new Error('503 Service Unavailable'));
    });

    render(<HFBilling />);

    await waitFor(() => {
      expect(screen.getByTestId('billing-platform-down')).toBeTruthy();
    });
    expect(screen.getByTestId('billing-platform-down').textContent).toMatch(
      /temporarily unavailable/i,
    );

    // Invoices remain visible despite the summary outage.
    expect(screen.getByText('2026-05')).toBeTruthy();
  });
});

// ── Redeem a code (2026-09-03) ──────────────────────────────────────────────
//
// v2 had no redemption entry point at all. The only one lived on the legacy
// /console/topup shell, which the v2 navigation cannot reach, so a customer
// holding a valid code had nowhere in the console to spend it.
describe('Billing — redeem a code', () => {
  const CODE = 'a'.repeat(32);

  const renderReady = async () => {
    mockGetDefaults();
    render(<HFBilling />);
    await waitFor(() =>
      expect(screen.getByTestId('redeem-input')).toBeTruthy(),
    );
  };

  it('posts the code to the v2 redeem endpoint and shows what was credited', async () => {
    await renderReady();
    API.post.mockResolvedValue({
      data: { success: true, data: { quota_added: 500000 } },
    });

    fireEvent.change(screen.getByTestId('redeem-input'), {
      target: { value: CODE },
    });
    fireEvent.click(screen.getByTestId('redeem-submit'));

    await waitFor(() => {
      expect(API.post).toHaveBeenCalledWith(
        '/api/v2/acme/redeem',
        { key: CODE },
        expect.anything(),
      );
    });
    // quota_added is a quota figure, not a currency amount; it must be
    // converted through the operator's rate exactly like every other number on
    // this page (500000 / 500000 = 1.0000).
    await waitFor(() => {
      expect(screen.getByTestId('redeem-result').textContent).toContain(
        '1.0000',
      );
    });
    // The balance panel above just changed — it must be re-read from the
    // server, not patched with local arithmetic.
    await waitFor(() => {
      const summaryCalls = API.get.mock.calls.filter(([u]) =>
        u.includes('/billing/summary'),
      );
      expect(summaryCalls.length).toBeGreaterThan(1);
    });
  });

  it('will not submit a code of the wrong length', async () => {
    await renderReady();

    fireEvent.change(screen.getByTestId('redeem-input'), {
      target: { value: 'too-short' },
    });

    expect(screen.getByTestId('redeem-submit').disabled).toBe(true);
    // …and says why, inline, rather than letting the user guess.
    expect(screen.getByTestId('redeem-hint').textContent).toContain('9/32');

    fireEvent.click(screen.getByTestId('redeem-submit'));
    expect(API.post).not.toHaveBeenCalled();
  });

  it('surfaces the server message when the code is rejected', async () => {
    await renderReady();
    API.post.mockResolvedValue({
      data: { success: false, message: 'invalid redemption code' },
    });

    fireEvent.change(screen.getByTestId('redeem-input'), {
      target: { value: CODE },
    });
    fireEvent.click(screen.getByTestId('redeem-submit'));

    await waitFor(() => {
      expect(showError).toHaveBeenCalledWith('invalid redemption code');
    });
    // No credited figure may appear for a rejected code.
    expect(screen.queryByTestId('redeem-result')).toBeNull();
  });
});
