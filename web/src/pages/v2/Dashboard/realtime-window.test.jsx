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

// Regression: the realtime window asked for page_size=200, which GetLogsV2
// (internal/adapter/handler/v2_log.go) rejects by falling back to 20 — so the
// KPIs were computed from 20 rows, not 200. And QPS counted the rows in hand
// rather than the server's `total`, capping the rate at page_size / window.
import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';

vi.mock('../../../helpers', () => ({
  // The onboarding curl block resolves the relay host at render time.
  getServerAddress: () => 'https://hub.example.test',
  API: { get: vi.fn() },
  showError: vi.fn(),
}));

vi.mock('../../../components/hifi/HFShell', () => ({
  default: ({ children, actions }) =>
    React.createElement(
      'div',
      { 'data-testid': 'hf-shell' },
      React.createElement('div', { 'data-testid': 'hf-actions' }, actions),
      children,
    ),
}));

import HFDashboard from './index';
import { API } from '../../../helpers';
import { DASHBOARD_REALTIME_WINDOW_SECONDS } from './kpis';

const makeMe = (overrides = {}) => ({
  username: 'testuser',
  display_name: 'Test User',
  used_quota: 250000,
  remaining_quota: 750000,
  token_count: 3,
  request_count: 42,
  ...overrides,
});

const makeLog = (overrides = {}) => ({
  type: 2,
  model_name: 'gpt-4o',
  quota: 1000,
  total_latency_ms: 150,
  created_at: Math.floor(Date.now() / 1000) - 60,
  ...overrides,
});

const wire = (logsPayload) => {
  API.get.mockImplementation((url) => {
    if (String(url).includes('/user/me')) {
      return Promise.resolve({ data: { success: true, data: makeMe() } });
    }
    return Promise.resolve({ data: { success: true, data: logsPayload } });
  });
};

beforeEach(() => {
  API.get.mockReset();
  window.localStorage.clear();
  window.localStorage.setItem('tenant_slug', 'acme');
});

describe('Dashboard — realtime window request', () => {
  it('asks for a page size the logs endpoint actually accepts', async () => {
    wire({ logs: [makeLog()], total: 1 });

    render(<HFDashboard />);

    await waitFor(() => {
      const logsURL = API.get.mock.calls
        .map(([u]) => String(u))
        .find((u) => u.includes('/logs'));
      expect(logsURL).toBeTruthy();
      const pageSize = Number(
        new URLSearchParams(logsURL.split('?')[1]).get('page_size'),
      );
      // v2_log.go: anything >100 silently becomes 20.
      expect(pageSize).toBeGreaterThan(0);
      expect(pageSize).toBeLessThanOrEqual(100);
    });
  });

  it('derives QPS from the server total, not the page it received', async () => {
    // 900 requests in the 300s window = 3 QPS, reported by `total`, while the
    // page only carries 2 rows.
    const total = 3 * DASHBOARD_REALTIME_WINDOW_SECONDS;
    wire({ logs: [makeLog(), makeLog()], total });

    render(<HFDashboard />);

    await waitFor(() => screen.getByText('qps'));
    // formatQPS(3) → '3.0'; the page-derived value would be '<0.01'.
    await waitFor(() => {
      expect(screen.getByText('3.0')).toBeTruthy();
    });
  });

  // Same money-unit class as the Token page: cost-by-model divided by the
  // hardcoded default while every other figure on the page used the live rate.
  it('prices cost-by-model with the live quota_per_unit', async () => {
    window.localStorage.setItem('quota_per_unit', '1000');
    wire({ logs: [makeLog({ quota: 2500 })], total: 1 });

    render(<HFDashboard />);

    // 2500 / 1000 = $2.5000; the default divisor would render $0.0050.
    await waitFor(() => {
      expect(screen.getByText(/\$2\.5000/)).toBeTruthy();
    });
  });

  it('falls back to the row count when the server sends no total', async () => {
    wire({ logs: [makeLog(), makeLog(), makeLog()] });

    render(<HFDashboard />);

    await waitFor(() => screen.getByText('qps'));
    // 3 / 300 = 0.01 → formatQPS → '0.01'.
    await waitFor(() => {
      expect(screen.getByText('0.01')).toBeTruthy();
    });
  });
});
