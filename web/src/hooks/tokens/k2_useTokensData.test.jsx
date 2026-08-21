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
  encodeToBase64: (s) => `b64(${s})`,
  isV2Mode: vi.fn(() => false),
  v2Url: (p) => `/api/v2/acme${p}`,
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

import { useTokensData } from './useTokensData';
import { API, copy, showError, showSuccess, isV2Mode } from '../../helpers';
import { Modal } from '@douyinfe/semi-ui';

const page = (items, extra = {}) => ({
  data: {
    success: true,
    data: { items, total: items.length, page: 1, page_size: 10, ...extra },
  },
});

const tok = (id, over = {}) => ({
  id,
  name: `token-${id}`,
  key: `key${id}`,
  status: 1,
  remain_quota: 1000,
  used_quota: 0,
  ...over,
});

const mount = async (openFluent = vi.fn()) => {
  const hook = renderHook(() => useTokensData(openFluent));
  await waitFor(() => expect(hook.result.current.loading).toBe(false));
  return hook;
};

beforeEach(() => {
  vi.clearAllMocks();
  window.localStorage.clear();
  isV2Mode.mockReturnValue(false);
  API.get.mockResolvedValue(page([tok(1), tok(2)]));
});

afterEach(() => {
  vi.useRealTimers();
});

describe('useTokensData — listing', () => {
  it('loads page 1 through the v1 endpoint on mount', async () => {
    const { result } = await mount();

    expect(API.get).toHaveBeenCalledWith('/api/token/?p=1&size=10');
    expect(result.current.tokens.map((x) => x.id)).toEqual([1, 2]);
    expect(result.current.tokenCount).toBe(2);
    expect(result.current.activePage).toBe(1);
    expect(result.current.pageSize).toBe(10);
  });

  it('switches to the tenant-scoped v2 endpoint when v2 mode is on', async () => {
    isV2Mode.mockReturnValue(true);
    await mount();
    expect(API.get).toHaveBeenCalledWith('/api/v2/acme/tokens?p=1&size=10');
  });

  it('reports the server message instead of rendering rows on failure', async () => {
    API.get.mockResolvedValue({
      data: { success: false, message: 'token list denied' },
    });
    const { result } = await mount();

    expect(showError).toHaveBeenCalledWith('token list denied');
    expect(result.current.tokens).toEqual([]);
  });

  it('syncPageData falls back to safe defaults for a partial envelope', async () => {
    const { result } = await mount();

    act(() => {
      result.current.syncPageData({});
    });

    expect(result.current.tokens).toEqual([]);
    expect(result.current.tokenCount).toBe(0);
    expect(result.current.activePage).toBe(1);
    // page_size missing => keep whatever the table is already using.
    expect(result.current.pageSize).toBe(10);
  });

  it('refresh reloads the current page and clears the selection', async () => {
    const { result } = await mount();

    act(() => {
      result.current.setSelectedKeys([tok(1)]);
    });
    expect(result.current.selectedKeys).toHaveLength(1);

    API.get.mockClear();
    await act(async () => {
      await result.current.refresh(3);
    });

    expect(API.get).toHaveBeenCalledWith('/api/token/?p=3&size=10');
    expect(result.current.selectedKeys).toEqual([]);
  });

  it('a page-size change reloads from page 1, not the page in view', async () => {
    const { result } = await mount();
    API.get.mockClear();

    await act(async () => {
      await result.current.handlePageSizeChange(50);
    });

    expect(API.get).toHaveBeenCalledWith('/api/token/?p=1&size=50');
  });

  it('handlePageChange requests the page it was handed', async () => {
    const { result } = await mount();
    API.get.mockClear();

    await act(async () => {
      result.current.handlePageChange(4);
    });

    expect(API.get).toHaveBeenCalledWith('/api/token/?p=4&size=10');
  });
});

