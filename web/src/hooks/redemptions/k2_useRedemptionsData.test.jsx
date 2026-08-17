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
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';

vi.mock('../../helpers', () => ({
  API: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
  copy: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  getTableCompactMode: () => false,
  setTableCompactMode: vi.fn(),
}));

vi.mock('@douyinfe/semi-ui', () => ({
  Modal: {
    error: vi.fn(),
    info: vi.fn(),
    confirm: vi.fn(),
    destroyAll: vi.fn(),
  },
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key, opts) => {
      let out = key;
      if (opts && typeof opts === 'object') {
        for (const [k, v] of Object.entries(opts)) {
          out = out.split(`{{${k}}}`).join(String(v));
        }
      }
      return out;
    },
  }),
}));

import { useRedemptionsData } from './useRedemptionsData';
import { API, copy, showError, showSuccess } from '../../helpers';
import { Modal } from '@douyinfe/semi-ui';
import { REDEMPTION_STATUS } from '../../constants/redemption.constants';

const code = (id, over = {}) => ({
  id,
  name: `code-${id}`,
  key: `KEY${id}`,
  quota: 10000,
  status: REDEMPTION_STATUS.UNUSED,
  expired_time: 0,
  ...over,
});

const page = (items, extra = {}) => ({
  data: {
    success: true,
    data: { items, total: items.length, page: 1, ...extra },
  },
});

const mount = async () => {
  const hook = renderHook(() => useRedemptionsData());
  await waitFor(() => expect(hook.result.current.loading).toBe(false));
  return hook;
};

const withForm = (hook, values) =>
  act(() => {
    hook.result.current.setFormApi({ getValues: () => values });
  });

beforeEach(() => {
  vi.clearAllMocks();
  API.get.mockResolvedValue(page([code(1), code(2)]));
});

afterEach(() => {
  vi.useRealTimers();
});

describe('useRedemptionsData — listing', () => {
  it('loads the first page with the state page size on mount', async () => {
    const { result } = await mount();

    expect(API.get).toHaveBeenCalledWith('/api/redemption/?p=1&page_size=10');
    expect(result.current.redemptions.map((r) => r.id)).toEqual([1, 2]);
    expect(result.current.tokenCount).toBe(2);
    expect(result.current.activePage).toBe(1);
  });

  it('clamps a non-positive page number from the server up to 1', async () => {
    API.get.mockResolvedValue(page([code(1)], { page: 0 }));
    const { result } = await mount();
    expect(result.current.activePage).toBe(1);

    API.get.mockResolvedValue(page([code(1)], { page: 4 }));
    await act(async () => {
      await result.current.loadRedemptions(4, 10);
    });
    expect(result.current.activePage).toBe(4);
  });

  it('shows the server message and clears loading when the list fails', async () => {
    API.get.mockResolvedValue({
      data: { success: false, message: 'redemption list denied' },
    });
    const { result } = await mount();

    expect(showError).toHaveBeenCalledWith('redemption list denied');
    expect(result.current.redemptions).toEqual([]);
    expect(result.current.loading).toBe(false);
  });

  it('turns a transport failure into a toast and still clears loading', async () => {
    API.get.mockRejectedValue(new Error('connection reset'));
    const { result } = await mount();

    expect(showError).toHaveBeenCalledWith('connection reset');
    expect(result.current.loading).toBe(false);
  });
});

