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

beforeEach(() => {
  API.get.mockReset();
  API.post.mockReset();
  showError.mockReset();
  window.localStorage.clear();
  window.localStorage.setItem('tenant_slug', 'acme');
});

describe('Billing page', () => {
  // 1. On mount, fetches invoices + summary; rendered content reflects mock data.
  it('fetches invoices + summary on mount', async () => {
    // Use mockResolvedValue (sticky) so repeated calls due to slug re-initialisation
    // from localStorage all resolve successfully without throwing.
    API.get.mockImplementation((url) => {
      if (url.includes('/billing/invoices')) {
        return Promise.resolve({
          data: { success: true, data: { items: fakeInvoices } },
        });
      }
      return Promise.resolve({ data: { success: true, data: fakeSummary } });
    });

    render(<HFBilling />);

    // Wait until the acme-slug fetch has been called (may fire twice due to
    // slug state initialising from localStorage asynchronously).
    await waitFor(() => {
      const calls = API.get.mock.calls.map(([url]) => url);
      expect(calls).toContain('/api/v2/acme/billing/invoices');
    });

    const calls = API.get.mock.calls.map(([url]) => url);
    expect(calls).toContain('/api/v2/user/billing/summary');

    // Invoice row for 2026-05 should appear in the DOM.
    await waitFor(() => {
      expect(screen.getByText('2026-05')).toBeTruthy();
    });
  });

  // 2. Clicking recharge calls POST /api/v2/user/billing/checkout and on
  //    success navigates to checkout_url via window.location.href.
  it('navigates to checkout on recharge click', async () => {
    API.get.mockResolvedValue({ data: { success: true, data: { items: [] } } });
    API.post.mockResolvedValueOnce({
      data: {
        success: true,
        data: { checkout_url: 'https://pay.example.com/order/123' },
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

    const btn = screen.getByTestId('billing-recharge');
    fireEvent.click(btn);

    await waitFor(() => {
      expect(API.post).toHaveBeenCalledTimes(1);
    });

    const [url] = API.post.mock.calls[0];
    expect(url).toBe('/api/v2/user/billing/checkout');

    await waitFor(() => {
      expect(navigatedTo).toBe('https://pay.example.com/order/123');
    });

    window.location = originalLocation;
  });

  // 3. PDF download buttons are disabled with scope-cut tooltip; edit payment
  //    is now a real link to identity.lurus.cn/wallet.
  it('PDF buttons disabled; payment is a link to identity.lurus.cn', async () => {
    API.get
      .mockResolvedValueOnce({
        data: { success: true, data: { items: fakeInvoices } },
      })
      .mockResolvedValueOnce({ data: { success: true, data: fakeSummary } });

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
