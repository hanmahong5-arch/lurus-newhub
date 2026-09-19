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
  API: { get: vi.fn() },
}));

vi.mock('../../../../components/hifi/HFShell', () => ({
  default: ({ children }) =>
    React.createElement('div', { 'data-testid': 'hf-shell' }, children),
}));

vi.mock('../../../../components/hifi/NotAvailable', () => ({
  default: ({ reason }) =>
    React.createElement('div', { 'data-testid': 'not-available' }, reason),
}));

import HFCostIntelligence from './index';
import { API } from '../../../../helpers';

// quota_per_usd = 500000 → $5.00 conservative, $8.00 aggressive.
const payload = (over = {}) => ({
  data: {
    success: true,
    data: {
      window_hours: 168,
      quota_per_usd: 500000,
      heuristic: true,
      caveat: 'aggregate estimate, conservative on cache; tiers heuristic',
      total_spend_quota: 50000000,
      spend_by_model: [
        { model_name: 'gpt-4o', count: 10, total_quota: 30000000 },
      ],
      spend_by_product: [
        { product: 'lurus-api', count: 8, total_quota: 25000000 },
        { product: 'kova', count: 2, total_quota: 5000000 },
      ],
      scenarios: {
        conservative: {
          total_savings_quota: 2500000,
          savings_pct: 0.05,
          top_opportunities: [
            {
              model: 'gpt-4o',
              alt_model: 'gpt-4o-mini',
              savings_quota: 2500000,
              savings_pct: 0.08,
            },
          ],
        },
        aggressive: {
          total_savings_quota: 4000000,
          savings_pct: 0.08,
          top_opportunities: [
            {
              model: 'gpt-4o',
              alt_model: 'deepseek-chat',
              savings_quota: 4000000,
              savings_pct: 0.13,
            },
          ],
        },
      },
      excluded: [{ model: 'dall-e-3', quota: 100, reason: 'per_call_priced' }],
      ...over,
    },
  },
});

beforeEach(() => {
  API.get.mockReset();
});

describe('Cost Intelligence savings page', () => {
  it('headline reflects the mocked payload (not a literal)', async () => {
    API.get.mockResolvedValue(payload());
    render(<HFCostIntelligence />);

    await waitFor(() => {
      // 2_500_000 / 500_000 = $5.00 (conservative default).
      expect(screen.getByTestId('savings-headline').textContent).toBe('$5.00');
    });
  });

  it('renders the heuristic caveat banner', async () => {
    API.get.mockResolvedValue(payload());
    render(<HFCostIntelligence />);
    await waitFor(() => {
      expect(screen.getByTestId('caveat').textContent).toMatch(/heuristic/i);
    });
  });

  it('toggling scenario switches the headline to the aggressive number', async () => {
    API.get.mockResolvedValue(payload());
    render(<HFCostIntelligence />);
    await waitFor(() => screen.getByTestId('scenario-aggressive'));

    fireEvent.click(screen.getByTestId('scenario-aggressive'));
    await waitFor(() => {
      // 4_000_000 / 500_000 = $8.00.
      expect(screen.getByTestId('savings-headline').textContent).toBe('$8.00');
    });
  });

  it('zero savings renders the explicit no-eligible-savings message', async () => {
    API.get.mockResolvedValue(
      payload({
        total_spend_quota: 0,
        spend_by_model: [],
        spend_by_product: [],
        scenarios: {
          conservative: {
            total_savings_quota: 0,
            savings_pct: 0,
            top_opportunities: [],
          },
          aggressive: {
            total_savings_quota: 0,
            savings_pct: 0,
            top_opportunities: [],
          },
        },
        excluded: [],
      }),
    );
    render(<HFCostIntelligence />);
    await waitFor(() => {
      expect(screen.getByTestId('savings-headline').textContent).toBe(
        '$0.00 — no eligible savings',
      );
    });
  });

  /*
   * Cycle 12 L3. The catch tested for 403 and nothing else, so a 502 or a
   * dropped connection left `data` null and the page rendered NotAvailable
   * with "savings endpoint returned no data" — which says the endpoint
   * answered and had nothing to report. It did not answer. An operator
   * reading that concludes there are no savings to be had, and stops looking.
   */
  it('shows an error state with a retry, not "returned no data", on a 502', async () => {
    API.get.mockRejectedValue({ response: { status: 502 } });

    render(<HFCostIntelligence />);

    await waitFor(() => screen.getByTestId('cost-error'));
    expect(screen.queryByTestId('not-available')).toBeNull();
    expect(screen.getByTestId('cost-retry')).toBeTruthy();
  });

  it('shows an error state when the call rejects with no response at all', async () => {
    API.get.mockRejectedValue(new Error('Network Error'));

    render(<HFCostIntelligence />);

    await waitFor(() => screen.getByTestId('cost-error'));
    expect(screen.queryByTestId('not-available')).toBeNull();
  });

  it('shows an error state on a 200 that carries success:false', async () => {
    API.get.mockResolvedValue({
      data: { success: false, message: 'savings job has not run yet' },
    });

    render(<HFCostIntelligence />);

    await waitFor(() => screen.getByTestId('cost-error'));
    expect(screen.getByTestId('cost-error').textContent).toContain(
      'savings job has not run yet',
    );
  });

  it('retry refetches and clears the error state', async () => {
    API.get.mockRejectedValue({ response: { status: 502 } });

    render(<HFCostIntelligence />);
    await waitFor(() => screen.getByTestId('cost-error'));

    API.get.mockResolvedValue(payload());
    fireEvent.click(screen.getByTestId('cost-retry'));

    await waitFor(() =>
      expect(screen.getByTestId('savings-headline').textContent).toBe('$5.00'),
    );
    expect(screen.queryByTestId('cost-error')).toBeNull();
  });

  it('treats 401 as the permission state, like 403', async () => {
    API.get.mockRejectedValue({ response: { status: 401 } });

    render(<HFCostIntelligence />);

    await waitFor(() =>
      screen.getByText(
        /You do not have permission to view platform cost intelligence/,
      ),
    );
    expect(screen.queryByTestId('cost-error')).toBeNull();
  });

  // The pre-existing "no data" path has to survive: a 200 with a null payload
  // is a real answer and must keep reading as one.
  it('keeps the no-data notice for a successful response with no payload', async () => {
    API.get.mockResolvedValue({ data: { success: true, data: null } });

    render(<HFCostIntelligence />);

    await waitFor(() => screen.getByTestId('not-available'));
    expect(screen.getByTestId('not-available').textContent).toContain(
      'savings endpoint returned no data',
    );
    expect(screen.queryByTestId('cost-error')).toBeNull();
  });
});