describe('useTokensData — search', () => {
  it('falls back to a plain page load when both fields are blank', async () => {
    const { result } = await mount();
    act(() => {
      result.current.setFormApi({ getValues: () => ({}) });
    });
    API.get.mockClear();

    await act(async () => {
      await result.current.searchTokens();
    });

    expect(API.get).toHaveBeenCalledWith('/api/token/?p=1&size=10');
    expect(result.current.searching).toBe(false);
  });

  it('v1 search consumes the flat array the v1 endpoint returns', async () => {
    const { result } = await mount();
    act(() => {
      result.current.setFormApi({
        getValues: () => ({ searchKeyword: 'prod', searchToken: '' }),
      });
    });

    API.get.mockResolvedValue({
      data: { success: true, data: [tok(7), tok(8), tok(9)] },
    });
    await act(async () => {
      await result.current.searchTokens();
    });

    expect(API.get).toHaveBeenCalledWith(
      '/api/token/search?keyword=prod&token=',
    );
    expect(result.current.tokens.map((x) => x.id)).toEqual([7, 8, 9]);
    expect(result.current.tokenCount).toBe(3);
    expect(result.current.activePage).toBe(1);
  });

  it('surfaces a failed search and leaves the previous rows untouched', async () => {
    const { result } = await mount();
    act(() => {
      result.current.setFormApi({
        getValues: () => ({ searchKeyword: 'x', searchToken: '' }),
      });
    });

    API.get.mockResolvedValue({
      data: { success: false, message: 'search blew up' },
    });
    await act(async () => {
      await result.current.searchTokens();
    });

    expect(showError).toHaveBeenCalledWith('search blew up');
    expect(result.current.tokens.map((x) => x.id)).toEqual([1, 2]);
  });

  // DEFECT (see report): in v2 mode searchTokens hits the SAME /tokens list
  // route, which answers with a {items,total,page,page_size} envelope and
  // ignores keyword/token entirely. The hook feeds that object straight into
  // setTokens and reads .length off it, so the table receives a non-array and
  // the count becomes undefined. Un-skip once the v2 search contract is fixed.
  it('v2 search unwraps the paginated envelope', async () => {
    isV2Mode.mockReturnValue(true);
    const { result } = await mount();
    act(() => {
      result.current.setFormApi({
        getValues: () => ({ searchKeyword: 'prod', searchToken: '' }),
      });
    });

    API.get.mockResolvedValue(page([tok(7)], { total: 1 }));
    await act(async () => {
      await result.current.searchTokens();
    });

    expect(Array.isArray(result.current.tokens)).toBe(true);
    expect(result.current.tokens.map((x) => x.id)).toEqual([7]);
    expect(result.current.tokenCount).toBe(1);
  });
});

describe('useTokensData — lifecycle actions', () => {
  it('disable sends status 2 to the status-only v1 route', async () => {
    const { result } = await mount();
    const record = result.current.tokens[0];
    API.put.mockResolvedValue({
      data: { success: true, data: { status: 2 } },
    });

    await act(async () => {
      await result.current.manageToken(1, 'disable', record);
    });

    expect(API.put).toHaveBeenCalledWith('/api/token/?status_only=true', {
      id: 1,
      status: 2,
    });
    expect(showSuccess).toHaveBeenCalled();
    expect(result.current.tokens[0].status).toBe(2);
    expect(result.current.loading).toBe(false);
  });

  it('enable sends status 1 and adopts the status the server echoes back', async () => {
    const { result } = await mount();
    const record = result.current.tokens[1];
    API.put.mockResolvedValue({
      data: { success: true, data: { status: 1 } },
    });

    await act(async () => {
      await result.current.manageToken(2, 'enable', record);
    });

    expect(API.put).toHaveBeenCalledWith('/api/token/?status_only=true', {
      id: 2,
      status: 1,
    });
    expect(result.current.tokens[1].status).toBe(1);
  });

  it('enable/disable go to the RESTful v2 route in v2 mode', async () => {
    isV2Mode.mockReturnValue(true);
    const { result } = await mount();
    API.put.mockResolvedValue({ data: { success: true, data: { status: 2 } } });

    await act(async () => {
      await result.current.manageToken(5, 'disable', result.current.tokens[0]);
    });

    expect(API.put).toHaveBeenCalledWith('/api/v2/acme/tokens/5', {
      id: 5,
      status: 2,
    });
  });

  it('delete calls DELETE and does not touch the record status', async () => {
    const { result } = await mount();
    const record = result.current.tokens[0];
    API.delete.mockResolvedValue({ data: { success: true, data: null } });

    await act(async () => {
      await result.current.manageToken(1, 'delete', record);
    });

    expect(API.delete).toHaveBeenCalledWith('/api/token/1/');
    expect(record.status).toBe(1);
  });

  it('a rejected management call reports the server message', async () => {
    const { result } = await mount();
    API.delete.mockResolvedValue({
      data: { success: false, message: 'token is in use' },
    });

    await act(async () => {
      await result.current.manageToken(1, 'delete', result.current.tokens[0]);
    });

    expect(showError).toHaveBeenCalledWith('token is in use');
    expect(result.current.loading).toBe(false);
  });

  // DEFECT (see report): manageToken has no try/catch, so a transport-level
  // failure escapes as a rejected promise AFTER setLoading(true) and the
  // table is pinned in the loading state forever. Sibling hooks
  // (useRedemptionsData.manageRedemption) do wrap this in try/catch.
  it('a transport failure still clears the loading flag', async () => {
    const { result } = await mount();
    API.delete.mockRejectedValue(new Error('socket hang up'));

    await act(async () => {
      await result.current
        .manageToken(1, 'delete', result.current.tokens[0])
        .catch(() => {});
    });

    expect(result.current.loading).toBe(false);
  });
});

