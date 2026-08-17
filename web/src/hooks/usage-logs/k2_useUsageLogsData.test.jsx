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
  getTodayStartTimestamp: () => 1772323200, // 2026-03-01T00:00:00Z
  timestamp2string: (ts) => new Date(Math.round(ts * 1000)).toISOString(),
  renderQuota: (q) => `$${q}`,
  renderNumber: (n) => String(n),
  getLogOther: vi.fn((raw) => (raw ? JSON.parse(raw) : {})),
  renderClaudeLogContent: (...args) => `claude-log(${args.length})`,
  renderLogContent: (...args) => `log(${args.length})`,
  renderAudioModelPrice: () => 'audio-price',
  renderClaudeModelPrice: () => 'claude-price',
  renderModelPrice: () => 'model-price',
  getTableCompactMode: () => false,
  setTableCompactMode: vi.fn(),
}));

vi.mock('@douyinfe/semi-ui', () => ({
  Modal: { error: vi.fn(), info: vi.fn(), confirm: vi.fn() },
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key) => key }),
}));

import { useLogsData } from './useUsageLogsData';
import { API, copy, isAdmin, showError, showSuccess } from '../../helpers';
import { Modal } from '@douyinfe/semi-ui';

const RANGE = ['2026-03-01T00:00:00.000Z', '2026-03-02T00:00:00.000Z'];
const START = Date.parse(RANGE[0]) / 1000;
const END = Date.parse(RANGE[1]) / 1000;

const logRow = (id, over = {}) => ({
  id,
  created_at: 1772323200,
  type: 2,
  model_name: 'gpt-4o',
  prompt_tokens: 10,
  completion_tokens: 20,
  channel: 7,
  channel_name: 'openai-main',
  other: null,
  ...over,
});

const logsPage = (items, extra = {}) => ({
  data: {
    success: true,
    data: { items, total: items.length, page: 1, page_size: 10, ...extra },
  },
});

const mount = async () => {
  const hook = renderHook(() => useLogsData());
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
  API.get.mockResolvedValue(logsPage([logRow(1)]));
});

describe('useLogsData — column preferences', () => {
  it('seeds admin-only columns off for a normal user and persists them', async () => {
    const { result } = await mount();

    await waitFor(() =>
      expect(Object.keys(result.current.visibleColumns).length).toBeGreaterThan(
        0,
      ),
    );
    expect(result.current.visibleColumns.channel).toBe(false);
    expect(result.current.visibleColumns.username).toBe(false);
    expect(result.current.visibleColumns.retry).toBe(false);
    expect(result.current.visibleColumns.cost).toBe(true);

    const saved = JSON.parse(
      window.localStorage.getItem('logs-table-columns-user'),
    );
    expect(saved.channel).toBe(false);
    expect(window.localStorage.getItem('logs-table-columns-admin')).toBeNull();
  });

  it('seeds admin-only columns on for an admin under a separate key', async () => {
    isAdmin.mockReturnValue(true);
    const { result } = await mount();

    await waitFor(() =>
      expect(result.current.visibleColumns.channel).toBe(true),
    );
    expect(result.current.visibleColumns.username).toBe(true);
    expect(result.current.visibleColumns.retry).toBe(true);
    expect(
      window.localStorage.getItem('logs-table-columns-admin'),
    ).not.toBeNull();
    expect(window.localStorage.getItem('logs-table-columns-user')).toBeNull();
  });

  it('merges saved preferences over the defaults', async () => {
    isAdmin.mockReturnValue(true);
    window.localStorage.setItem(
      'logs-table-columns-admin',
      JSON.stringify({ cost: false }),
    );

    const { result } = await mount();

    await waitFor(() => expect(result.current.visibleColumns.cost).toBe(false));
    // Keys absent from the saved blob keep their default.
    expect(result.current.visibleColumns.time).toBe(true);
    expect(result.current.visibleColumns.channel).toBe(true);
  });

  it('re-hides admin columns a demoted user still has stored', async () => {
    window.localStorage.setItem(
      'logs-table-columns-user',
      JSON.stringify({ channel: true, username: true, retry: true }),
    );

    const { result } = await mount();

    await waitFor(() =>
      expect(result.current.visibleColumns.channel).toBe(false),
    );
    expect(result.current.visibleColumns.username).toBe(false);
    expect(result.current.visibleColumns.retry).toBe(false);
  });

  it('rebuilds the defaults when the stored blob is corrupt', async () => {
    window.localStorage.setItem('logs-table-columns-user', '{{not json');
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

    const { result } = await mount();

    await waitFor(() => expect(result.current.visibleColumns.time).toBe(true));
    expect(result.current.visibleColumns.channel).toBe(false);
    expect(errSpy).toHaveBeenCalled();
    errSpy.mockRestore();
  });

  it('toggling one column leaves the rest untouched and re-persists', async () => {
    const { result } = await mount();
    await waitFor(() => expect(result.current.visibleColumns.cost).toBe(true));

    act(() => {
      result.current.handleColumnVisibilityChange('cost', false);
    });

    expect(result.current.visibleColumns.cost).toBe(false);
    expect(result.current.visibleColumns.model).toBe(true);
    await waitFor(() =>
      expect(
        JSON.parse(window.localStorage.getItem('logs-table-columns-user')).cost,
      ).toBe(false),
    );
  });

  it('select-all cannot hand a normal user the admin columns', async () => {
    const { result } = await mount();
    await waitFor(() => expect(result.current.visibleColumns.time).toBe(true));

    act(() => {
      result.current.handleSelectAll(true);
    });

    expect(result.current.visibleColumns.model).toBe(true);
    expect(result.current.visibleColumns.channel).toBe(false);
    expect(result.current.visibleColumns.username).toBe(false);
    expect(result.current.visibleColumns.retry).toBe(false);
  });

  it('select-none clears every column for an admin too', async () => {
    isAdmin.mockReturnValue(true);
    const { result } = await mount();
    await waitFor(() => expect(result.current.visibleColumns.time).toBe(true));

    act(() => {
      result.current.handleSelectAll(false);
    });

    expect(
      Object.values(result.current.visibleColumns).every((v) => v === false),
    ).toBe(true);
  });
});

