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
import { render, screen, waitFor } from '@testing-library/react';

// ─── mocks ───────────────────────────────────────────────────────────────────

vi.mock('../../../helpers', () => ({
  // The onboarding curl block resolves the relay host at render time.
  getServerAddress: () => 'https://hub.example.test',
  API: {
    get: vi.fn(),
  },
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

// ─── fixtures ────────────────────────────────────────────────────────────────

// Backend /api/v2/:slug/user/me response — all snake_case keys
const makeMe = (overrides = {}) => ({
  username: 'testuser',
  display_name: 'Test User',
  used_quota: 250000, // snake_case — NOT usedQuota
  remaining_quota: 750000, // snake_case — NOT remainingQuota
  token_count: 3, // snake_case — NOT tokenCount
  request_count: 42, // snake_case — NOT requestCount
  ...overrides,
});

// Backend /api/v2/:slug/logs response — items use snake_case keys
const makeLog = (overrides = {}) => ({
  type: 2,
  model_name: 'gpt-4o', // snake_case — NOT modelName
  quota: 1000,
  total_latency_ms: 150, // snake_case — NOT totalLatencyMs
  created_at: Math.floor(Date.now() / 1000) - 60, // snake_case — NOT createdAt
  ...overrides,
});

beforeEach(() => {
  API.get.mockReset();
  window.localStorage.clear();
  window.localStorage.setItem('tenant_slug', 'acme');
});

// ─── Dashboard KPI card fixture tests ────────────────────────────────────────

describe('Dashboard page — KPI cards read lowercase keys', () => {
  it('renders total spend from used_quota (snake_case)', async () => {
    API.get.mockResolvedValue({
      data: { success: true, data: makeMe({ used_quota: 500000 }) },
    });

    render(React.createElement(HFDashboard));

    // Total spend KPI: $1.00 from 500000 / 500000 = 1.00
    await waitFor(() => screen.getByText('total spend'));
    await waitFor(() => screen.getByText('$1.00'));
  });

  it('renders active tokens from token_count (snake_case)', async () => {
    API.get.mockResolvedValue({
      data: { success: true, data: makeMe({ token_count: 7 }) },
    });

    render(React.createElement(HFDashboard));

    await waitFor(() => screen.getByText('active tokens'));
    await waitFor(() => screen.getByText('7'));
  });

  it('renders total requests from request_count (snake_case)', async () => {
    API.get.mockResolvedValue({
      data: { success: true, data: makeMe({ request_count: 99 }) },
    });

    render(React.createElement(HFDashboard));

    await waitFor(() => screen.getByText('total requests'));
    await waitFor(() => screen.getByText('99'));
  });

  it('renders remaining quota from remaining_quota (snake_case)', async () => {
    // remaining_quota = 1000000 → $2.00
    API.get.mockResolvedValue({
      data: { success: true, data: makeMe({ remaining_quota: 1000000 }) },
    });

    render(React.createElement(HFDashboard));

    await waitFor(() => screen.getByText('remaining quota'));
    await waitFor(() => screen.getByText('$2.00'));
  });

  it('renders infinity remaining for negative remaining_quota (unlimited plan)', async () => {
    API.get.mockResolvedValue({
      data: { success: true, data: makeMe({ remaining_quota: -1 }) },
    });

    render(React.createElement(HFDashboard));

    await waitFor(() => screen.getByText('remaining quota'));
    await waitFor(() => screen.getByText('∞'));
  });
});

describe('Dashboard page — user/me + logs fetched with correct URLs', () => {
  it('fetches /api/v2/acme/user/me and /api/v2/acme/logs', async () => {
    API.get
      .mockResolvedValueOnce({ data: { success: true, data: makeMe() } })
      .mockResolvedValueOnce({
        // Real backend shape: GET /logs returns { logs: [...] }.
        data: { success: true, data: { logs: [makeLog()] } },
      });

    render(React.createElement(HFDashboard));

    await waitFor(() => {
      const calls = API.get.mock.calls.map((c) => c[0]);
      expect(calls.some((u) => u.includes('/api/v2/acme/user/me'))).toBe(true);
      expect(calls.some((u) => u.includes('/api/v2/acme/logs'))).toBe(true);
    });
  });

  // Regression guard: the dashboard must derive realtime KPIs from the `.logs`
  // array (the real backend key). The prior code read `.items`, so the latency
  // percentiles were always empty against production.
  it('derives realtime latency KPIs from the .logs array', async () => {
    const me = makeMe({ token_count: 3 });
    const now = Math.floor(Date.now() / 1000);
    const logs = [
      makeLog({ type: 2, total_latency_ms: 100, created_at: now - 10 }),
      makeLog({ type: 2, total_latency_ms: 200, created_at: now - 20 }),
    ];
    API.get.mockImplementation((url) => {
      if (url.includes('/user/me')) {
        return Promise.resolve({ data: { success: true, data: me } });
      }
      return Promise.resolve({
        data: { success: true, data: { logs, total: logs.length } },
      });
    });

    render(React.createElement(HFDashboard));

    // p50 of [100, 200] = 150ms — only renders if `.logs` was consumed.
    await waitFor(() => {
      expect(screen.getByText('150ms')).toBeTruthy();
    });
  });

  it('shows display_name (snake_case) in the h1 heading', async () => {
    API.get.mockResolvedValue({
      data: { success: true, data: makeMe({ display_name: 'Alice Smith' }) },
    });

    render(React.createElement(HFDashboard));

    await waitFor(() => screen.getByText(/Alice Smith/));
  });

  it('shows onboarding block when token_count is 0', async () => {
    API.get.mockResolvedValue({
      data: { success: true, data: makeMe({ token_count: 0 }) },
    });

    render(React.createElement(HFDashboard));

    await waitFor(() => screen.getByText(/0 tokens/));
  });

  it('cost-by-model rows deep-link to the log page filtered to that model', async () => {
    const now = Math.floor(Date.now() / 1000);
    API.get.mockImplementation((url) => {
      if (url.includes('/user/me')) {
        return Promise.resolve({ data: { success: true, data: makeMe() } });
      }
      return Promise.resolve({
        data: {
          success: true,
          data: {
            logs: [
              makeLog({
                type: 2,
                model_name: 'model/x-1',
                quota: 5000,
                created_at: now - 10,
              }),
            ],
            total: 1,
          },
        },
      });
    });

    // Capture href assignments without leaving jsdom.
    const prevLocation = window.location;
    let assignedHref = '';
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: {
        ...prevLocation,
        set href(v) {
          assignedHref = v;
        },
        get href() {
          return assignedHref;
        },
      },
    });
    try {
      const { fireEvent } = await import('@testing-library/react');
      render(React.createElement(HFDashboard));

      const row = await waitFor(() => screen.getByTestId('cost-model-row-0'));
      fireEvent.click(row);
      // The '/' in the model name must be encoded, or the link lands on a
      // nonexistent nested route instead of a query param.
      expect(assignedHref).toBe('/console/v2/log?model_name=model%2Fx-1');
    } finally {
      Object.defineProperty(window, 'location', {
        configurable: true,
        value: prevLocation,
      });
    }
  });

  it('does NOT show onboarding when token_count > 0', async () => {
    API.get.mockResolvedValue({
      data: { success: true, data: makeMe({ token_count: 2 }) },
    });

    render(React.createElement(HFDashboard));

    await waitFor(() => screen.queryByText('total spend') !== null);
    expect(screen.queryByText(/0 tokens/)).toBeNull();
  });
});
