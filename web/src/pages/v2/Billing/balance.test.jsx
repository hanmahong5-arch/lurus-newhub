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

// Regression: the balance card read `wallet_balance_cny`, a key the platform
// BillingSummary DTO (internal/pkg/common/identity_client.go) has never
// emitted — it sends `balance` — and the axios success interceptor does no key
// remapping, so the card rendered an em dash on every real response.
import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';

vi.mock('../../../helpers', () => ({
  API: { get: vi.fn(), post: vi.fn() },
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

vi.mock('react-i18next', () => ({
  initReactI18next: { type: '3rdParty', init: () => {} },
  Trans: ({ children }) => children,
  useTranslation: () => ({
    t: (key, fallback) => (typeof fallback === 'string' ? fallback : key),
  }),
}));

import HFBilling from './index';
import { API } from '../../../helpers';

// Exactly the shape GetBillingSummary returns: no wallet_balance_cny anywhere.
const realSummary = {
  balance: 1210.5,
  frozen: 10,
  available: 1200.5,
  lifetime_topup: 5000,
  lifetime_spend: 3789.5,
  active_pre_auths: 1,
  pending_orders: 0,
};

const wire = (summary) => {
  API.get.mockImplementation((url) => {
    if (String(url).includes('/billing/invoices')) {
      return Promise.resolve({
        data: { success: true, data: { items: [] } },
      });
    }
    return Promise.resolve({ data: { success: true, data: summary } });
  });
};

beforeEach(() => {
  API.get.mockReset();
  API.post.mockReset();
  window.localStorage.clear();
  window.localStorage.setItem('tenant_slug', 'acme');
});

describe('Billing page — wallet balance', () => {
  it('renders the balance from the DTO `balance` field', async () => {
    wire(realSummary);

    render(<HFBilling />);

    // Balance card + the side-panel wallet row both read the same value.
    await waitFor(() => {
      expect(screen.getAllByText('¥1,210.50').length).toBe(2);
    });
  });

  it('still honours a legacy wallet_balance_cny producer', async () => {
    wire({ wallet_balance_cny: 42.25 });

    render(<HFBilling />);

    await waitFor(() => {
      expect(screen.getAllByText('¥42.25').length).toBe(2);
    });
  });

  it('shows an em dash when neither key is present', async () => {
    wire({ subscription_plan: 'pro' });

    render(<HFBilling />);

    await waitFor(() => screen.getByText('balance'));
    // The wallet row is omitted entirely; the card falls back to '—'.
    expect(screen.queryByText('wallet')).toBeNull();
    expect(screen.getAllByText('—').length).toBeGreaterThan(0);
  });
});
