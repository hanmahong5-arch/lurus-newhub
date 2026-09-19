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
  showError: vi.fn(),
  showSuccess: vi.fn(),
}));

vi.mock('../../../../helpers/formatting', () => ({
  getQuotaPerUSD: () => 500000,
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

// Mirror i18next's en behaviour: return the English defaultValue with
// {{var}} interpolation, so a count assertion cannot pass against the literal
// "{{count}}".
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

import HFModelPerformance from './index';
import { API } from '../../../../helpers';

/*
 * This page is what an operator opens during an incident, and until cycle 12
 * it answered every failure with numbers. The catch recognised 403 and
 * nothing else: on a 502, a dropped connection, or a 200 carrying
 * success:false, `models` stayed [] and loading went false, so the summary
 * cards rendered "0" total requests and the table rendered "no traffic in
 * this window" — a clean bill of health produced by a call that never
 * arrived. Each failure case below asserts BOTH halves: the error state is
 * visible AND the zeroes are not.
 */

const TENANTS_URL = '/api/v2/admin/tenants?page_size=100';

const perf = (models) => ({
  data: {
    success: true,
    data: {
      models,
      start_time: 1_700_000_000,
      end_time: 1_700_086_400,
    },
  },
});

const MODELS = [
  {
    model_name: 'alpha-1',
    requests: 120,
    errors: 6,
    error_rate: 0.05,
    prompt_tokens: 1000,
    completion_tokens: 500,
    quota: 500000,
    avg_latency_ms: 210,
    p50_latency_ms: 190,
    p95_latency_ms: 400,
    latency_samples: 114,
  },
];

// The tenant filter is a separate, non-fatal call; give it an empty list and
// let each test decide what the analytics call does.
const routeGet = (analyticsOutcome) =>
  vi.fn((url) => {
    if (url === TENANTS_URL) {
      return Promise.resolve({
        data: { success: true, data: { tenants: [] } },
      });
    }
    return typeof analyticsOutcome === 'function'
      ? analyticsOutcome()
      : analyticsOutcome;
  });

const rejectWith = (err) => () => Promise.reject(err);
const resolveWith = (res) => () => Promise.resolve(res);

beforeEach(() => {
  API.get.mockReset();
});

describe('Admin model performance page', () => {
  it('renders the summary and a per-model row on a successful fetch', async () => {
    API.get.mockImplementation(routeGet(resolveWith(perf(MODELS))));

    render(<HFModelPerformance />);

    await waitFor(() => screen.getByTestId('perf-row'));
    expect(screen.getByText('alpha-1')).toBeTruthy();
    // 120 requests reaches the summary card, so the "no 0" assertions below
    // are about the failure branch and not about an empty fixture.
    expect(screen.getByTestId('hf-shell').textContent).toContain('120');
    expect(screen.queryByTestId('perf-error')).toBeNull();
  });

  it('shows an error state, not 0 requests, when the endpoint 502s', async () => {
    API.get.mockImplementation(
      routeGet(rejectWith({ response: { status: 502 } })),
    );

    render(<HFModelPerformance />);

    await waitFor(() => screen.getByTestId('perf-error'));
    expect(screen.getByTestId('perf-headline').textContent).toContain(
      'unknown',
    );
    expect(screen.queryByTestId('perf-row')).toBeNull();
    expect(screen.queryByText('no traffic in this window')).toBeNull();
    expect(screen.queryByText('total requests')).toBeNull();
    expect(screen.getByTestId('perf-retry')).toBeTruthy();
    // The CSV export would download the same unreadable window.
    expect(screen.queryByTestId('perf-export-btn')).toBeNull();
  });

  it('shows an error state when the network call rejects with no response', async () => {
    API.get.mockImplementation(
      routeGet(rejectWith(new Error('Network Error'))),
    );

    render(<HFModelPerformance />);

    await waitFor(() => screen.getByTestId('perf-error'));
    expect(screen.queryByText('total requests')).toBeNull();
  });

  it('shows an error state, with the message, on a 200 carrying success:false', async () => {
    API.get.mockImplementation(
      routeGet(
        resolveWith({
          data: { success: false, message: 'analytics store unavailable' },
        }),
      ),
    );

    render(<HFModelPerformance />);

    await waitFor(() => screen.getByTestId('perf-error'));
    expect(screen.getByTestId('perf-error').textContent).toContain(
      'analytics store unavailable',
    );
    expect(screen.queryByText('total requests')).toBeNull();
  });

  it('shows the permission panel on 403 and no error state', async () => {
    API.get.mockImplementation(
      routeGet(rejectWith({ response: { status: 403 } })),
    );

    render(<HFModelPerformance />);

    await waitFor(() =>
      screen.getByText(
        /You do not have permission to view model performance analytics/,
      ),
    );
    expect(screen.queryByTestId('perf-error')).toBeNull();
    expect(screen.queryByTestId('perf-signed-out')).toBeNull();
  });

  // L4 made the v2 admin group answer 401 UNAUTHENTICATED for a missing or
  // invalid session and 403 PERMISSION_DENIED for a role shortfall. An
  // expired cookie is not a permission problem: telling the operator to
  // "contact a platform administrator" sends them to ask for access they
  // already have.
  it('shows a sign-in-again state on 401, not the permission panel', async () => {
    API.get.mockImplementation(
      routeGet(rejectWith({ response: { status: 401 } })),
    );

    render(<HFModelPerformance />);

    await waitFor(() => screen.getByTestId('perf-signed-out'));
    expect(screen.getByTestId('perf-sign-in').getAttribute('href')).toBe(
      '/login',
    );
    expect(
      screen.queryByText(
        /You do not have permission to view model performance analytics/,
      ),
    ).toBeNull();
    expect(screen.queryByText('total requests')).toBeNull();
  });

  it('retry re-runs the fetch and leaves the error state once it succeeds', async () => {
    let fail = true;
    API.get.mockImplementation(
      routeGet(() =>
        fail
          ? Promise.reject({ response: { status: 502 } })
          : Promise.resolve(perf(MODELS)),
      ),
    );

    render(<HFModelPerformance />);
    await waitFor(() => screen.getByTestId('perf-error'));

    fail = false;
    fireEvent.click(screen.getByTestId('perf-retry'));

    await waitFor(() => screen.getByTestId('perf-row'));
    expect(screen.queryByTestId('perf-error')).toBeNull();
    expect(screen.getByText('alpha-1')).toBeTruthy();
  });
});