describe('useRedemptionsData — search and paging', () => {
  it('falls back to the plain list when the keyword is blank', async () => {
    const hook = await mount();
    withForm(hook, { searchKeyword: '' });
    API.get.mockClear();

    await act(async () => {
      await hook.result.current.searchRedemptions();
    });

    expect(API.get).toHaveBeenCalledWith('/api/redemption/?p=1&page_size=10');
    expect(hook.result.current.searching).toBe(false);
  });

  it('queries the search endpoint and replaces the visible rows', async () => {
    const hook = await mount();
    withForm(hook, { searchKeyword: 'promo' });

    API.get.mockResolvedValue(page([code(9)], { total: 1, page: 1 }));
    await act(async () => {
      await hook.result.current.searchRedemptions();
    });

    expect(API.get).toHaveBeenCalledWith(
      '/api/redemption/search?keyword=promo&p=1&page_size=10',
    );
    expect(hook.result.current.redemptions.map((r) => r.id)).toEqual([9]);
    expect(hook.result.current.tokenCount).toBe(1);
  });

  it('reports a failed search without wiping the table', async () => {
    const hook = await mount();
    withForm(hook, { searchKeyword: 'promo' });

    API.get.mockResolvedValue({
      data: { success: false, message: 'search rejected' },
    });
    await act(async () => {
      await hook.result.current.searchRedemptions();
    });

    expect(showError).toHaveBeenCalledWith('search rejected');
    expect(hook.result.current.redemptions.map((r) => r.id)).toEqual([1, 2]);
    expect(hook.result.current.searching).toBe(false);
  });

  it('page changes stay inside the active search instead of dropping it', async () => {
    const hook = await mount();
    withForm(hook, { searchKeyword: 'promo' });
    API.get.mockClear();

    await act(async () => {
      hook.result.current.handlePageChange(3);
    });

    // The search route is re-queried; the unfiltered list route is not.
    expect(API.get).toHaveBeenCalledTimes(1);
    expect(API.get.mock.calls[0][0]).toContain('/api/redemption/search');
    expect(API.get).not.toHaveBeenCalledWith(
      '/api/redemption/?p=3&page_size=10',
    );
  });

  // DEFECT (see report): searchRedemptions hard-codes p=1, so paging through
  // search results is impossible — every paginator click re-fetches page 1
  // while the control marches on. useUsersData.searchUsers takes the page
  // index for exactly this reason. Un-skip once search accepts a page.
  it.skip('page changes carry the page index into the search query', async () => {
    const hook = await mount();
    withForm(hook, { searchKeyword: 'promo' });
    API.get.mockClear();

    await act(async () => {
      hook.result.current.handlePageChange(3);
    });

    expect(API.get).toHaveBeenCalledWith(
      '/api/redemption/search?keyword=promo&p=3&page_size=10',
    );
    expect(hook.result.current.activePage).toBe(3);
  });

  it('page changes without a search request the plain page', async () => {
    const hook = await mount();
    withForm(hook, { searchKeyword: '' });
    API.get.mockClear();

    await act(async () => {
      hook.result.current.handlePageChange(3);
    });

    expect(API.get).toHaveBeenCalledWith('/api/redemption/?p=3&page_size=10');
  });

  it('a page-size change reloads from page 1 with the new size', async () => {
    const hook = await mount();
    withForm(hook, { searchKeyword: '' });
    API.get.mockClear();

    await act(async () => {
      hook.result.current.handlePageSizeChange(25);
    });

    expect(API.get).toHaveBeenCalledWith('/api/redemption/?p=1&page_size=25');
    expect(hook.result.current.pageSize).toBe(25);
  });

  it('refresh honours the active search', async () => {
    const hook = await mount();
    withForm(hook, { searchKeyword: 'promo' });
    API.get.mockClear();

    await act(async () => {
      await hook.result.current.refresh();
    });

    expect(API.get).toHaveBeenCalledWith(
      '/api/redemption/search?keyword=promo&p=1&page_size=10',
    );
  });

  it('getFormValues normalises a missing keyword to an empty string', async () => {
    const hook = await mount();
    expect(hook.result.current.getFormValues()).toEqual({ searchKeyword: '' });

    withForm(hook, { searchKeyword: 'abc' });
    expect(hook.result.current.getFormValues()).toEqual({
      searchKeyword: 'abc',
    });
  });
});