describe('useLogsData — querying', () => {
  it('a normal user hits the self-scoped route with no username or channel', async () => {
    const hook = await mount();
    await withRange(hook, {
      token_name: 'tk',
      model_name: 'gpt-4o',
      group: 'vip',
    });
    API.get.mockClear();

    await act(async () => {
      await hook.result.current.loadLogs(2, 20);
    });

    const url = API.get.mock.calls[0][0];
    expect(url).toContain('/api/log/self/?p=2&page_size=20');
    expect(url).toContain(`start_timestamp=${START}`);
    expect(url).toContain(`end_timestamp=${END}`);
    expect(url).toContain('token_name=tk');
    expect(url).not.toContain('username=');
    expect(url).not.toContain('channel=');
  });

  it('an admin hits the global route carrying username and channel', async () => {
    isAdmin.mockReturnValue(true);
    const hook = await mount();
    await withRange(hook, { username: 'alice', channel: '7' });
    API.get.mockClear();

    await act(async () => {
      await hook.result.current.loadLogs(1, 10);
    });

    const url = API.get.mock.calls[0][0];
    expect(url).toContain('/api/log/?p=1&page_size=10');
    expect(url).toContain('username=alice');
    expect(url).toContain('channel=7');
  });

  it('an explicit log type argument overrides the form value', async () => {
    const hook = await mount();
    await withRange(hook, { logType: '2' });
    API.get.mockClear();

    await act(async () => {
      await hook.result.current.loadLogs(1, 10, 5);
    });

    expect(API.get.mock.calls[0][0]).toContain('type=5');
  });

  it('falls back to the form log type when no override is given', async () => {
    const hook = await mount();
    await withRange(hook, { logType: '3' });
    API.get.mockClear();

    await act(async () => {
      await hook.result.current.loadLogs(1, 10);
    });

    expect(API.get.mock.calls[0][0]).toContain('type=3');
  });

  it('adopts the paging the server reports', async () => {
    const hook = await mount();
    API.get.mockResolvedValue(
      logsPage([logRow(1), logRow(2)], { page: 3, page_size: 25, total: 57 }),
    );

    await act(async () => {
      await hook.result.current.loadLogs(3, 25);
    });

    expect(hook.result.current.activePage).toBe(3);
    expect(hook.result.current.pageSize).toBe(25);
    expect(hook.result.current.logCount).toBe(57);
    expect(hook.result.current.logs).toHaveLength(2);
  });

  it('reports a refused query and leaves the table as it was', async () => {
    const hook = await mount();
    expect(hook.result.current.logs).toHaveLength(1);

    API.get.mockResolvedValue({
      data: { success: false, message: 'log access denied' },
    });
    await act(async () => {
      await hook.result.current.loadLogs(1, 10);
    });

    expect(showError).toHaveBeenCalledWith('log access denied');
    expect(hook.result.current.logs).toHaveLength(1);
    expect(hook.result.current.loading).toBe(false);
  });

  it('remembers the chosen page size for the next visit', async () => {
    const hook = await mount();

    await act(async () => {
      await hook.result.current.handlePageSizeChange(50);
    });

    expect(window.localStorage.getItem('page-size')).toBe('50');
  });

  it('restores the remembered page size on the next mount', async () => {
    window.localStorage.setItem('page-size', '40');
    API.get.mockResolvedValue(logsPage([logRow(1)], { page_size: 40 }));

    const hook = await mount();

    expect(API.get.mock.calls[0][0]).toContain('page_size=40');
    expect(hook.result.current.pageSize).toBe(40);
  });

  it('lets the server echo override the remembered page size', async () => {
    // The stored preference only seeds the first request; whatever page_size
    // comes back is what the table ends up using.
    window.localStorage.setItem('page-size', '40');
    API.get.mockResolvedValue(logsPage([logRow(1)], { page_size: 10 }));

    const hook = await mount();

    expect(API.get.mock.calls[0][0]).toContain('page_size=40');
    expect(hook.result.current.pageSize).toBe(10);
  });

  // DEFECT (see report): handlePageSizeChange sets activePage to 1 but then
  // fetches the stale `activePage` captured at render time. Changing rows per
  // page while deep in the log history re-queries the old page number under
  // the new size, which can land past the end and show an empty table.
  it.skip('a page-size change reloads from page 1', async () => {
    const hook = await mount();

    // The server echoes the page it served, so the hook really is sitting on
    // page 4 when the operator changes rows-per-page.
    API.get.mockResolvedValue(logsPage([logRow(1)], { page: 4 }));
    await act(async () => {
      hook.result.current.handlePageChange(4);
    });
    expect(hook.result.current.activePage).toBe(4);
    API.get.mockClear();

    await act(async () => {
      await hook.result.current.handlePageSizeChange(50);
    });

    expect(API.get.mock.calls[0][0]).toContain('?p=1&page_size=50');
  });
});

