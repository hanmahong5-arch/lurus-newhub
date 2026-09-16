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
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';

// ─── mocks ───────────────────────────────────────────────────────────────────

vi.mock('../../../helpers', () => ({
  API: { get: vi.fn() },
  showError: vi.fn(),
}));

vi.mock('../../../helpers/formatting', () => ({
  formatUSD: (q) => (q ? `$${(q / 500000).toFixed(4)}` : '—'),
}));

vi.mock('../../../components/hifi/HFShell', () => ({
  default: ({ children }) =>
    React.createElement('div', { 'data-testid': 'hf-shell' }, children),
}));

// Mirror i18next's en behaviour: return the English default (2nd arg) with
// {{var}} interpolation from the vars object (3rd arg), falling back to the
// key when no default is given. Matches the pattern used by the other v2
// page test suites (e.g. Redemption/index.test.jsx).
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key, fallback, vars) => {
      let out = typeof fallback === 'string' ? fallback : key;
      if (vars) {
        for (const [k, v] of Object.entries(vars)) {
          out = out.split(`{{${k}}}`).join(String(v));
        }
      }
      return out;
    },
  }),
}));

import HFTasks from './index';
import { API, showError } from '../../../helpers';

// ─── fixtures ────────────────────────────────────────────────────────────────

const makeTask = (id, overrides = {}) => ({
  id,
  submit_time: 1757900000,
  finish_time: 1757900010,
  action: 'GENERATE',
  task_id: `tid-${id}`,
  request_id: `req-${id}`,
  status: 'SUCCESS',
  progress: '100%',
  quota: 500000,
  fail_reason: '',
  ...overrides,
});

const makeMj = (id, overrides = {}) => ({
  id,
  submit_time: 1757900000000,
  finish_time: 1757900012000,
  action: 'IMAGINE',
  mj_id: `mj-${id}`,
  status: 'SUCCESS',
  progress: '100%',
  quota: 250000,
  image_url: '',
  prompt: 'a cat in a hat',
  ...overrides,
});

const page = (items, total) => ({
  data: { success: true, data: { items, total, page: 1, page_size: 50 } },
});

const emptyPage = () => page([], 0);

beforeEach(() => {
  API.get.mockReset();
  showError.mockReset();
});

// ─── tests ───────────────────────────────────────────────────────────────────