describe('useTokensData — sorting', () => {
  it('sorts by name and flips direction when re-applied', async () => {
    API.get.mockResolvedValue(
      page([
        tok(1, { name: 'beta' }),
        tok(2, { name: 'alpha' }),
        tok(3, { name: 'gamma' }),
      ]),
    );
    const { result } = await mount();

    act(() => result.current.sortToken('name'));
    expect(result.current.tokens.map((x) => x.name)).toEqual([
      'alpha',
      'beta',
      'gamma',
    ]);

    // Head is unchanged from the caller's perspective only when the sort was
    // already applied, so a second call reverses.
    act(() => result.current.sortToken('name'));
    expect(result.current.tokens.map((x) => x.name)).toEqual([
      'gamma',
      'beta',
      'alpha',
    ]);
  });

  it('is a no-op on an empty table', async () => {
    API.get.mockResolvedValue(page([]));
    const { result } = await mount();

    act(() => result.current.sortToken('name'));
    expect(result.current.tokens).toEqual([]);
    expect(result.current.loading).toBe(false);
  });

  // DEFECT (see report): sortToken coerces both sides to string and uses
  // localeCompare, so quota columns sort lexicographically — 1000 lands
  // before 900 and an operator reading "largest remaining quota" gets the
  // wrong token. Un-skip when numeric columns get a numeric comparator.
  it('sorts quota columns numerically', async () => {
    API.get.mockResolvedValue(
      page([
        tok(1, { remain_quota: 900 }),
        tok(2, { remain_quota: 1000 }),
        tok(3, { remain_quota: 80 }),
      ]),
    );
    const { result } = await mount();

    act(() => result.current.sortToken('remain_quota'));
    expect(result.current.tokens.map((x) => x.remain_quota)).toEqual([
      80, 900, 1000,
    ]);
  });
});

describe('useTokensData — batch operations', () => {
  it('refuses to delete with an empty selection', async () => {
    const { result } = await mount();

    await act(async () => {
      await result.current.batchDeleteTokens();
    });

    expect(API.post).not.toHaveBeenCalled();
    expect(showError).toHaveBeenCalledWith('请先选择要删除的令牌！');
  });

  it('posts the selected ids and reports the deleted count', async () => {
    const { result } = await mount();
    act(() => {
      result.current.setSelectedKeys([tok(1), tok(2)]);
    });

    API.post.mockResolvedValue({ data: { success: true, data: 2 } });
    await act(async () => {
      await result.current.batchDeleteTokens();
    });

    expect(API.post).toHaveBeenCalledWith('/api/token/batch', { ids: [1, 2] });
    expect(showSuccess).toHaveBeenCalledWith('已删除 2 个令牌！');
    expect(result.current.loading).toBe(false);
  });

  it('reports a rejected batch delete and keeps the selection', async () => {
    const { result } = await mount();
    act(() => {
      result.current.setSelectedKeys([tok(1)]);
    });

    API.post.mockResolvedValue({
      data: { success: false, message: 'not permitted' },
    });
    await act(async () => {
      await result.current.batchDeleteTokens();
    });

    expect(showError).toHaveBeenCalledWith('not permitted');
    expect(result.current.selectedKeys).toHaveLength(1);
  });

  it('turns a thrown batch delete into an error toast', async () => {
    const { result } = await mount();
    act(() => {
      result.current.setSelectedKeys([tok(1)]);
    });

    API.post.mockRejectedValue(new Error('gateway timeout'));
    await act(async () => {
      await result.current.batchDeleteTokens();
    });

    expect(showError).toHaveBeenCalledWith('gateway timeout');
    expect(result.current.loading).toBe(false);
  });

  it('refuses to copy with an empty selection', async () => {
    const { result } = await mount();

    act(() => {
      result.current.batchCopyTokens('key');
    });

    expect(Modal.info).not.toHaveBeenCalled();
    expect(showError).toHaveBeenCalledWith('请至少选择一个令牌！');
  });

  it('offers both copy shapes and prefixes every key with sk-', async () => {
    const { result } = await mount();
    act(() => {
      result.current.setSelectedKeys([tok(1), tok(2)]);
    });

    act(() => {
      result.current.batchCopyTokens('key');
    });

    expect(Modal.info).toHaveBeenCalledTimes(1);
    const footer = Modal.info.mock.calls[0][0].footer;
    const [withName, keyOnly] = footer.props.children;

    copy.mockResolvedValue(true);
    await act(async () => {
      await withName.props.onClick();
    });
    expect(copy).toHaveBeenLastCalledWith(
      'token-1    sk-key1\ntoken-2    sk-key2\n',
    );

    await act(async () => {
      await keyOnly.props.onClick();
    });
    expect(copy).toHaveBeenLastCalledWith('sk-key1\nsk-key2\n');
    expect(Modal.destroyAll).toHaveBeenCalledTimes(2);
  });
});