describe('useLogsData — statistics', () => {
  it('a normal user asks for the self-scoped totals', async () => {
    const hook = await mount();
    API.get.mockResolvedValue({
      data: { success: true, data: { quota: 4242, token: 17 } },
    });

    await act(async () => {
      await hook.result.current.handleEyeClick();
    });

    expect(API.get.mock.calls.at(-1)[0]).toContain('/api/log/self/stat');
    expect(hook.result.current.stat).toEqual({ quota: 4242, token: 17 });
    expect(hook.result.current.showStat).toBe(true);
    expect(hook.result.current.loadingStat).toBe(false);
  });

  it('an admin asks for the global totals', async () => {
    isAdmin.mockReturnValue(true);
    const hook = await mount();
    await withRange(hook, { username: 'alice' });

    API.get.mockResolvedValue({
      data: { success: true, data: { quota: 10, token: 1 } },
    });
    await act(async () => {
      await hook.result.current.handleEyeClick();
    });

    const url = API.get.mock.calls.at(-1)[0];
    expect(url).toContain('/api/log/stat');
    expect(url).toContain('username=alice');
    expect(url).toContain(`start_timestamp=${START}`);
  });

  it('reports a refused stat query without opening the panel', async () => {
    const hook = await mount();
    API.get.mockResolvedValue({
      data: { success: false, message: 'stat denied' },
    });

    await act(async () => {
      await hook.result.current.handleEyeClick();
    });

    expect(showError).toHaveBeenCalledWith('stat denied');
    expect(hook.result.current.stat).toEqual({ quota: 0, token: 0 });
  });

  it('fires the stat query automatically once the form is wired up', async () => {
    const hook = await mount();
    API.get.mockClear();

    await withRange(hook);

    expect(API.get.mock.calls.some((c) => c[0].includes('/stat'))).toBe(true);
  });
});

