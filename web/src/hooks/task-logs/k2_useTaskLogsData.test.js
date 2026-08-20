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
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';

vi.mock('../../helpers', () => ({
  API: { get: vi.fn() },
  copy: vi.fn(),
  isAdmin: vi.fn(() => false),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  timestamp2string: (ts) => new Date(Math.round(ts * 1000)).toISOString(),
  getTableCompactMode: () => false,
  setTableCompactMode: vi.fn(),
}));

vi.mock('@douyinfe/semi-ui', () => ({
  Modal: { error: vi.fn(), info: vi.fn(), confirm: vi.fn() },
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key) => key }),
}));

import { useTaskLogsData } from './useTaskLogsData';
import { API, copy, isAdmin, showError, showSuccess } from '../../helpers';
import { Modal } from '@douyinfe/semi-ui';

// Task.SubmitTime is stored as Unix SECONDS server-side, so the query the
// hook builds must be in seconds too.
const RANGE = ['2026-03-01T00:00:00.000Z', '2026-03-02T00:00:00.000Z'];
const START_SEC = Date.parse(RANGE[0]) / 1000;
const END_SEC = Date.parse(RANGE[1]) / 1000;

const task = (id, over = {}) => ({
  id,
  created_at: 1772323200,
  task_id: `task-${id}`,
  platform: 'suno',
  status: 'SUCCESS',
  progress: '100%',
  ...over,
});

const taskPage = (items, extra = {}) => ({
  data: {
    success: true,
    data: { items, total: items.length, page: 1, page_size: 10, ...extra },
  },
});

const mount = async () => {
  const hook = renderHook(() => useTaskLogsData());
  await waitFor(() => expect(hook.result.current.loading).toBe(false));
  return hook;
};

const withRange = async (hook, extra = {}) => {
  await act(async () => {
    hook.result.current.setFormApi({
      getValues: () => ({ dateRange: RANGE, ...extra }),
    });
  });
};

beforeEach(() => {
  vi.clearAllMocks();
  window.localStorage.clear();
  isAdmin.mockReturnValue(false);
  API.get.mockResolvedValue(taskPage([task(1)]));
});

describe('useTaskLogsData — querying', () => {
  it('a normal user queries the self-scoped route without a channel filter', async () => {
    const hook = await mount();
    await withRange(hook, { task_id: 'task-9', channel_id: '3' });
    API.get.mockClear();

    await act(async () => {
      await hook.result.current.loadLogs(2, 20);
    });

    const url = API.get.mock.calls[0][0];
    expect(url).toContain('/api/task/self?p=2&page_size=20');
    expect(url).toContain('task_id=task-9');
    expect(url).not.toContain('channel_id=');
    expect(url).toContain(`start_timestamp=${START_SEC}`);
    expect(url).toContain(`end_timestamp=${END_SEC}`);
  });

  it('an admin queries the global route with the channel filter', async () => {
    isAdmin.mockReturnValue(true);
    const hook = await mount();
    await withRange(hook, { channel_id: '3' });
    API.get.mockClear();

    await act(async () => {
      await hook.result.current.loadLogs(1, 10);
    });

    const url = API.get.mock.calls[0][0];
    expect(url).toContain('/api/task/?p=1&page_size=10');
    expect(url).toContain('channel_id=3');
  });

  it('sends whole-second timestamps, never fractions', async () => {
    const hook = await mount();
    await withRange(hook, {
      dateRange: ['2026-03-01T00:00:00.500Z', '2026-03-02T00:00:00.900Z'],
    });
    API.get.mockClear();

    await act(async () => {
      await hook.result.current.loadLogs(1, 10);
    });

    const url = API.get.mock.calls[0][0];
    expect(url).toContain('start_timestamp=1772323200&');
    expect(url).toContain('end_timestamp=1772409600');
    expect(url).not.toContain('.');
  });

  it('adopts the paging the server reports', async () => {
    const hook = await mount();
    API.get.mockResolvedValue(
      taskPage([task(1)], { page: 5, page_size: 25, total: 120 }),
    );

    await act(async () => {
      await hook.result.current.loadLogs(5, 25);
    });

    expect(hook.result.current.activePage).toBe(5);
    expect(hook.result.current.pageSize).toBe(25);
    expect(hook.result.current.logCount).toBe(120);
  });

  it('reports a refused query and clears loading', async () => {
    const hook = await mount();
    API.get.mockResolvedValue({
      data: { success: false, message: 'task log denied' },
    });

    await act(async () => {
      await hook.result.current.loadLogs(1, 10);
    });

    expect(showError).toHaveBeenCalledWith('task log denied');
    expect(hook.result.current.loading).toBe(false);
  });

  it('remembers the page size under a task-specific key', async () => {
    const hook = await mount();

    await act(async () => {
      await hook.result.current.handlePageSizeChange(50);
    });

    expect(window.localStorage.getItem('task-page-size')).toBe('50');
    expect(API.get.mock.calls.at(-1)[0]).toContain('?p=1&page_size=50');
    // The generic logs key must not be clobbered.
    expect(window.localStorage.getItem('page-size')).toBeNull();
  });

  it('seeds the first request from the remembered page size', async () => {
    window.localStorage.setItem('task-page-size', '30');
    API.get.mockResolvedValue(taskPage([task(1)], { page_size: 30 }));

    const hook = await mount();

    expect(API.get.mock.calls[0][0]).toContain('page_size=30');
    expect(hook.result.current.pageSize).toBe(30);
  });

  it('refresh always goes back to the first page', async () => {
    const hook = await mount();
    API.get.mockClear();

    await act(async () => {
      await hook.result.current.refresh();
    });

    expect(API.get.mock.calls[0][0]).toContain('?p=1&');
  });

  it('handlePageChange keeps the current page size', async () => {
    const hook = await mount();
    API.get.mockClear();

    await act(async () => {
      hook.result.current.handlePageChange(7);
    });

    expect(API.get.mock.calls[0][0]).toContain('?p=7&page_size=10');
  });
});

