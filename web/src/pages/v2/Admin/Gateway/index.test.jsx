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
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';

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

// Mirror i18next's en behaviour: return the English defaultValue with
// {{var}} interpolation. Without this the real (uninitialised) i18n returns the
// raw default, so a count assertion would silently pass against "{{count}}".
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

import HFAdminGateway from './index';
import { API } from '../../../../helpers';

const health = (routes, extra = {}) => ({
  data: {
    success: true,
    data: {
      routes,
      tracked: routes.length,
      open: routes.filter((r) => r.state === 'open').length,
      half_open: routes.filter((r) => r.state === 'half_open').length,
      replica_scoped: true,
      lazy_registered: true,
      ...extra,
    },
  },
});

beforeEach(() => {
  API.get.mockReset();
});

afterEach(() => {
  vi.useRealTimers();
});

describe('Admin Gateway health page', () => {
  it('renders breaker state and failure progress per channel', async () => {
    API.get.mockResolvedValue(
      health([
        {
          channel_id: 3,
          channel_name: 'primary',
          provider: 'openai',
          state: 'closed',
          consecutive_fails: 2,
          threshold: 5,
        },
      ]),
    );

    render(<HFAdminGateway />);

    await waitFor(() => screen.getByTestId('gateway-row-3'));
    const row = within(screen.getByTestId('gateway-row-3'));
    expect(row.getByText('primary')).toBeTruthy();
    // "2/5" is the degrading-but-not-tripped signal.
    expect(row.getByText('2/5')).toBeTruthy();
    expect(screen.getByTestId('gateway-state-3').textContent).toBe('closed');
  });

  it('headlines the number of open breakers', async () => {
    API.get.mockResolvedValue(
      health([
        { channel_id: 1, state: 'open', consecutive_fails: 5, threshold: 5 },
        { channel_id: 2, state: 'closed', consecutive_fails: 0, threshold: 5 },
      ]),
    );

    render(<HFAdminGateway />);

    await waitFor(() =>
      expect(screen.getByTestId('gateway-headline').textContent).toContain('1'),
    );
    // The mocked t() returns the fallback (the raw state); the en resource
    // renders it uppercase. Match case-insensitively so the assertion is about
    // the state reaching the cell, not about copy.
    expect(screen.getByTestId('gateway-state-1').textContent).toMatch(
      /^open$/i,
    );
  });

  it('shows a countdown to the half-open probe for an open breaker', async () => {
    const soon = Math.floor(Date.now() / 1000) + 20;
    API.get.mockResolvedValue(
      health([
        {
          channel_id: 4,
          state: 'open',
          consecutive_fails: 5,
          threshold: 5,
          probe_eligible_unix: soon,
        },
      ]),
    );

    render(<HFAdminGateway />);

    await waitFor(() => screen.getByTestId('gateway-row-4'));
    expect(screen.getByTestId('gateway-row-4').textContent).toMatch(/\d+s/);
  });

  // The caveat is load-bearing: without it an empty table reads as "all healthy"
  // when it actually means "nothing observed yet".
  it('always states the lazy-registration caveat', async () => {
    API.get.mockResolvedValue(health([]));

    render(<HFAdminGateway />);

    await waitFor(() => screen.getByTestId('gateway-empty'));
    expect(screen.getByTestId('gateway-caveat').textContent).toMatch(
      /absent means unknown, not healthy/i,
    );
  });

  it('polls while live and stops once paused', async () => {
    vi.useFakeTimers();
    API.get.mockResolvedValue(health([]));

    render(<HFAdminGateway />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    const afterMount = API.get.mock.calls.length;

    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    const afterOneTick = API.get.mock.calls.length;
    expect(afterOneTick).toBeGreaterThan(afterMount);

    fireEvent.click(screen.getByTestId('gateway-live-toggle'));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(15000);
    });
    expect(API.get.mock.calls.length).toBe(afterOneTick);
  });

  it('shows a permission notice on 403 and stops polling', async () => {
    API.get.mockRejectedValue({ response: { status: 403 } });

    render(<HFAdminGateway />);

    await waitFor(() =>
      screen.getByText(/You do not have permission to read gateway health/),
    );
    expect(screen.queryByTestId('gateway-live-toggle')).toBeNull();
  });
});