describe('useTokensData — misc surface', () => {
  it('copyText falls back to a modal when the clipboard is unavailable', async () => {
    const { result } = await mount();

    copy.mockResolvedValue(true);
    await act(async () => {
      await result.current.copyText('sk-abc');
    });
    expect(showSuccess).toHaveBeenCalledWith('已复制到剪贴板！');

    copy.mockResolvedValue(false);
    await act(async () => {
      await result.current.copyText('sk-abc');
    });
    expect(Modal.error).toHaveBeenCalledWith(
      expect.objectContaining({ content: 'sk-abc' }),
    );
  });

  it('greys out rows for anything that is not status 1', async () => {
    const { result } = await mount();

    expect(result.current.handleRow({ status: 1 })).toEqual({});
    expect(result.current.handleRow({ status: 2 }).style.background).toBe(
      'var(--semi-color-disabled-border)',
    );
    expect(result.current.handleRow({ status: 3 }).style.background).toBe(
      'var(--semi-color-disabled-border)',
    );
  });

  it('rowSelection.onChange stores the selected row objects, not the keys', async () => {
    const { result } = await mount();

    act(() => {
      result.current.rowSelection.onChange([1, 2], [tok(1), tok(2)]);
    });

    expect(result.current.selectedKeys.map((x) => x.id)).toEqual([1, 2]);
  });

  it('getFormValues normalises missing fields to empty strings', async () => {
    const { result } = await mount();
    expect(result.current.getFormValues()).toEqual({
      searchKeyword: '',
      searchToken: '',
    });

    act(() => {
      result.current.setFormApi({
        getValues: () => ({ searchKeyword: 'abc' }),
      });
    });
    expect(result.current.getFormValues()).toEqual({
      searchKeyword: 'abc',
      searchToken: '',
    });
  });

  it('closeEdit hides the modal immediately and drops the draft later', async () => {
    const { result } = await mount();
    vi.useFakeTimers();

    act(() => {
      result.current.setEditingToken({ id: 42 });
      result.current.setShowEdit(true);
    });
    expect(result.current.showEdit).toBe(true);

    act(() => {
      result.current.closeEdit();
    });
    expect(result.current.showEdit).toBe(false);
    // Kept during the close animation so the form does not blank out.
    expect(result.current.editingToken).toEqual({ id: 42 });

    act(() => {
      vi.advanceTimersByTime(500);
    });
    expect(result.current.editingToken).toEqual({ id: undefined });
  });
});

describe('useTokensData — chat integration links', () => {
  const openWith = async (url, record, openFluent = vi.fn()) => {
    const spy = vi.spyOn(window, 'open').mockImplementation(() => null);
    const { result } = await mount(openFluent);
    await act(async () => {
      await result.current.onOpenLink('chat', url, record);
    });
    const opened = spy.mock.calls[0]?.[0];
    spy.mockRestore();
    return opened;
  };

  it('hands fluent links to the notification callback instead of opening', async () => {
    const openFluent = vi.fn();
    const spy = vi.spyOn(window, 'open').mockImplementation(() => null);
    const { result } = await mount(openFluent);

    await act(async () => {
      await result.current.onOpenLink('chat', 'fluent://import', tok(1));
    });

    expect(openFluent).toHaveBeenCalledWith('key1');
    expect(spy).not.toHaveBeenCalled();
    spy.mockRestore();
  });

  it('substitutes the address and sk-prefixed key into the template', async () => {
    window.localStorage.setItem(
      'status',
      JSON.stringify({ server_address: 'https://hub.example' }),
    );

    const opened = await openWith(
      'https://chat.example/?base={address}&sk={key}',
      tok(1),
    );

    expect(opened).toBe(
      'https://chat.example/?base=https%3A%2F%2Fhub.example&sk=sk-key1',
    );
  });

  it('falls back to the current origin when no server address is stored', async () => {
    const opened = await openWith(
      'https://chat.example/?base={address}',
      tok(1),
    );

    expect(opened).toBe(
      `https://chat.example/?base=${encodeURIComponent(window.location.origin)}`,
    );
  });

  it('packs a base64 config blob for the cherry-style template', async () => {
    window.localStorage.setItem(
      'status',
      JSON.stringify({ server_address: 'https://hub.example' }),
    );

    const opened = await openWith('cherry://import?c={cherryConfig}', tok(1));

    const expected = encodeURIComponent(
      `b64(${JSON.stringify({
        id: 'lurus-api',
        baseUrl: 'https://hub.example',
        apiKey: 'sk-key1',
      })})`,
    );
    expect(opened).toBe(`cherry://import?c=${expected}`);
  });
});