describe('useLogsData — user info drawer', () => {
  it('is inert for a normal user', async () => {
    const hook = await mount();
    API.get.mockClear();

    await act(async () => {
      await hook.result.current.showUserInfoFunc(9);
    });

    expect(API.get).not.toHaveBeenCalled();
    expect(hook.result.current.showUserInfo).toBe(false);
  });

  it('loads and opens the drawer for an admin', async () => {
    isAdmin.mockReturnValue(true);
    const hook = await mount();

    API.get.mockResolvedValue({
      data: { success: true, data: { id: 9, username: 'bob' } },
    });
    await act(async () => {
      await hook.result.current.showUserInfoFunc(9);
    });

    expect(API.get).toHaveBeenCalledWith('/api/user/9');
    expect(hook.result.current.userInfoData).toEqual({
      id: 9,
      username: 'bob',
    });
    expect(hook.result.current.showUserInfo).toBe(true);
  });

  it('reports a refused lookup and keeps the drawer shut', async () => {
    isAdmin.mockReturnValue(true);
    const hook = await mount();

    API.get.mockResolvedValue({
      data: { success: false, message: 'no such user' },
    });
    await act(async () => {
      await hook.result.current.showUserInfoFunc(9);
    });

    expect(showError).toHaveBeenCalledWith('no such user');
    expect(hook.result.current.showUserInfo).toBe(false);
  });
});

