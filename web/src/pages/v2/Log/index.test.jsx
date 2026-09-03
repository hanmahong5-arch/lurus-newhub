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

// Mock helpers BEFORE importing the component — vi.mock is hoisted.
// isAdmin defaults to false so the tenant-wide toggle stays hidden unless a
// test opts in via mockIsAdmin.
const mockIsAdmin = vi.fn(() => false);
vi.mock('../../../helpers', () => ({
  API: {
    get: vi.fn(),
    post: vi.fn(),
  },
  showError: vi.fn(),
  showSuccess: vi.fn(),
  isAdmin: (...a) => mockIsAdmin(...a),
}));

// HFShell pulls TenantSwitcher → API helper chain → react-router. Stub to a
// passthrough wrapper so tests focus on Log UI/logic.
vi.mock('../../../components/hifi/HFShell', () => ({
  default: ({ children, actions }) =>
    React.createElement(
      'div',
      { 'data-testid': 'hf-shell' },
      React.createElement('div', null, actions),
      children,
    ),
}));

// Mirror i18next's en behaviour: return the English defaultValue (2nd arg)
// with {{var}} interpolation, falling back to the key when no default given.
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

import HFLog from './index';
import { API, showError } from '../../../helpers';

beforeEach(() => {
  API.get.mockReset();
  API.post.mockReset();
  showError.mockReset();
  mockIsAdmin.mockReset();
  mockIsAdmin.mockReturnValue(false);
  window.localStorage.clear();
  window.localStorage.setItem('tenant_slug', 'acme');

  // Default mock for Recent tab initial fetch (called on mount).
  API.get.mockResolvedValue({
    data: { success: true, data: { logs: [], total: 0 } },
  });
});

afterEach(() => {
  vi.useRealTimers();
});