describe('Tasks page (v2)', () => {
  it('loads and renders the Tasks tab by default from GET /api/task/self/', async () => {
    API.get.mockResolvedValue(page([makeTask(1), makeTask(2)], 2));

    render(React.createElement(HFTasks));

    await waitFor(() => {
      expect(screen.getByTestId('tasks-row-1')).toBeDefined();
      expect(screen.getByTestId('tasks-row-2')).toBeDefined();
    });

    expect(API.get).toHaveBeenCalledWith(
      expect.stringContaining('/api/task/self/?'),
    );
  });

  it('shows the empty state when the task list is empty', async () => {
    API.get.mockResolvedValue(emptyPage());

    render(React.createElement(HFTasks));

    await waitFor(() => {
      expect(screen.getByTestId('tasks-empty')).toBeDefined();
    });
  });

  // Oracle: project_id / request_id must actually reach the server as query
  // parameters on the real request URL — not merely cause rows to render.
  it('sends project_id and request_id filters on the request URL when searching', async () => {
    API.get.mockResolvedValue(page([makeTask(1)], 1));

    render(React.createElement(HFTasks));

    await waitFor(() => expect(API.get).toHaveBeenCalledTimes(1));

    fireEvent.change(screen.getByTestId('tasks-project-id-filter'), {
      target: { value: '42' },
    });
    fireEvent.change(screen.getByTestId('tasks-request-id-filter'), {
      target: { value: 'req-abc-123' },
    });
    fireEvent.click(screen.getByTestId('tasks-search-btn'));

    await waitFor(() => expect(API.get).toHaveBeenCalledTimes(2));

    const lastUrl = API.get.mock.calls[1][0];
    const qs = new URLSearchParams(lastUrl.split('?')[1]);
    expect(qs.get('project_id')).toBe('42');
    expect(qs.get('request_id')).toBe('req-abc-123');
  });

  it('paginates the Tasks tab: clicking next requests p=2', async () => {
    const page1 = Array.from({ length: 50 }, (_, i) => makeTask(i + 1));
    const page2 = [makeTask(51)];
    API.get.mockImplementation((url) => {
      const qs = new URLSearchParams(url.split('?')[1]);
      const p = Number(qs.get('p'));
      return Promise.resolve(
        p === 2
          ? {
              data: {
                success: true,
                data: { items: page2, total: 51, page: 2, page_size: 50 },
              },
            }
          : {
              data: {
                success: true,
                data: { items: page1, total: 51, page: 1, page_size: 50 },
              },
            },
      );
    });

    render(React.createElement(HFTasks));

    await waitFor(() => screen.getByTestId('tasks-row-1'));
    expect(screen.getByTestId('tasks-next-btn')).not.toBeDisabled();

    fireEvent.click(screen.getByTestId('tasks-next-btn'));

    await waitFor(() => {
      const lastUrl = API.get.mock.calls[API.get.mock.calls.length - 1][0];
      expect(new URLSearchParams(lastUrl.split('?')[1]).get('p')).toBe('2');
    });
    await waitFor(() => screen.getByTestId('tasks-row-51'));
  });

  it('switches to the MJ tab and loads from GET /api/mj/self/', async () => {
    API.get.mockImplementation((url) =>
      url.startsWith('/api/mj/self/')
        ? Promise.resolve(page([makeMj(9)], 1))
        : Promise.resolve(emptyPage()),
    );

    render(React.createElement(HFTasks));

    await waitFor(() => expect(API.get).toHaveBeenCalledTimes(1));

    fireEvent.click(screen.getByTestId('tasks-tab-mj'));

    await waitFor(() => {
      expect(screen.getByTestId('mj-row-9')).toBeDefined();
    });

    const mjCall = API.get.mock.calls.find((c) =>
      c[0].startsWith('/api/mj/self/'),
    );
    expect(mjCall).toBeDefined();
  });

  it('sends mj_id filter on the MJ tab request URL', async () => {
    API.get.mockImplementation((url) =>
      url.startsWith('/api/mj/self/')
        ? Promise.resolve(page([makeMj(9)], 1))
        : Promise.resolve(emptyPage()),
    );

    render(React.createElement(HFTasks));
    await waitFor(() => expect(API.get).toHaveBeenCalledTimes(1));

    fireEvent.click(screen.getByTestId('tasks-tab-mj'));
    await waitFor(() => screen.getByTestId('mj-row-9'));

    fireEvent.change(screen.getByTestId('mj-mj-id-filter'), {
      target: { value: 'mj-xyz' },
    });
    fireEvent.click(screen.getByTestId('mj-search-btn'));

    await waitFor(() => {
      const calls = API.get.mock.calls.filter((c) =>
        c[0].startsWith('/api/mj/self/'),
      );
      expect(calls.length).toBeGreaterThanOrEqual(2);
      const lastMjUrl = calls[calls.length - 1][0];
      expect(new URLSearchParams(lastMjUrl.split('?')[1]).get('mj_id')).toBe(
        'mj-xyz',
      );
    });
  });

  it('renders a fail_reason that is a URL as a clickable "view result" link', async () => {
    API.get.mockResolvedValue(
      page(
        [
          makeTask(3, {
            status: 'SUCCESS',
            fail_reason: 'https://cdn.example.test/out.mp4',
          }),
        ],
        1,
      ),
    );

    render(React.createElement(HFTasks));

    await waitFor(() => screen.getByTestId('tasks-row-3'));
    const link = screen.getByText('view result');
    expect(link.closest('a')).toHaveAttribute(
      'href',
      'https://cdn.example.test/out.mp4',
    );
  });

  // Oracle for the seconds-vs-milliseconds split (Task.SubmitTime is Unix
  // seconds, repo/task.go:100; Midjourney.SubmitTime is Unix ms,
  // mjproxy_handler.go:566/604). Asserts the exact wire value, which only
  // matches when the Tasks tab divides by 1000 and the MJ tab does not.
  it('sends start_timestamp in Unix SECONDS on the Tasks tab', async () => {
    API.get.mockResolvedValue(page([makeTask(1)], 1));

    render(React.createElement(HFTasks));
    await waitFor(() => expect(API.get).toHaveBeenCalledTimes(1));

    fireEvent.change(screen.getByTestId('tasks-start-filter'), {
      target: { value: '2026-09-16T00:00' },
    });
    fireEvent.click(screen.getByTestId('tasks-search-btn'));

    await waitFor(() => expect(API.get).toHaveBeenCalledTimes(2));

    const lastUrl = API.get.mock.calls[1][0];
    const qs = new URLSearchParams(lastUrl.split('?')[1]);
    const sent = Number(qs.get('start_timestamp'));
    expect(sent).toBe(
      Math.floor(new Date('2026-09-16T00:00').getTime() / 1000),
    );
    // magnitude check: whole Unix seconds are ~1e9, not ~1e12
    expect(sent).toBeLessThan(1e10);
  });

  // end_timestamp is converted by the same code path but needs its own
  // assertion: with only start_timestamp pinned, the unit could drift on the
  // end bound alone and every other assertion would stay green.
  it('sends end_timestamp in Unix SECONDS on the Tasks tab', async () => {
    API.get.mockResolvedValue(page([makeTask(1)], 1));

    render(React.createElement(HFTasks));
    await waitFor(() => expect(API.get).toHaveBeenCalledTimes(1));

    fireEvent.change(screen.getByTestId('tasks-end-filter'), {
      target: { value: '2026-09-17T00:00' },
    });
    fireEvent.click(screen.getByTestId('tasks-search-btn'));

    await waitFor(() => expect(API.get).toHaveBeenCalledTimes(2));

    const qs2 = new URLSearchParams(API.get.mock.calls[1][0].split('?')[1]);
    const sentEnd = Number(qs2.get('end_timestamp'));
    expect(sentEnd).toBe(
      Math.floor(new Date('2026-09-17T00:00').getTime() / 1000),
    );
    expect(sentEnd).toBeLessThan(1e10);
  });

  it('sends start_timestamp in Unix MILLISECONDS on the MJ tab', async () => {
    API.get.mockImplementation((url) =>
      url.startsWith('/api/mj/self/')
        ? Promise.resolve(page([makeMj(9)], 1))
        : Promise.resolve(emptyPage()),
    );

    render(React.createElement(HFTasks));
    await waitFor(() => expect(API.get).toHaveBeenCalledTimes(1));

    fireEvent.click(screen.getByTestId('tasks-tab-mj'));
    await waitFor(() => screen.getByTestId('mj-row-9'));

    fireEvent.change(screen.getByTestId('mj-start-filter'), {
      target: { value: '2026-09-16T00:00' },
    });
    fireEvent.click(screen.getByTestId('mj-search-btn'));

    await waitFor(() => {
      const calls = API.get.mock.calls.filter((c) =>
        c[0].startsWith('/api/mj/self/'),
      );
      expect(calls.length).toBeGreaterThanOrEqual(2);
    });

    const calls = API.get.mock.calls.filter((c) =>
      c[0].startsWith('/api/mj/self/'),
    );
    const lastMjUrl = calls[calls.length - 1][0];
    const qs = new URLSearchParams(lastMjUrl.split('?')[1]);
    const sent = Number(qs.get('start_timestamp'));
    expect(sent).toBe(new Date('2026-09-16T00:00').getTime());
    // magnitude check: Unix milliseconds are ~1e12, not ~1e9
    expect(sent).toBeGreaterThan(1e12);
  });

  it('sends end_timestamp in Unix MILLISECONDS on the MJ tab', async () => {
    API.get.mockImplementation((url) =>
      Promise.resolve(
        url.startsWith('/api/mj/self/')
          ? page([makeMj(9)], 1)
          : page([makeTask(1)], 1),
      ),
    );

    render(React.createElement(HFTasks));
    await waitFor(() => expect(API.get).toHaveBeenCalledTimes(1));

    fireEvent.click(screen.getByTestId('tasks-tab-mj'));
    await waitFor(() => screen.getByTestId('mj-row-9'));

    fireEvent.change(screen.getByTestId('mj-end-filter'), {
      target: { value: '2026-09-17T00:00' },
    });
    fireEvent.click(screen.getByTestId('mj-search-btn'));

    await waitFor(() => {
      const c = API.get.mock.calls.filter((x) =>
        x[0].startsWith('/api/mj/self/'),
      );
      expect(c.length).toBeGreaterThanOrEqual(2);
    });

    const mjCalls = API.get.mock.calls.filter((c) =>
      c[0].startsWith('/api/mj/self/'),
    );
    const qsEnd = new URLSearchParams(
      mjCalls[mjCalls.length - 1][0].split('?')[1],
    );
    const sentEnd = Number(qsEnd.get('end_timestamp'));
    expect(sentEnd).toBe(new Date('2026-09-17T00:00').getTime());
    expect(sentEnd).toBeGreaterThan(1e12);
  });

  it('renders the FAILURE status tag with error styling on the Tasks tab', async () => {
    API.get.mockResolvedValue(page([makeTask(4, { status: 'FAILURE' })], 1));

    render(React.createElement(HFTasks));

    await waitFor(() => screen.getByTestId('tasks-row-4'));
    const tag = within(screen.getByTestId('tasks-row-4')).getByText('failed');
    expect(tag.className).toContain('err');
  });

  it('renders the formatted cost cell on the Tasks tab', async () => {
    API.get.mockResolvedValue(page([makeTask(5, { quota: 500000 })], 1));

    render(React.createElement(HFTasks));

    await waitFor(() => screen.getByTestId('tasks-row-5'));
    expect(
      within(screen.getByTestId('tasks-row-5')).getByText('$1.0000'),
    ).toBeDefined();
  });

  it('renders the MJ submitted time from milliseconds without re-scaling', async () => {
    // submit_time 1757900000000 is 2025-09-14/15 depending on timezone; a
    // stray *1000 (treating it as seconds) would land ~55,000 years in the
    // future, so a bare '2025' substring is enough to catch the unit bug.
    API.get.mockImplementation((url) =>
      url.startsWith('/api/mj/self/')
        ? Promise.resolve(page([makeMj(10)], 1))
        : Promise.resolve(emptyPage()),
    );

    render(React.createElement(HFTasks));
    await waitFor(() => expect(API.get).toHaveBeenCalledTimes(1));

    fireEvent.click(screen.getByTestId('tasks-tab-mj'));
    await waitFor(() => screen.getByTestId('mj-row-10'));

    expect(screen.getByTestId('mj-row-10').textContent).toContain('2025');
  });

  it('renders fail_reason on the MJ tab for a failed job', async () => {
    API.get.mockImplementation((url) =>
      url.startsWith('/api/mj/self/')
        ? Promise.resolve(
            page(
              [
                makeMj(11, {
                  status: 'FAILURE',
                  fail_reason: 'upstream timeout',
                }),
              ],
              1,
            ),
          )
        : Promise.resolve(emptyPage()),
    );

    render(React.createElement(HFTasks));
    await waitFor(() => expect(API.get).toHaveBeenCalledTimes(1));

    fireEvent.click(screen.getByTestId('tasks-tab-mj'));
    await waitFor(() => screen.getByTestId('mj-row-11'));

    expect(
      within(screen.getByTestId('mj-row-11')).getByText('upstream timeout'),
    ).toBeDefined();
  });

  it('renders a FAILURE fail_reason that looks like a URL as raw text, not a link', async () => {
    API.get.mockResolvedValue(
      page(
        [
          makeTask(6, {
            status: 'FAILURE',
            fail_reason: 'https://errors.example.test/E123',
          }),
        ],
        1,
      ),
    );

    render(React.createElement(HFTasks));

    await waitFor(() => screen.getByTestId('tasks-row-6'));
    expect(screen.queryByText('view result')).toBeNull();
    expect(
      within(screen.getByTestId('tasks-row-6')).getByText(
        'https://errors.example.test/E123',
      ),
    ).toBeDefined();
  });

  it('handles API error on list fetch gracefully', async () => {
    API.get.mockRejectedValue(new Error('network error'));

    render(React.createElement(HFTasks));

    await waitFor(() => {
      expect(screen.getByTestId('hf-shell')).toBeDefined();
    });
  });
});
