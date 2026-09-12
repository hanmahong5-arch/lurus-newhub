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
  API: { get: vi.fn() },
}));

vi.mock('../../../components/hifi/HFShell', () => ({
  default: ({ children }) =>
    React.createElement('div', { 'data-testid': 'hf-shell' }, children),
}));

import HFRankings from './Rankings';
import { API } from '../../../helpers';

const payload = (over = {}) => ({
  data: {
    success: true,
    data: {
      by: 'model',
      window: { start: 1000, end: 2000 },
      cached_at: 1234567890,
      rows: [
        {
          name: 'gpt-4o',
          rank: 1,
          rank_delta: 1,
          is_new: false,
          requests: 30,
          total_tokens: 900,
          quota: 4_500_000,
          token_share_pct: 75,
          quota_share_pct: 75,
          requests_growth_pct: 20,
        },
        {
          name: 'deepseek-chat',
          rank: 2,
          rank_delta: 0,
          is_new: true,
          requests: 10,
          total_tokens: 300,
          quota: 1_500_000,
          token_share_pct: 25,
          quota_share_pct: 25,
          requests_growth_pct: null,
        },
      ],
      ...over,
    },
  },
});

beforeEach(() => {
  API.get.mockReset();
  window.localStorage.clear();
  window.localStorage.setItem('tenant_slug', 'acme');
});

describe('Rankings page', () => {
  it('renders rank/trend/share rows from the mocked payload (not literals)', async () => {
    API.get.mockResolvedValue(payload());
    render(<HFRankings />);

    await waitFor(() => {
      expect(screen.getAllByTestId('rankings-row')).toHaveLength(2);
    });
    expect(screen.getByText('gpt-4o')).toBeInTheDocument();
    expect(screen.getByText('deepseek-chat')).toBeInTheDocument();
    // Top row moved up 1 (rank_delta=1) -> ▲1; second row is_new -> "new".
    expect(screen.getByText('▲1')).toBeInTheDocument();
    expect(screen.getByText('new')).toBeInTheDocument();
  });

  it('calls the tenant-scoped rankings endpoint with the resolved slug', async () => {
    API.get.mockResolvedValue(payload());
    render(<HFRankings />);

    await waitFor(() => {
      expect(API.get).toHaveBeenCalled();
    });
    const [url] = API.get.mock.calls[0];
    expect(url).toBe('/api/v2/acme/analytics/rankings?by=model&hours=24');
  });

  it('switching to the vendor tab re-fetches with by=vendor', async () => {
    API.get.mockResolvedValue(payload({ by: 'vendor' }));
    render(<HFRankings />);
    await waitFor(() => screen.getByTestId('rankings-by-vendor'));

    fireEvent.click(screen.getByTestId('rankings-by-vendor'));

    await waitFor(() => {
      const lastCall = API.get.mock.calls[API.get.mock.calls.length - 1];
      expect(lastCall[0]).toContain('by=vendor');
    });
  });

  it('renders the forbidden panel on a 403', async () => {
    API.get.mockRejectedValue({ response: { status: 403 } });
    render(<HFRankings />);

    await waitFor(() => {
      expect(screen.getByText(/Admin access required/i)).toBeInTheDocument();
    });
  });

  it('renders the empty state when rows is empty', async () => {
    API.get.mockResolvedValue(payload({ rows: [] }));
    render(<HFRankings />);

    await waitFor(() => {
      expect(screen.getByTestId('rankings-empty')).toBeInTheDocument();
    });
  });
});
