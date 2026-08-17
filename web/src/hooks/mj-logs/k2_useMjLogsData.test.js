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
  // Faithful to the real helper: it never throws, it just produces a NaN
  // string when handed something that is not a unix timestamp.
  timestamp2string: (ts) => {
    const date = new Date(Math.round(ts * 1000));
    return Number.isNaN(date.getTime())
      ? 'NaN-NaN-NaN NaN:NaN:NaN'
      : date.toISOString();
  },
  getTableCompactMode: () => false,
  setTableCompactMode: vi.fn(),
}));

vi.mock('@douyinfe/semi-ui', () => ({
  Modal: { error: vi.fn(), info: vi.fn(), confirm: vi.fn() },
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key) => key }),
}));

import { useMjLogsData } from './useMjLogsData';
import { API, copy, isAdmin, showError, showSuccess } from '../../helpers';
import { Modal } from '@douyinfe/semi-ui';

// Midjourney.SubmitTime is stored in MILLISECONDS server-side (the handler
// derives use_time from UnixNano/Millisecond), so this query is in ms —
// unlike the task-log query, which is in seconds.
const RANGE = ['2026-03-01T00:00:00.000Z', '2026-03-02T00:00:00.000Z'];
const START_MS = Date.parse(RANGE[0]);
const END_MS = Date.parse(RANGE[1]);

const mjRow = (id, over = {}) => ({
  id,
  mj_id: `mj-${id}`,
  submit_time: 1772323200000,
  status: 'SUCCESS',
  progress: '100%',
  prompt: 'a cat',
  ...over,
});

const mjPage = (items, extra = {}) => ({
  data: {
    success: true,
    data: { items, total: items.length, page: 1, page_size: 10, ...extra },
  },
});

const mount = async () => {
  const hook = renderHook(() => useMjLogsData());
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
  API.get.mockResolvedValue(mjPage([mjRow(1)]));
});

describe('useMjLogsData — querying', () => {
  it('a normal user queries the self route without the channel filter', async () => {
    const hook = await mount();
    await withRange(hook, { mj_id: 'mj-9', channel_id: '4' });
    API.get.mockClear();

    await act(async () => {
      await hook.result.current.loadLogs(2, 20);
    });

    const url = API.get.mock.calls[0][0];
    expect(url).toContain('/api/mj/self/?p=2&page_size=20');
    expect(url).toContain('mj_id=mj-9');
    expect(url).not.toContain('channel_id=');
  });

  it('an admin queries the global route with the channel filter', async () => {
    isAdmin.mockReturnValue(true);
    const hook = await mount();
    await withRange(hook, { channel_id: '4' });
    API.get.mockClear();

    await act(async () => {
      await hook.result.current.loadLogs(1, 10);
    });

    const url = API.get.mock.calls[0][0];
    expect(url).toContain('/api/mj/?p=1&page_size=10');
    expect(url).toContain('channel_id=4');
  });

  it('sends the range in milliseconds to match stored submit_time', async () => {
    const hook = await mount();
    await withRange(hook);
    API.get.mockClear();

    await act(async () => {
      await hook.result.current.loadLogs(1, 10);
    });

    const url = API.get.mock.calls[0][0];
    expect(url).toContain(`start_timestamp=${START_MS}`);
    expect(url).toContain(`end_timestamp=${END_MS}`);
    expect(START_MS).toBe(1772323200000);
  });

  it('adopts the paging the server reports', async () => {
    const hook = await mount();
    API.get.mockResolvedValue(
      mjPage([mjRow(1)], { page: 2, page_size: 25, total: 80 }),
    );

    await act(async () => {
      await hook.result.current.loadLogs(2, 25);
    });

    expect(hook.result.current.activePage).toBe(2);
    expect(hook.result.current.pageSize).toBe(25);
    expect(hook.result.current.logCount).toBe(80);
  });

  it('reports a refused query and clears loading', async () => {
    const hook = await mount();
    API.get.mockResolvedValue({
      data: { success: false, message: 'mj log denied' },
    });

    await act(async () => {
      await hook.result.current.loadLogs(1, 10);
    });

    expect(showError).toHaveBeenCalledWith('mj log denied');
    expect(hook.result.current.loading).toBe(false);
  });

  it('remembers the page size under an mj-specific key', async () => {
    const hook = await mount();

    await act(async () => {
      await hook.result.current.handlePageSizeChange(50);
    });

    expect(window.localStorage.getItem('mj-page-size')).toBe('50');
    expect(window.localStorage.getItem('page-size')).toBeNull();
    expect(API.get.mock.calls.at(-1)[0]).toContain('?p=1&page_size=50');
  });

  it('seeds the first request from the remembered page size', async () => {
    window.localStorage.setItem('mj-page-size', '30');
    API.get.mockResolvedValue(mjPage([mjRow(1)], { page_size: 30 }));

    const hook = await mount();

    expect(API.get.mock.calls[0][0]).toContain('page_size=30');
    expect(hook.result.current.pageSize).toBe(30);
  });

  it('handlePageChange keeps the current page size', async () => {
    const hook = await mount();
    API.get.mockClear();

    await act(async () => {
      hook.result.current.handlePageChange(6);
    });

    expect(API.get.mock.calls[0][0]).toContain('?p=6&page_size=10');
  });

  it('defaults the visible window to the last 30 days', async () => {
    const hook = await mount();

    const { start_timestamp, end_timestamp } =
      hook.result.current.getFormValues();
    const spanDays =
      (Date.parse(end_timestamp) - Date.parse(start_timestamp)) / 86400000;

    expect(spanDays).toBeGreaterThan(29.9);
    expect(spanDays).toBeLessThan(30.2);
  });
});