describe('useRedemptionsData — manage', () => {
  it('delete hits the id route', async () => {
    const { result } = await mount();
    API.delete.mockResolvedValue({ data: { success: true, data: null } });

    await act(async () => {
      await result.current.manageRedemption(
        1,
        'delete',
        result.current.redemptions[0],
      );
    });

    expect(API.delete).toHaveBeenCalledWith('/api/redemption/1/');
    expect(showSuccess).toHaveBeenCalled();
    expect(result.current.loading).toBe(false);
  });

  it('enable restores the UNUSED status, disable sets DISABLED', async () => {
    const { result } = await mount();
    API.put.mockResolvedValue({
      data: { success: true, data: { status: REDEMPTION_STATUS.DISABLED } },
    });

    const record = result.current.redemptions[0];
    await act(async () => {
      await result.current.manageRedemption(1, 'disable', record);
    });

    expect(API.put).toHaveBeenCalledWith('/api/redemption/?status_only=true', {
      id: 1,
      status: REDEMPTION_STATUS.DISABLED,
    });
    expect(record.status).toBe(REDEMPTION_STATUS.DISABLED);

    API.put.mockResolvedValue({
      data: { success: true, data: { status: REDEMPTION_STATUS.UNUSED } },
    });
    await act(async () => {
      await result.current.manageRedemption(1, 'enable', record);
    });

    expect(API.put).toHaveBeenLastCalledWith(
      '/api/redemption/?status_only=true',
      { id: 1, status: REDEMPTION_STATUS.UNUSED },
    );
    expect(record.status).toBe(REDEMPTION_STATUS.UNUSED);
  });

  it('rejects an unknown action instead of firing a request', async () => {
    const { result } = await mount();

    await act(async () => {
      await result.current.manageRedemption(1, 'teleport', code(1));
    });

    expect(API.put).not.toHaveBeenCalled();
    expect(API.delete).not.toHaveBeenCalled();
    expect(showError).toHaveBeenCalledWith('Unknown operation type');
    expect(result.current.loading).toBe(false);
  });

  it('surfaces a server-side rejection', async () => {
    const { result } = await mount();
    API.delete.mockResolvedValue({
      data: { success: false, message: 'already used' },
    });

    await act(async () => {
      await result.current.manageRedemption(1, 'delete', code(1));
    });

    expect(showError).toHaveBeenCalledWith('already used');
  });

  it('survives a transport failure with the loading flag cleared', async () => {
    const { result } = await mount();
    API.delete.mockRejectedValue(new Error('socket closed'));

    await act(async () => {
      await result.current.manageRedemption(1, 'delete', code(1));
    });

    expect(showError).toHaveBeenCalledWith('socket closed');
    expect(result.current.loading).toBe(false);
  });
});

describe('useRedemptionsData — row styling by expiry', () => {
  const grey = { style: { background: 'var(--semi-color-disabled-border)' } };
  const nowSec = () => Math.floor(Date.now() / 1000);

  it('leaves an unused, never-expiring code un-greyed', async () => {
    const { result } = await mount();
    // 0 is the sentinel the edit modal writes for "never expires".
    expect(result.current.handleRow(code(1, { expired_time: 0 }))).toEqual({});
  });

  it('leaves an unused code with a future expiry un-greyed', async () => {
    const { result } = await mount();
    expect(
      result.current.handleRow(code(1, { expired_time: nowSec() + 3600 })),
    ).toEqual({});
  });

  it('greys an unused code whose expiry has passed', async () => {
    const { result } = await mount();
    expect(
      result.current.handleRow(code(1, { expired_time: nowSec() - 1 })),
    ).toEqual(grey);
  });

  it('greys used and disabled codes regardless of expiry', async () => {
    const { result } = await mount();
    expect(
      result.current.handleRow(
        code(1, { status: REDEMPTION_STATUS.USED, expired_time: 0 }),
      ),
    ).toEqual(grey);
    expect(
      result.current.handleRow(
        code(1, {
          status: REDEMPTION_STATUS.DISABLED,
          expired_time: nowSec() + 3600,
        }),
      ),
    ).toEqual(grey);
  });
});