describe('useTaskLogsData — row shaping', () => {
  it('gives every row a string key and a rendered timestamp', async () => {
    const hook = await mount();

    const rows = hook.result.current.enrichLogs([task(11), task(12)]);

    expect(rows.map((r) => r.key)).toEqual(['11', '12']);
    expect(rows[0].timestamp2string).toBe(
      new Date(1772323200000).toISOString(),
    );
    expect(rows[0].task_id).toBe('task-11');
  });

  it('does not mutate the rows handed to it', async () => {
    const hook = await mount();
    const input = task(1);

    hook.result.current.enrichLogs([input]);

    expect(input.key).toBeUndefined();
    expect(input.timestamp2string).toBeUndefined();
  });

  it('syncPageData tolerates a payload with no items', async () => {
    const hook = await mount();

    act(() => {
      hook.result.current.syncPageData({});
    });

    expect(hook.result.current.logs).toEqual([]);
    expect(hook.result.current.logCount).toBe(0);
    expect(hook.result.current.activePage).toBe(1);
    expect(hook.result.current.pageSize).toBe(10);
  });
});

describe('useTaskLogsData — column preferences', () => {
  it('hides the channel column from a normal user and stores it separately', async () => {
    const { result } = await mount();

    await waitFor(() =>
      expect(result.current.visibleColumns.channel).toBe(false),
    );
    expect(result.current.visibleColumns.platform).toBe(true);
    expect(
      window.localStorage.getItem('task-logs-table-columns-user'),
    ).not.toBeNull();
    expect(
      window.localStorage.getItem('task-logs-table-columns-admin'),
    ).toBeNull();
  });

  it('shows the channel column to an admin', async () => {
    isAdmin.mockReturnValue(true);
    const { result } = await mount();

    await waitFor(() =>
      expect(result.current.visibleColumns.channel).toBe(true),
    );
  });

  it('re-hides a channel column a demoted user still has stored', async () => {
    window.localStorage.setItem(
      'task-logs-table-columns-user',
      JSON.stringify({ channel: true, progress: false }),
    );

    const { result } = await mount();

    await waitFor(() =>
      expect(result.current.visibleColumns.channel).toBe(false),
    );
    expect(result.current.visibleColumns.progress).toBe(false);
    expect(result.current.visibleColumns.task_id).toBe(true);
  });

  it('select-all still withholds the channel column from a normal user', async () => {
    const { result } = await mount();
    await waitFor(() =>
      expect(result.current.visibleColumns.task_id).toBe(true),
    );

    act(() => {
      result.current.handleSelectAll(true);
    });

    expect(result.current.visibleColumns.channel).toBe(false);
    expect(result.current.visibleColumns.fail_reason).toBe(true);
  });

  it('toggling one column persists the whole map', async () => {
    const { result } = await mount();
    await waitFor(() =>
      expect(result.current.visibleColumns.duration).toBe(true),
    );

    act(() => {
      result.current.handleColumnVisibilityChange('duration', false);
    });

    await waitFor(() =>
      expect(
        JSON.parse(window.localStorage.getItem('task-logs-table-columns-user'))
          .duration,
      ).toBe(false),
    );
    expect(result.current.visibleColumns.submit_time).toBe(true);
  });

  it('rebuilds defaults from a corrupt stored blob', async () => {
    window.localStorage.setItem('task-logs-table-columns-user', 'nope');
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

    const { result } = await mount();

    await waitFor(() =>
      expect(result.current.visibleColumns.task_id).toBe(true),
    );
    errSpy.mockRestore();
  });
});

describe('useTaskLogsData — modals and clipboard', () => {
  it('opens the content modal with the given text', async () => {
    const { result } = await mount();

    act(() => {
      result.current.openContentModal('failed: quota exhausted');
    });

    expect(result.current.isModalOpen).toBe(true);
    expect(result.current.modalContent).toBe('failed: quota exhausted');
  });

  it('opens the video modal with the given url', async () => {
    const { result } = await mount();

    act(() => {
      result.current.openVideoModal('https://cdn.example/v.mp4');
    });

    expect(result.current.isVideoModalOpen).toBe(true);
    expect(result.current.videoUrl).toBe('https://cdn.example/v.mp4');
  });

  it('echoes the copied text on success', async () => {
    const { result } = await mount();
    copy.mockResolvedValue(true);

    await act(async () => {
      await result.current.copyText('task-1');
    });

    expect(showSuccess).toHaveBeenCalledWith('已复制：task-1');
  });

  it('falls back to a modal when the clipboard is blocked', async () => {
    const { result } = await mount();
    copy.mockResolvedValue(false);

    await act(async () => {
      await result.current.copyText('task-1');
    });

    expect(Modal.error).toHaveBeenCalledWith(
      expect.objectContaining({ content: 'task-1' }),
    );
  });

  it('getFormValues normalises the filters and keeps the default range', async () => {
    const { result } = await mount();

    const values = result.current.getFormValues();
    expect(values.channel_id).toBe('');
    expect(values.task_id).toBe('');
    expect(Number.isNaN(Date.parse(values.start_timestamp))).toBe(false);
    expect(Date.parse(values.end_timestamp)).toBeGreaterThan(
      Date.parse(values.start_timestamp),
    );
  });
});
