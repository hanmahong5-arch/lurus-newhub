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

vi.mock('../../../../helpers', () => ({
  API: { get: vi.fn() },
  showError: vi.fn(),
  showSuccess: vi.fn(),
}));

vi.mock('../../../../components/hifi/HFShell', () => ({
  default: ({ children, actions }) =>
    React.createElement(
      'div',
      { 'data-testid': 'hf-shell' },
      React.createElement('div', { 'data-testid': 'hf-actions' }, actions),
      children,
    ),
}));

// Same English-fallback mirror as ModelPerformance's test.
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key, fallback, opts) => {
      const vars =
        typeof fallback === 'object' && fallback !== null ? fallback : opts;
      let out = typeof fallback === 'string' ? fallback : key;
      if (vars) {
        for (const [k, v] of Object.entries(vars)) {
          out = out.split('{{' + k + '}}').join(String(v));
        }
      }
      return out;
    },
  }),
  initReactI18next: { type: '3rdParty', init: () => {} },
}));

import HFModelPools from './index';
import { API } from '../../../../helpers';

const poolsResponse = (pools) => ({
  data: {
    success: true,
    data: { generated_at: 1700000000, pools },
  },
});

const CHANNEL = {
  id: 1,
  name: 'chan-a',
  type: 1,
  status: 'enabled',
  priority: 10,
  weight: 5,
  tenant_id: 'default',
  test_time: 1699999000,
  response_time: 120,
  balance: 3.5,
  keys: { total: 3, enabled: 1, cooling: 1, disabled: 1 },
  models: [
    {
      model: 'm1',
      requests_24h: 2,
      errors_24h: 1,
      health: {
        ok: true,
        last_probe_at: 1699999900,
        latency_ms: 50,
        last_error: '',
        consecutive_failures: 0,
        auto_disabled: false,
      },
    },
    {
      model: 'm2',
      requests_24h: 0,
      errors_24h: 0,
      health: {
        ok: false,
        last_probe_at: 1699999950,
        latency_ms: 0,
        last_error: 'boom upstream',
        consecutive_failures: 5,
        auto_disabled: true,
      },
    },
    {
      model: 'm3',
      requests_24h: 5,
      errors_24h: 0,
      health: null,
    },
    {
      model: 'm4',
      requests_24h: 1,
      errors_24h: 1,
      health: {
        ok: false,
        last_probe_at: 1699999960,
        latency_ms: 900,
        last_error: 'timeout',
        consecutive_failures: 2,
        auto_disabled: false,
      },
    },
  ],
};

const POOLS = [{ group: 'free', channels: [CHANNEL] }];

const rejectWith = (err) => () => Promise.reject(err);
const resolveWith = (res) => () => Promise.resolve(res);

const routeGet = (outcome) =>
  vi.fn(() => (typeof outcome === 'function' ? outcome() : outcome));

beforeEach(() => {
  API.get.mockReset();
});

describe('Admin model pools page', () => {
  it('renders pool groups, channel header and per-model health badges', async () => {
    API.get.mockImplementation(routeGet(resolveWith(poolsResponse(POOLS))));

    render(<HFModelPools />);

    await waitFor(() => screen.getByText('chan-a'));
    expect(screen.getByTestId('hf-shell').textContent).toContain('free');
    // Key counts.
    expect(screen.getByTestId('hf-shell').textContent).toContain('m1');
    expect(screen.getByTestId('hf-shell').textContent).toContain('m2');
    expect(screen.getByTestId('hf-shell').textContent).toContain('m3');
    expect(screen.getByTestId('hf-shell').textContent).toContain('m4');

    // Health badge variants.
    expect(screen.getByTestId('pool-health-m1').textContent).toContain('ok');
    expect(screen.getByTestId('pool-health-m2').textContent).toContain(
      'auto-disabled',
    );
    expect(screen.getByTestId('pool-health-m3').textContent).toContain(
      'never probed',
    );
    expect(screen.getByTestId('pool-health-m4').textContent).toContain('2');

    // Full error text is available (title attr), not just the truncated cell.
    expect(screen.getByTestId('pool-last-error-m2').title).toBe(
      'boom upstream',
    );
  });

  // cycle-19 L6: requests/errors/latency are the table's number columns —
  // they should carry the shared `num` (right-align + tabular-nums) contract
  // on both header and body cells.
  it('marks the requests/errors/latency columns with the `num` class', async () => {
    API.get.mockImplementation(routeGet(resolveWith(poolsResponse(POOLS))));

    render(<HFModelPools />);
    await waitFor(() => screen.getByText('chan-a'));

    const shell = screen.getByTestId('hf-shell');
    const headers = Array.from(shell.querySelectorAll('th'));
    const numHeaders = headers.filter((th) => th.className.includes('num'));
    expect(numHeaders.map((th) => th.textContent)).toEqual([
      'latency',
      'requests (24h)',
      'errors (24h)',
    ]);

    const requestRow = screen.getByText('m1').closest('tr');
    const numCells = Array.from(requestRow.querySelectorAll('td.num'));
    expect(numCells).toHaveLength(3);
  });

  // cycle-19 L6: "never probed" (no health row yet) must look distinctly
  // different from a passing "ok" probe — before this both were an untoned
  // label indistinguishable from each other.
  it('gives the never-probed badge a distinct title and class/style from the ok badge', async () => {
    API.get.mockImplementation(routeGet(resolveWith(poolsResponse(POOLS))));

    render(<HFModelPools />);
    await waitFor(() => screen.getByText('chan-a'));

    const neverProbed = screen.getByTestId('pool-health-m3');
    const ok = screen.getByTestId('pool-health-m1');

    expect(neverProbed.title).toBe(
      'Not probed yet — health is unknown, not healthy',
    );
    expect(ok.title).toBe('');

    const neverProbedBadge = neverProbed.querySelector(
      '.hf-health-badge--never_probed',
    );
    const okBadge = ok.querySelector('.hf-health-badge--ok');
    expect(neverProbedBadge).not.toBeNull();
    expect(okBadge).not.toBeNull();
    expect(neverProbedBadge.className).not.toBe(okBadge.className);
    expect(neverProbedBadge.style.border).not.toBe(okBadge.style.border);
  });

  it('shows an empty state when there are no pools', async () => {
    API.get.mockImplementation(routeGet(resolveWith(poolsResponse([]))));

    render(<HFModelPools />);

    await waitFor(() => screen.getByTestId('pools-empty'));
    expect(screen.queryByText('chan-a')).toBeNull();
  });

  it('shows an error state, not an empty pools list, when the endpoint 502s', async () => {
    API.get.mockImplementation(
      routeGet(rejectWith({ response: { status: 502 } })),
    );

    render(<HFModelPools />);

    await waitFor(() => screen.getByTestId('pools-error'));
    expect(screen.queryByTestId('pools-empty')).toBeNull();
    expect(screen.queryByText('chan-a')).toBeNull();
    expect(screen.getByTestId('pools-retry')).toBeTruthy();
  });

  it('shows the permission panel on 403', async () => {
    API.get.mockImplementation(
      routeGet(rejectWith({ response: { status: 403 } })),
    );

    render(<HFModelPools />);

    await waitFor(() => screen.getByTestId('pools-forbidden'));
    expect(screen.queryByTestId('pools-error')).toBeNull();
  });

  it('shows a sign-in-again state on 401', async () => {
    API.get.mockImplementation(
      routeGet(rejectWith({ response: { status: 401 } })),
    );

    render(<HFModelPools />);

    await waitFor(() => screen.getByTestId('pools-signed-out'));
  });
});