describe('useMjLogsData — row shaping', () => {
  it('gives every row a string key and leaves submit_time alone', async () => {
    const hook = await mount();

    const rows = hook.result.current.enrichLogs([mjRow(11), mjRow(12)]);

    expect(rows.map((r) => r.key)).toEqual(['11', '12']);
    expect(rows[0].submit_time).toBe(1772323200000);
    expect(rows[0].mj_id).toBe('mj-11');
  });

  it('does not mutate the rows handed to it', async () => {
    const hook = await mount();
    const input = mjRow(1);

    hook.result.current.enrichLogs([input]);

    expect(input.key).toBeUndefined();
  });

  // DEFECT (see report): enrichLogs derives the display timestamp from
  // `created_at`, but the Midjourney record has no such field — the entity
  // only carries submit_time/start_time/finish_time. Every row therefore gets
  // a NaN timestamp string. Un-skip once it reads submit_time.
  it.skip('derives a usable display timestamp for an mj row', async () => {
    const hook = await mount();

    const [row] = hook.result.current.enrichLogs([mjRow(11)]);

    expect(Number.isNaN(Date.parse(row.timestamp2string))).toBe(false);
  });

  it('syncPageData tolerates a payload with no items', async () => {
    const hook = await mount();

    act(() => {
      hook.result.current.syncPageData({});
    });

    expect(hook.result.current.logs).toEqual([]);
    expect(hook.result.current.logCount).toBe(0);
    expect(hook.result.current.activePage).toBe(1);
  });
});

describe('useMjLogsData — banner and column preferences', () => {
  it('raises the notify banner until the user opts in', async () => {
    const first = await mount();
    expect(first.result.current.showBanner).toBe(true);

    window.localStorage.setItem('mj_notify_enabled', 'true');
    const second = await mount();
    expect(second.result.current.showBanner).toBe(false);
  });

  it('treats any value other than "true" as opted out', async () => {
    window.localStorage.setItem('mj_notify_enabled', 'false');
    const hook = await mount();
    expect(hook.result.current.showBanner).toBe(true);
  });

  it('hides channel and submit-result columns from a normal user', async () => {
    const { result } = await mount();

    await waitFor(() =>
      expect(result.current.visibleColumns.channel).toBe(false),
    );
    expect(result.current.visibleColumns.submit_result).toBe(false);
    expect(result.current.visibleColumns.prompt).toBe(true);
    expect(
      window.localStorage.getItem('mj-logs-table-columns-user'),
    ).not.toBeNull();
  });

  it('shows channel and submit-result columns to an admin', async () => {
    isAdmin.mockReturnValue(true);
    const { result } = await mount();

    await waitFor(() =>
      expect(result.current.visibleColumns.channel).toBe(true),
    );
    expect(result.current.visibleColumns.submit_result).toBe(true);
    expect(
      window.localStorage.getItem('mj-logs-table-columns-admin'),
    ).not.toBeNull();
  });

  it('re-hides admin columns a demoted user still has stored', async () => {
    window.localStorage.setItem(
      'mj-logs-table-columns-user',
      JSON.stringify({ channel: true, submit_result: true, image: false }),
    );

    const { result } = await mount();

    await waitFor(() =>
      expect(result.current.visibleColumns.channel).toBe(false),
    );
    expect(result.current.visibleColumns.submit_result).toBe(false);
    expect(result.current.visibleColumns.image).toBe(false);
  });

  it('select-all still withholds the admin columns from a normal user', async () => {
    const { result } = await mount();
    await waitFor(() =>
      expect(result.current.visibleColumns.prompt).toBe(true),
    );

    act(() => {
      result.current.handleSelectAll(true);
    });

    expect(result.current.visibleColumns.channel).toBe(false);
    expect(result.current.visibleColumns.submit_result).toBe(false);
    expect(result.current.visibleColumns.prompt_en).toBe(true);
  });

  it('toggling one column persists the whole map', async () => {
    const { result } = await mount();
    await waitFor(() => expect(result.current.visibleColumns.image).toBe(true));

    act(() => {
      result.current.handleColumnVisibilityChange('image', false);
    });

    await waitFor(() =>
      expect(
        JSON.parse(window.localStorage.getItem('mj-logs-table-columns-user'))
          .image,
      ).toBe(false),
    );
  });
});

describe('useMjLogsData — modals and clipboard', () => {
  it('opens the content modal with the given text', async () => {
    const { result } = await mount();

    act(() => {
      result.current.openContentModal('NSFW prompt rejected');
    });

    expect(result.current.isModalOpen).toBe(true);
    expect(result.current.modalContent).toBe('NSFW prompt rejected');
  });

  it('opens the image modal with the given url', async () => {
    const { result } = await mount();

    act(() => {
      result.current.openImageModal('https://cdn.example/i.png');
    });

    expect(result.current.isModalOpenurl).toBe(true);
    expect(result.current.modalImageUrl).toBe('https://cdn.example/i.png');
  });

  it('echoes the copied text on success and falls back to a modal otherwise', async () => {
    const { result } = await mount();

    copy.mockResolvedValue(true);
    await act(async () => {
      await result.current.copyText('mj-1');
    });
    expect(showSuccess).toHaveBeenCalledWith('已复制：mj-1');

    copy.mockResolvedValue(false);
    await act(async () => {
      await result.current.copyText('mj-1');
    });
    expect(Modal.error).toHaveBeenCalledWith(
      expect.objectContaining({ content: 'mj-1' }),
    );
  });
});
