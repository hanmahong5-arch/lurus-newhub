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
} from '@testing-library/react';

vi.mock('../../../helpers', () => ({
  API: { get: vi.fn() },
  showError: vi.fn(),
  showSuccess: vi.fn(),
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

// Mirror i18next's en behaviour: return the English defaultValue with
// {{var}} interpolation. Without this the real (uninitialised) i18n returns
// the raw default, so a count assertion would silently pass against
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

import V2AdminSystemTasks from './SystemTasks';
import { API } from '../../../helpers';

const tasksResponse = (tasks, extra = {}) => ({
  data: {
    success: true,
    data: {
      is_leader: true,
      pod: 'newhub-abc123',
      tasks,
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

describe('Admin background-task heartbeats page', () => {
  it('renders each task row with interval, leader-only and state', async () => {
    API.get.mockResolvedValue(
      tasksResponse([
        {
          name: 'audit-cleanup',
          interval_seconds: 86400,
          leader_only: true,
          last_success_at: 1700000000,
          state: 'ok',
        },
      ]),
    );

    render(<V2AdminSystemTasks />);

    await waitFor(() => screen.getByTestId('system-tasks-row-audit-cleanup'));
    expect(
      screen.getByTestId('system-tasks-state-audit-cleanup').textContent,
    ).toBe('ok');
    const row = screen.getByTestId('system-tasks-row-audit-cleanup');
    expect(row.textContent).toContain('audit-cleanup');
    expect(row.textContent).toContain('24h');
  });

  it('headlines the number of overdue tasks', async () => {
    API.get.mockResolvedValue(
      tasksResponse([
        {
          name: 'privacy-erasure',
          interval_seconds: 60,
          leader_only: true,
          last_success_at: 0,
          state: 'overdue',
        },
        {
          name: 'secret-rotation',
          interval_seconds: 86400,
          leader_only: true,
          last_success_at: 1700000000,
          state: 'ok',
        },
      ]),
    );

    render(<V2AdminSystemTasks />);

    await waitFor(() =>
      expect(screen.getByTestId('system-tasks-headline').textContent).toContain(
        '1',
      ),
    );
    expect(
      screen.getByTestId('system-tasks-state-privacy-erasure').className,
    ).toMatch(/error/);
  });

  it('gives a standby task a neutral (non-error) badge', async () => {
    API.get.mockResolvedValue(
      tasksResponse([
        {
          name: 'credit-pool-reconcile',
          interval_seconds: 300,
          leader_only: true,
          last_success_at: 0,
          state: 'standby',
        },
      ]),
    );

    render(<V2AdminSystemTasks />);

    await waitFor(() =>
      screen.getByTestId('system-tasks-state-credit-pool-reconcile'),
    );
    const badge = screen.getByTestId(
      'system-tasks-state-credit-pool-reconcile',
    );
    expect(badge.textContent).toBe('standby');
    expect(badge.className).not.toMatch(/error/);
  });

  it('shows the standby reason next to a follower badge', async () => {
    API.get.mockResolvedValue(
      tasksResponse([
        {
          name: 'audit-cleanup',
          interval_seconds: 86400,
          leader_only: true,
          last_success_at: 0,
          state: 'standby',
          standby_reason: 'follower',
        },
      ]),
    );

    render(<V2AdminSystemTasks />);

    await waitFor(() =>
      screen.getByTestId('system-tasks-reason-audit-cleanup'),
    );
    expect(
      screen.getByTestId('system-tasks-reason-audit-cleanup').textContent,
    ).toContain('follower');
  });

  it('shows the standby reason next to a disabled badge, with a neutral (non-error) class', async () => {
    API.get.mockResolvedValue(
      tasksResponse([
        {
          name: 'channel-health-test',
          interval_seconds: 600,
          leader_only: false,
          last_success_at: 0,
          state: 'standby',
          standby_reason: 'disabled',
        },
      ]),
    );

    render(<V2AdminSystemTasks />);

    await waitFor(() =>
      screen.getByTestId('system-tasks-state-channel-health-test'),
    );
    const badge = screen.getByTestId('system-tasks-state-channel-health-test');
    expect(badge.className).not.toMatch(/error/);
    expect(
      screen.getByTestId('system-tasks-reason-channel-health-test').textContent,
    ).toContain('disabled');
  });

  // The caveat is load-bearing: without it a "standby" row reads as broken
  // when it is the expected state for a leader-gated task on a follower.
  it('states the per-pod caveat whenever the table is shown', async () => {
    API.get.mockResolvedValue(tasksResponse([]));

    render(<V2AdminSystemTasks />);

    await waitFor(() => screen.getByTestId('system-tasks-empty'));
    expect(screen.getByTestId('system-tasks-caveat').textContent).toMatch(
      /per-pod/i,
    );
  });

  it('polls while live and stops once paused', async () => {
    vi.useFakeTimers();
    API.get.mockResolvedValue(tasksResponse([]));

    render(<V2AdminSystemTasks />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    const afterMount = API.get.mock.calls.length;

    await act(async () => {
      await vi.advanceTimersByTimeAsync(15000);
    });
    const afterOneTick = API.get.mock.calls.length;
    expect(afterOneTick).toBeGreaterThan(afterMount);

    fireEvent.click(screen.getByTestId('system-tasks-live-toggle'));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(45000);
    });
    expect(API.get.mock.calls.length).toBe(afterOneTick);
  });

  /*
   * Cycle 12. The refusal shape moved, and the failure shape was missing.
   *
   * Until L4, RootJWTAuth's session fallback refused a non-root caller with
   * HTTP 200 {"success":false,...}, so this page read success:false as
   * "forbidden". L4 changed that fallback to answer 403 PERMISSION_DENIED
   * and 401 UNAUTHENTICATED (internal/adapter/middleware/admin_jwt_auth.go,
   * pinned by root_jwt_denial_shape_test.go), which leaves success:false
   * meaning one thing only: the handler itself failed. Reporting that as
   * "Root access required" sends the operator off to request a permission
   * they already hold.
   *
   * And every other failure — a 502 from a rolling update, a dropped
   * connection — fell through to a null payload, i.e. an empty task list,
   * i.e. a page saying nothing is late at the moment it cannot tell.
   */
  it('shows a permission notice on 403 and stops polling', async () => {
    API.get.mockRejectedValue({ response: { status: 403 } });
    vi.useFakeTimers({ shouldAdvanceTime: true });

    render(<V2AdminSystemTasks />);

    await waitFor(() =>
      screen.getByText(/You do not have permission to view background tasks/),
    );
    expect(screen.queryByTestId('system-tasks-live-toggle')).toBeNull();
    // "stops polling" is the half a missing-toggle assertion cannot see: the
    // refusal must also end the interval, or a forbidden viewer keeps hitting
    // a root-only endpoint every few seconds.
    const afterNotice = API.get.mock.calls.length;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(60000);
    });
    expect(API.get.mock.calls.length).toBe(afterNotice);
  });

  it('shows a sign-in-again state on 401, not the permission panel, and stops polling', async () => {
    API.get.mockRejectedValue({ response: { status: 401 } });
    vi.useFakeTimers({ shouldAdvanceTime: true });

    render(<V2AdminSystemTasks />);

    await waitFor(() => screen.getByTestId('system-tasks-signed-out'));
    expect(
      screen.getByTestId('system-tasks-sign-in').getAttribute('href'),
    ).toBe('/login');
    expect(
      screen.queryByText(/You do not have permission to view background tasks/),
    ).toBeNull();
    expect(screen.queryByText(/all tasks on schedule/)).toBeNull();
    const afterNotice = API.get.mock.calls.length;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(60000);
    });
    expect(API.get.mock.calls.length).toBe(afterNotice);
  });

  it('shows an error state, not "all tasks on schedule", when the endpoint 502s', async () => {
    API.get.mockRejectedValue({ response: { status: 502 } });

    render(<V2AdminSystemTasks />);

    await waitFor(() => screen.getByTestId('system-tasks-error'));
    expect(screen.queryByText(/all tasks on schedule/)).toBeNull();
    expect(screen.queryByTestId('system-tasks-empty')).toBeNull();
    expect(
      screen.queryByText(/You do not have permission to view background tasks/),
    ).toBeNull();
    expect(screen.getByTestId('system-tasks-retry')).toBeTruthy();
  });

  it('shows an error state, with the message, on a 200 carrying success:false', async () => {
    API.get.mockResolvedValue({
      data: { success: false, message: 'task registry unavailable' },
    });

    render(<V2AdminSystemTasks />);

    await waitFor(() => screen.getByTestId('system-tasks-error'));
    expect(screen.getByTestId('system-tasks-error').textContent).toContain(
      'task registry unavailable',
    );
    expect(screen.queryByText(/all tasks on schedule/)).toBeNull();
  });

  // The trade-off the Gateway page pays too: stopping outright means one
  // transient 502 during a rolling update freezes the page until a human
  // notices the retry button. So polling continues, backed off from 15s to
  // 30s, and the error copy says so.
  it('keeps polling in the error state, at the 30s backoff rather than 15s', async () => {
    API.get.mockRejectedValue({ response: { status: 502 } });
    vi.useFakeTimers({ shouldAdvanceTime: true });

    render(<V2AdminSystemTasks />);
    await waitFor(() => screen.getByTestId('system-tasks-error'));
    const afterError = API.get.mock.calls.length;

    // 15s is the healthy interval; nothing may fire before the backoff.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(15000);
    });
    expect(API.get.mock.calls.length).toBe(afterError);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(15000);
    });
    expect(API.get.mock.calls.length).toBeGreaterThan(afterError);
  });

  it('retry re-runs the fetch and leaves the error state once it succeeds', async () => {
    API.get.mockRejectedValue({ response: { status: 502 } });

    render(<V2AdminSystemTasks />);
    await waitFor(() => screen.getByTestId('system-tasks-error'));

    API.get.mockResolvedValue(
      tasksResponse([
        {
          name: 'channel-cache-sync',
          interval_seconds: 60,
          leader_only: false,
          state: 'ok',
          last_success_unix: 1700000000,
        },
      ]),
    );
    fireEvent.click(screen.getByTestId('system-tasks-retry'));

    await waitFor(() =>
      screen.getByTestId('system-tasks-row-channel-cache-sync'),
    );
    expect(screen.queryByTestId('system-tasks-error')).toBeNull();
  });
});