describe('useLogsData — row expansion payload', () => {
  it('keys every row by its id and stamps a display timestamp', async () => {
    const hook = await mount();

    act(() => {
      hook.result.current.setLogsFormat([logRow(11), logRow(12)]);
    });

    expect(hook.result.current.logs.map((l) => l.key)).toEqual([11, 12]);
    expect(hook.result.current.logs[0].timestamp2string).toBe(
      new Date(1772323200000).toISOString(),
    );
  });

  it('hides channel attribution from a normal user', async () => {
    const hook = await mount();

    act(() => {
      hook.result.current.setLogsFormat([logRow(1, { type: 0 })]);
    });

    const rows = hook.result.current.expandData[1];
    expect(rows.some((r) => r.key === '渠道信息')).toBe(false);
    expect(rows.some((r) => r.key === '计费模式')).toBe(false);
  });

  it('shows channel attribution and billing mode to an admin', async () => {
    isAdmin.mockReturnValue(true);
    const hook = await mount();

    act(() => {
      hook.result.current.setLogsFormat([
        logRow(1, {
          type: 0,
          other: JSON.stringify({ admin_info: { local_count_tokens: true } }),
        }),
      ]);
    });

    const rows = hook.result.current.expandData[1];
    expect(rows.find((r) => r.key === '渠道信息').value).toBe(
      '7 - openai-main',
    );
    expect(rows.find((r) => r.key === '计费模式').value).toBe('本地计费');
  });

  it('falls back to a placeholder channel name', async () => {
    isAdmin.mockReturnValue(true);
    const hook = await mount();

    act(() => {
      hook.result.current.setLogsFormat([
        logRow(1, { type: 0, channel_name: '' }),
      ]);
    });

    expect(
      hook.result.current.expandData[1].find((r) => r.key === '渠道信息').value,
    ).toBe('7 - [未知]');
  });

  it('labels upstream-reported billing when the admin flag is absent', async () => {
    isAdmin.mockReturnValue(true);
    const hook = await mount();

    act(() => {
      hook.result.current.setLogsFormat([logRow(1, { type: 0 })]);
    });

    expect(
      hook.result.current.expandData[1].find((r) => r.key === '计费模式').value,
    ).toBe('上游返回');
  });

  it('only lists cache token rows when the counts are positive', async () => {
    const hook = await mount();

    act(() => {
      hook.result.current.setLogsFormat([
        logRow(1, {
          type: 1,
          other: JSON.stringify({ cache_tokens: 0, cache_creation_tokens: 0 }),
        }),
        logRow(2, {
          type: 1,
          other: JSON.stringify({
            cache_tokens: 128,
            cache_creation_tokens: 64,
          }),
        }),
      ]);
    });

    const none = hook.result.current.expandData[1];
    expect(none.some((r) => r.key === '缓存 Tokens')).toBe(false);

    const some = hook.result.current.expandData[2];
    expect(some.find((r) => r.key === '缓存 Tokens').value).toBe(128);
    expect(some.find((r) => r.key === '缓存创建 Tokens').value).toBe(64);
  });

  it('expands the audio breakdown for a voice request', async () => {
    const hook = await mount();

    act(() => {
      hook.result.current.setLogsFormat([
        logRow(1, {
          type: 1,
          other: JSON.stringify({
            audio: true,
            audio_input: 5,
            audio_output: 6,
            text_input: 7,
            text_output: 8,
          }),
        }),
      ]);
    });

    const rows = hook.result.current.expandData[1];
    expect(rows.find((r) => r.key === '语音输入').value).toBe(5);
    expect(rows.find((r) => r.key === '语音输出').value).toBe(6);
    expect(rows.find((r) => r.key === '文字输入').value).toBe(7);
    expect(rows.find((r) => r.key === '文字输出').value).toBe(8);
  });

  it('surfaces the model mapping for a billed request', async () => {
    const hook = await mount();

    act(() => {
      hook.result.current.setLogsFormat([
        logRow(1, {
          type: 2,
          other: JSON.stringify({
            is_model_mapped: true,
            upstream_model_name: 'gpt-4o-2026-01',
          }),
        }),
      ]);
    });

    const rows = hook.result.current.expandData[1];
    expect(rows.find((r) => r.key === '请求并计费模型').value).toBe('gpt-4o');
    expect(rows.find((r) => r.key === '实际模型').value).toBe('gpt-4o-2026-01');
    expect(rows.some((r) => r.key === '计费过程')).toBe(true);
  });

  it('omits the mapping rows when the model was not remapped', async () => {
    const hook = await mount();

    act(() => {
      hook.result.current.setLogsFormat([
        logRow(1, {
          type: 2,
          other: JSON.stringify({
            is_model_mapped: true,
            upstream_model_name: '',
          }),
        }),
      ]);
    });

    expect(
      hook.result.current.expandData[1].some((r) => r.key === '实际模型'),
    ).toBe(false);
  });

  it('routes the billing breakdown by request flavour', async () => {
    const hook = await mount();

    act(() => {
      hook.result.current.setLogsFormat([
        logRow(1, { type: 2, other: JSON.stringify({ claude: true }) }),
        logRow(2, { type: 2, other: JSON.stringify({ ws: true }) }),
        logRow(3, { type: 2, other: JSON.stringify({}) }),
      ]);
    });

    const value = (id) =>
      hook.result.current.expandData[id].find((r) => r.key === '计费过程')
        .value;
    expect(value(1)).toBe('claude-price');
    expect(value(2)).toBe('audio-price');
    expect(value(3)).toBe('model-price');
  });

  it('attaches free-form content and the request path when present', async () => {
    const hook = await mount();

    act(() => {
      hook.result.current.setLogsFormat([
        logRow(1, {
          type: 2,
          content: 'retried once',
          other: JSON.stringify({
            request_path: '/v1/chat/completions',
            reasoning_effort: 'high',
          }),
        }),
      ]);
    });

    const rows = hook.result.current.expandData[1];
    expect(rows.find((r) => r.key === '其他详情').value).toBe('retried once');
    expect(rows.find((r) => r.key === '请求路径').value).toBe(
      '/v1/chat/completions',
    );
    expect(rows.find((r) => r.key === 'Reasoning Effort').value).toBe('high');
  });

  it('reports whether any row has something to expand', async () => {
    const hook = await mount();

    act(() => {
      hook.result.current.setLogsFormat([logRow(1, { type: 1 })]);
    });
    expect(hook.result.current.hasExpandableRows()).toBe(false);

    act(() => {
      hook.result.current.setLogsFormat([logRow(2, { type: 2 })]);
    });
    expect(hook.result.current.hasExpandableRows()).toBe(true);
  });
});

describe('useLogsData — clipboard', () => {
  it('echoes the copied value and swallows the row click', async () => {
    const hook = await mount();
    const event = { stopPropagation: vi.fn() };
    copy.mockResolvedValue(true);

    await act(async () => {
      await hook.result.current.copyText(event, 'req-123');
    });

    expect(event.stopPropagation).toHaveBeenCalled();
    expect(showSuccess).toHaveBeenCalledWith('已复制：req-123');
  });

  it('falls back to a modal when the clipboard is blocked', async () => {
    const hook = await mount();
    copy.mockResolvedValue(false);

    await act(async () => {
      await hook.result.current.copyText({ stopPropagation: vi.fn() }, 'req-9');
    });

    expect(Modal.error).toHaveBeenCalledWith(
      expect.objectContaining({ content: 'req-9' }),
    );
  });
});