describe('Log page', () => {
  // 1. Switching to Cluster tab triggers GET /api/v2/acme/logs/cluster?bucket=hour
  it('switches to Cluster tab and fetches with default bucket=hour', async () => {
    API.get.mockImplementation((url) => {
      if (url.includes('/logs/cluster')) {
        return Promise.resolve({
          data: {
            success: true,
            data: { items: [], total: 0, bucket: 'hour' },
          },
        });
      }
      // Recent tab initial fetch
      return Promise.resolve({
        data: { success: true, data: { logs: [], total: 0 } },
      });
    });

    render(<HFLog />);

    // Click the "Error clusters" tab button.
    fireEvent.click(screen.getByText('Error clusters'));

    await waitFor(() => {
      const calls = API.get.mock.calls.map(([url]) => url);
      const clusterCall = calls.find((u) => u.includes('/logs/cluster'));
      expect(clusterCall).toBeDefined();
      expect(clusterCall).toContain('bucket=hour');
    });
  });

  // 2. Cluster rows are rendered sorted by count descending.
  it('renders cluster rows sorted by count descending', async () => {
    const mockItems = [
      {
        model_name: 'gpt-4o',
        error_code: 'ERR',
        bucket: '2023-11-15 12:00',
        count: 10,
      },
      {
        model_name: 'claude',
        error_code: '',
        bucket: '2023-11-15 12:00',
        count: 50,
      },
      {
        model_name: 'gemini',
        error_code: '500',
        bucket: '2023-11-15 12:00',
        count: 5,
      },
    ];

    API.get.mockImplementation((url) => {
      if (url.includes('/logs/cluster')) {
        return Promise.resolve({
          data: {
            success: true,
            data: { items: mockItems, total: 3, bucket: 'hour' },
          },
        });
      }
      return Promise.resolve({
        data: { success: true, data: { logs: [], total: 0 } },
      });
    });

    render(<HFLog />);
    fireEvent.click(screen.getByText('Error clusters'));

    await waitFor(() => {
      expect(screen.getByTestId('cluster-table')).toBeDefined();
    });

    const rows = screen
      .getByTestId('cluster-table')
      .querySelectorAll('tbody tr');
    // Rows should be sorted by count DESC: 50, 10, 5
    // The count cells are the 4th td in each row.
    const counts = Array.from(rows).map((r) =>
      Number(r.querySelectorAll('td')[3].textContent),
    );
    expect(counts[0]).toBeGreaterThanOrEqual(counts[1]);
    expect(counts[1]).toBeGreaterThanOrEqual(counts[2]);
    // First row must be the highest count (claude / 50).
    expect(counts[0]).toBe(50);
  });

  // 3. Live tail seeds then polls with after_id + disableDuplicate, prepending
  //    newly-arrived rows on top of the buffer.
  it('seeds then polls with after_id + disableDuplicate and prepends new rows', async () => {
    vi.useFakeTimers();
    const seedLog = {
      id: 10,
      type: 2,
      model_name: 'seed-model',
      created_at: 1700000000,
    };
    const pollLog = {
      id: 11,
      type: 2,
      model_name: 'poll-model',
      created_at: 1700000003,
    };
    API.get.mockImplementation((url) => {
      if (/after_id=/.test(url)) {
        return Promise.resolve({
          data: { success: true, data: { logs: [pollLog], total: 1 } },
        });
      }
      if (/\/logs\/stat/.test(url)) {
        return Promise.resolve({ data: { success: true, data: {} } });
      }
      return Promise.resolve({
        data: { success: true, data: { logs: [seedLog], total: 1 } },
      });
    });

    render(<HFLog />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });

    fireEvent.click(screen.getByText('Live tail'));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    // Seed row is visible immediately (no 3s wait).
    expect(screen.getByText('seed-model')).toBeTruthy();

    // One poll interval elapses.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000);
    });

    const pollCall = API.get.mock.calls.find(
      ([url, cfg]) => /after_id=/.test(url) && cfg?.disableDuplicate,
    );
    expect(pollCall).toBeTruthy();
    // New row prepended to the live buffer.
    expect(screen.getByText('poll-model')).toBeTruthy();

    vi.useRealTimers();
  });

  // 3b. Pausing stops further polling.
  it('pause stops further polls', async () => {
    vi.useFakeTimers();
    const seedLog = {
      id: 5,
      type: 2,
      model_name: 'seed',
      created_at: 1700000000,
    };
    API.get.mockImplementation((url) => {
      if (/after_id=/.test(url)) {
        return Promise.resolve({
          data: { success: true, data: { logs: [], total: 0 } },
        });
      }
      if (/\/logs\/stat/.test(url)) {
        return Promise.resolve({ data: { success: true, data: {} } });
      }
      return Promise.resolve({
        data: { success: true, data: { logs: [seedLog], total: 1 } },
      });
    });

    render(<HFLog />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    fireEvent.click(screen.getByText('Live tail'));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });

    fireEvent.click(screen.getByTestId('live-pause-btn'));
    const before = API.get.mock.calls.filter(([url]) =>
      /after_id=/.test(url),
    ).length;

    await act(async () => {
      await vi.advanceTimersByTimeAsync(9000);
    });
    const after = API.get.mock.calls.filter(([url]) =>
      /after_id=/.test(url),
    ).length;
    expect(after).toBe(before);

    vi.useRealTimers();
  });

  // 3c. Unmounting clears the polling interval.
  it('unmount clears the polling interval', async () => {
    vi.useFakeTimers();
    const seedLog = {
      id: 5,
      type: 2,
      model_name: 'seed',
      created_at: 1700000000,
    };
    API.get.mockImplementation((url) => {
      if (/after_id=/.test(url)) {
        return Promise.resolve({
          data: { success: true, data: { logs: [], total: 0 } },
        });
      }
      if (/\/logs\/stat/.test(url)) {
        return Promise.resolve({ data: { success: true, data: {} } });
      }
      return Promise.resolve({
        data: { success: true, data: { logs: [seedLog], total: 1 } },
      });
    });

    const { unmount } = render(<HFLog />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    fireEvent.click(screen.getByText('Live tail'));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000);
    });
    const before = API.get.mock.calls.filter(([url]) =>
      /after_id=/.test(url),
    ).length;
    expect(before).toBeGreaterThan(0);

    unmount();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(9000);
    });
    const after = API.get.mock.calls.filter(([url]) =>
      /after_id=/.test(url),
    ).length;
    expect(after).toBe(before);

    vi.useRealTimers();
  });

  // 4. Export button sets window.location.href to the correct URL with
  //    the current filter state encoded as query params.
  it('export button builds correct URL with current filters', async () => {
    // Capture href assignments via a writable mock on window.location.
    let assignedHref = '';
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: {
        ...window.location,
        set href(v) {
          assignedHref = v;
        },
        get href() {
          return assignedHref;
        },
      },
    });

    render(<HFLog />);

    // Wait for initial fetch to complete so the Requests tab is active.
    await waitFor(() => {
      expect(API.get).toHaveBeenCalled();
    });

    // Simulate filter inputs.
    const modelInput = screen.getByPlaceholderText('model name…');
    const tokenInput = screen.getByPlaceholderText('token name…');
    fireEvent.change(modelInput, { target: { value: 'gpt-4o' } });
    fireEvent.change(tokenInput, { target: { value: 'my-token' } });

    // Click the export button (visible on Requests tab).
    const exportBtn = screen.getByTestId('log-export-btn');
    fireEvent.click(exportBtn);

    // href must point to the export endpoint for the current tenant slug.
    expect(assignedHref).toContain('/api/v2/acme/logs/export');
    expect(assignedHref).toContain('model_name=gpt-4o');
    expect(assignedHref).toContain('token_name=my-token');
  });

  // 5. Stat header is wired to GET /logs/stat and renders the aggregates.
  it('fetches the stat header from /logs/stat and renders aggregates', async () => {
    API.get.mockImplementation((url) => {
      if (url.includes('/logs/stat')) {
        return Promise.resolve({
          data: {
            success: true,
            data: {
              total_requests: 42,
              total_quota: 1_000_000,
              rpm: 3,
              tpm: 1500,
            },
          },
        });
      }
      return Promise.resolve({
        data: { success: true, data: { logs: [], total: 0 } },
      });
    });

    render(<HFLog />);

    await waitFor(() => {
      const calls = API.get.mock.calls.map(([u]) => u);
      expect(calls.some((u) => u.includes('/api/v2/acme/logs/stat'))).toBe(
        true,
      );
    });

    await waitFor(() => {
      const header = screen.getByTestId('log-stat-header');
      expect(header.textContent).toContain('42'); // total requests
      expect(header.textContent).toContain('$2.0000'); // 1_000_000 / 500_000
      expect(header.textContent).toContain('1,500'); // tpm, locale-formatted
    });
  });

  // 6. TTFT. A row with no first token still renders an honest n/a cell (never
  //    a silent —); upstream shows the channel id when the row carries one.
  //
  //    This used to assert the reason matched /ttft/i, which passed against the
  //    text "TTFT not stored: the log schema has no time-to-first-token
  //    column" — a false claim. other.frt is written by the relay and kept by
  //    the user-tier projection on purpose. The reason must now describe why
  //    THIS row has no value, not deny that the field exists.
  const ttftBaseLog = {
    id: 1,
    type: 2,
    model_name: 'gpt-4o',
    total_latency_ms: 120,
    prompt_tokens: 100,
    completion_tokens: 200,
    quota: 1000,
    created_at: Math.floor(Date.now() / 1000),
    channel: 7,
  };

  const wireLogs = (log) => {
    API.get.mockImplementation((url) => {
      if (url.includes('/logs/stat')) {
        return Promise.resolve({ data: { success: true, data: {} } });
      }
      return Promise.resolve({
        data: { success: true, data: { logs: [log], total: 1 } },
      });
    });
  };

  it('renders TTFT as n/a for a row with no frt, and upstream from the channel id', async () => {
    wireLogs(ttftBaseLog);

    render(<HFLog />);

    await waitFor(() => {
      expect(screen.getAllByTestId('na-cell').length).toBeGreaterThan(0);
    });
    const reasons = screen
      .getAllByTestId('na-cell')
      .map((c) => c.getAttribute('title') || '');
    expect(reasons.some((r) => /first token/i.test(r))).toBe(true);
    // The retired lie must not come back.
    expect(reasons.some((r) => /schema has no/i.test(r))).toBe(false);
    // upstream column shows "#7" because log.channel is present.
    expect(screen.getByText('#7')).toBeTruthy();
  });

  it('renders the TTFT value from other.frt when the row carries one', async () => {
    wireLogs({
      ...ttftBaseLog,
      other: JSON.stringify({ frt: 412.6 }),
    });

    render(<HFLog />);

    // Rounded, with the same ms suffix the latency column uses.
    await waitFor(() => expect(screen.getByText('413')).toBeTruthy());
    // …and no n/a cell claiming TTFT is unavailable on a row that has it.
    const reasons = screen
      .queryAllByTestId('na-cell')
      .map((c) => c.getAttribute('title') || '');
    expect(reasons.some((r) => /first token/i.test(r))).toBe(false);
  });

  // ── Routing trace (other.admin_info.route_attempts) ───────────────────────
  //
  // The relay writes this only when a request bounced between channels, and the
  // API strips admin_info for non-admin callers — so the panel must appear for
  // an admin looking at a failed-over request and stay silent in every other
  // case, including malformed payloads.

  const logWithOther = (other) => ({
    id: 1,
    model_name: 'gpt-4o',
    token_name: 'tk',
    prompt_tokens: 10,
    completion_tokens: 5,
    quota: 1000,
    created_at: Math.floor(Date.now() / 1000),
    channel: 7,
    type: 1,
    other,
  });

  const renderWithLog = (log) => {
    API.get.mockImplementation((url) => {
      if (url.includes('/logs/stat')) {
        return Promise.resolve({ data: { success: true, data: {} } });
      }
      return Promise.resolve({
        data: { success: true, data: { logs: [log], total: 1 } },
      });
    });
    render(<HFLog />);
  };

  it('renders the routing trace for a request that failed over', async () => {
    renderWithLog(
      logWithOther(
        JSON.stringify({
          admin_info: {
            route_attempts: [
              {
                channel_id: 3,
                channel_name: 'primary',
                provider: 'openai',
                outcome: 'upstream_error',
                error_code: 'channel:timeout',
                status_code: 504,
                duration_ms: 30000,
              },
              {
                channel_id: 8,
                channel_name: 'backup',
                provider: 'openai',
                outcome: 'success',
                duration_ms: 910,
              },
            ],
          },
        }),
      ),
    );

    await waitFor(() => screen.getByTestId('log-route-attempts'));

    // The abandoned channel and WHY it was abandoned must both be visible —
    // that is the entire reason the trace exists.
    const first = screen.getByTestId('route-attempt-0');
    expect(first.textContent).toContain('#3');
    expect(first.textContent).toContain('channel:timeout');
    expect(first.textContent).toContain('504');
    expect(first.textContent).toContain('30000ms');

    const second = screen.getByTestId('route-attempt-1');
    expect(second.textContent).toContain('#8');
    expect(second.textContent).toContain('910ms');
  });

  it('omits the routing trace when admin_info is stripped for non-admins', async () => {
    renderWithLog(logWithOther(JSON.stringify({ model_ratio: 1, frt: 120 })));

    await waitFor(() =>
      expect(screen.getAllByText('gpt-4o').length).toBeGreaterThan(0),
    );
    expect(screen.queryByTestId('log-route-attempts')).toBeNull();
  });

  it('survives a malformed other payload without rendering a trace', async () => {
    renderWithLog(logWithOther('{not json'));

    await waitFor(() =>
      expect(screen.getAllByText('gpt-4o').length).toBeGreaterThan(0),
    );
    expect(screen.queryByTestId('log-route-attempts')).toBeNull();
  });

  // ── Deep-linking + errors-only + tenant-wide + billing detail ─────────────

  it('seeds model/token/type filters from the URL query (deep-link support)', async () => {
    // The export test above replaces window.location with a static mock whose
    // `search` is frozen at '' — history.pushState would mutate the REAL
    // location while the component reads the mock. Override explicitly.
    const prevLocation = window.location;
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: {
        ...prevLocation,
        search: '?model_name=m-deep&token_name=tk-deep&type=5',
      },
    });
    try {
      render(<HFLog />);
      await waitFor(() => expect(API.get).toHaveBeenCalled());

      // The inputs reflect the seeded filters…
      expect(screen.getByPlaceholderText('model name…')).toHaveValue('m-deep');
      expect(screen.getByPlaceholderText('token name…')).toHaveValue('tk-deep');
      // …and the very FIRST fetch already carries them — a deep link that only
      // filled the boxes but fetched everything would defeat its purpose.
      const firstLogsCall = API.get.mock.calls
        .map(([u]) => u)
        .find((u) => u.includes('/logs?'));
      expect(firstLogsCall).toContain('model_name=m-deep');
      expect(firstLogsCall).toContain('token_name=tk-deep');
      expect(firstLogsCall).toContain('type=5');
    } finally {
      Object.defineProperty(window, 'location', {
        configurable: true,
        value: prevLocation,
      });
    }
  });

  it('errors-only toggle adds type=5 to the fetch and clears it on second click', async () => {
    render(<HFLog />);
    await waitFor(() => expect(API.get).toHaveBeenCalled());
    API.get.mockClear();

    fireEvent.click(screen.getByTestId('log-errors-only'));
    await waitFor(() => {
      const urls = API.get.mock.calls.map(([u]) => u);
      expect(
        urls.some((u) => u.includes('/logs?') && u.includes('type=5')),
      ).toBe(true);
      // The stat header must aggregate the SAME slice the table shows.
      expect(
        urls.some((u) => u.includes('/logs/stat') && u.includes('type=5')),
      ).toBe(true);
    });

    API.get.mockClear();
    fireEvent.click(screen.getByTestId('log-errors-only'));
    await waitFor(() => {
      const urls = API.get.mock.calls.map(([u]) => u);
      const logsCall = urls.find((u) => u.includes('/logs?'));
      expect(logsCall).toBeDefined();
      expect(logsCall).not.toContain('type=5');
    });
  });

  it('hides the tenant-wide toggle from non-admins', async () => {
    render(<HFLog />);
    await waitFor(() => expect(API.get).toHaveBeenCalled());
    expect(screen.queryByTestId('log-tenant-wide')).toBeNull();
  });

  it('tenant-wide toggle (admin) switches the fetch to /logs/all', async () => {
    mockIsAdmin.mockReturnValue(true);
    render(<HFLog />);
    await waitFor(() => expect(API.get).toHaveBeenCalled());
    API.get.mockClear();

    fireEvent.click(screen.getByTestId('log-tenant-wide'));
    await waitFor(() => {
      const urls = API.get.mock.calls.map(([u]) => u);
      expect(urls.some((u) => u.includes('/api/v2/acme/logs/all?'))).toBe(true);
      // The stat header must follow the scope — a caller-scoped header over a
      // tenant-wide table would misreport every aggregate.
      expect(urls.some((u) => u.includes('/logs/stat/all'))).toBe(true);
    });

    // Toggling back returns to the caller-scoped routes.
    API.get.mockClear();
    fireEvent.click(screen.getByTestId('log-tenant-wide'));
    await waitFor(() => {
      const urls = API.get.mock.calls.map(([u]) => u);
      expect(urls.some((u) => u.includes('/logs?'))).toBe(true);
      expect(urls.some((u) => u.includes('/logs/all'))).toBe(false);
      expect(urls.some((u) => u.includes('/logs/stat/all'))).toBe(false);
      expect(urls.some((u) => u.includes('/logs/stat'))).toBe(true);
    });
  });

  it('renders billing-explainability fields from the row `other` payload', async () => {
    renderWithLog(
      logWithOther(
        JSON.stringify({
          cache_tokens: 42,
          cache_creation_tokens: 7,
          request_path: '/v1/chat/completions',
          frt: 123.4,
        }),
      ),
    );

    await waitFor(() =>
      expect(screen.getAllByText('gpt-4o').length).toBeGreaterThan(0),
    );
    expect(screen.getByTestId('log-detail-cache-read').textContent).toContain(
      '42',
    );
    expect(screen.getByTestId('log-detail-cache-write').textContent).toContain(
      '7',
    );
    expect(screen.getByTestId('log-detail-endpoint').textContent).toContain(
      '/v1/chat/completions',
    );
    expect(screen.getByTestId('log-detail-frt').textContent).toContain('123ms');
  });

  it('renders no fabricated zeros when the row carries no billing detail', async () => {
    renderWithLog(logWithOther(JSON.stringify({ cache_tokens: 0 })));

    await waitFor(() =>
      expect(screen.getAllByText('gpt-4o').length).toBeGreaterThan(0),
    );
    for (const id of [
      'log-detail-cache-read',
      'log-detail-cache-write',
      'log-detail-endpoint',
      'log-detail-frt',
    ]) {
      expect(screen.queryByTestId(id)).toBeNull();
    }
  });
});