describe('useRedemptionsData — clipboard and batch', () => {
  it('copyText reports success and falls back to a modal otherwise', async () => {
    const { result } = await mount();

    copy.mockResolvedValue(true);
    await act(async () => {
      await result.current.copyText('KEY1');
    });
    expect(showSuccess).toHaveBeenCalledWith('已复制到剪贴板！');

    copy.mockResolvedValue(false);
    await act(async () => {
      await result.current.copyText('KEY1');
    });
    expect(Modal.error).toHaveBeenCalledWith(
      expect.objectContaining({ content: 'KEY1' }),
    );
  });

  it('refuses a batch copy with nothing selected', async () => {
    const { result } = await mount();

    await act(async () => {
      await result.current.batchCopyRedemptions();
    });

    expect(copy).not.toHaveBeenCalled();
    expect(showError).toHaveBeenCalledWith('请至少选择一个兑换码！');
  });

  it('copies name and key per line for the selection', async () => {
    const { result } = await mount();
    copy.mockResolvedValue(true);

    act(() => {
      result.current.setSelectedKeys([code(1), code(2)]);
    });
    await act(async () => {
      await result.current.batchCopyRedemptions();
    });

    expect(copy).toHaveBeenCalledWith('code-1    KEY1\ncode-2    KEY2\n');
  });

  it('asks for confirmation before purging invalid codes', async () => {
    const { result } = await mount();

    await act(async () => {
      await result.current.batchDeleteRedemptions();
    });

    expect(Modal.confirm).toHaveBeenCalledTimes(1);
    expect(API.delete).not.toHaveBeenCalled();
  });

  it('purges invalid codes and reports the deleted count on confirm', async () => {
    const { result } = await mount();
    await act(async () => {
      await result.current.batchDeleteRedemptions();
    });

    API.delete.mockResolvedValue({ data: { success: true, data: 7 } });
    API.get.mockClear();
    const { onOk } = Modal.confirm.mock.calls[0][0];
    await act(async () => {
      await onOk();
    });

    expect(API.delete).toHaveBeenCalledWith('/api/redemption/invalid');
    expect(showSuccess).toHaveBeenCalledWith('已删除 7 条失效兑换码');
    // The table is reloaded so the purged rows disappear.
    expect(API.get).toHaveBeenCalled();
    expect(result.current.loading).toBe(false);
  });

  it('reports a refused purge without reloading', async () => {
    const { result } = await mount();
    await act(async () => {
      await result.current.batchDeleteRedemptions();
    });

    API.delete.mockResolvedValue({
      data: { success: false, message: 'not allowed' },
    });
    API.get.mockClear();
    const { onOk } = Modal.confirm.mock.calls[0][0];
    await act(async () => {
      await onOk();
    });

    expect(showError).toHaveBeenCalledWith('not allowed');
    expect(API.get).not.toHaveBeenCalled();
    expect(result.current.loading).toBe(false);
  });
});

describe('useRedemptionsData — local row bookkeeping', () => {
  it('removeRecord drops exactly the matching key', async () => {
    const { result } = await mount();

    act(() => {
      result.current.removeRecord('KEY1');
    });

    expect(result.current.redemptions.map((r) => r.key)).toEqual(['KEY2']);
  });

  it('removeRecord ignores an unknown key and a null key', async () => {
    const { result } = await mount();

    act(() => {
      result.current.removeRecord('NOPE');
    });
    expect(result.current.redemptions).toHaveLength(2);

    act(() => {
      result.current.removeRecord(null);
    });
    expect(result.current.redemptions).toHaveLength(2);
  });

  it('rowSelection.onChange stores the selected rows', async () => {
    const { result } = await mount();

    act(() => {
      result.current.rowSelection.onChange(['KEY1'], [code(1)]);
    });

    expect(result.current.selectedKeys.map((r) => r.key)).toEqual(['KEY1']);
  });

  it('closeEdit hides the modal now and clears the draft after the animation', async () => {
    const { result } = await mount();
    vi.useFakeTimers();

    act(() => {
      result.current.setEditingRedemption({ id: 9 });
      result.current.setShowEdit(true);
    });
    expect(result.current.showEdit).toBe(true);

    act(() => {
      result.current.closeEdit();
    });
    expect(result.current.showEdit).toBe(false);
    expect(result.current.editingRedemption).toEqual({ id: 9 });

    act(() => {
      vi.advanceTimersByTime(500);
    });
    expect(result.current.editingRedemption).toEqual({ id: undefined });
  });
});
