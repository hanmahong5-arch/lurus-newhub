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
  API: { get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() },
  showError: vi.fn(),
  showSuccess: vi.fn(),
  getTableCompactMode: () => false,
  setTableCompactMode: vi.fn(),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key) => key }),
}));

import { API, showError, showSuccess } from '../../helpers';
import { useDeploymentsData } from './useDeploymentsData';

const listOk = (items, extra = {}) => ({
  data: { success: true, data: { items, total: items.length, ...extra } },
});

const mountLoaded = async (payload = listOk([])) => {
  API.get.mockResolvedValue(payload);
  const hook = renderHook(() => useDeploymentsData());
  await waitFor(() => expect(hook.result.current.loading).toBe(false));
  return hook;
};

beforeEach(() => {
  vi.clearAllMocks();
  window.localStorage.clear();
  vi.spyOn(console, 'error').mockImplementation(() => {});
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('useDeploymentsData – initial load and shaping', () => {
  it('requests page 1 with the default page size and stamps a table key', async () => {
    const { result } = await mountLoaded(
      listOk([{ id: 'a' }, { id: 'b' }], { total: 42, page: 1 }),
    );

    expect(API.get).toHaveBeenCalledWith('/api/deployments/?p=1&page_size=10');
    expect(result.current.deployments).toEqual([
      { id: 'a', key: 'a' },
      { id: 'b', key: 'b' },
    ]);
    expect(result.current.deploymentCount).toBe(42);
    expect(result.current.activePage).toBe(1);
    expect(result.current.searching).toBe(false);
  });

  it('falls back to the item count when the payload omits total', async () => {
    const { result } = await mountLoaded({
      data: { success: true, data: { items: [{ id: 1 }, { id: 2 }] } },
    });

    expect(result.current.deploymentCount).toBe(2);
  });

  it('accepts a bare array payload and an empty result set', async () => {
    const { result } = await mountLoaded({
      data: { success: true, data: [{ id: 7 }] },
    });
    expect(result.current.deployments).toEqual([{ id: 7, key: 7 }]);

    const empty = await mountLoaded({
      data: { success: true, data: { items: [], total: 0 } },
    });
    expect(empty.result.current.deployments).toEqual([]);
    expect(empty.result.current.deploymentCount).toBe(0);
  });

  it('adopts the page the server actually served, not the one requested', async () => {
    // Asking past the last page: the backend clamps and echoes page 1.
    const { result } = await mountLoaded(listOk([]));
    API.get.mockResolvedValue({
      data: { success: true, data: { items: [{ id: 1 }], total: 1, page: 1 } },
    });

    await act(async () => {
      result.current.handlePageChange(99);
    });

    await waitFor(() => expect(result.current.activePage).toBe(1));
  });
});

describe('useDeploymentsData – failure handling', () => {
  it('empties the table and reports the message on success=false', async () => {
    const { result } = await mountLoaded({
      data: { success: false, message: 'forbidden' },
    });

    expect(showError).toHaveBeenCalledWith('forbidden');
    expect(result.current.deployments).toEqual([]);
    expect(result.current.deploymentCount).toBe(0);
    expect(result.current.loading).toBe(false);
  });

  it('releases the loading flag when the request rejects', async () => {
    API.get.mockRejectedValue(new Error('offline'));
    const { result } = renderHook(() => useDeploymentsData());

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(showError).toHaveBeenCalledWith('获取部署列表失败');
    expect(result.current.deployments).toEqual([]);
  });

  it('uses the search-specific message when a search rejects', async () => {
    const { result } = await mountLoaded(listOk([{ id: 1 }]));

    API.get.mockRejectedValue(new Error('offline'));
    await act(async () => {
      await result.current.searchDeployments({ searchKeyword: 'gpu' });
    });

    expect(showError).toHaveBeenCalledWith('搜索失败');
    expect(result.current.searching).toBe(false);
    expect(result.current.deployments).toEqual([]);
  });

  it('ignores a stale response that lands after a newer one', async () => {
    const { result } = await mountLoaded(listOk([]));

    let resolveSlow;
    let resolveFast;
    API.get
      .mockReturnValueOnce(
        new Promise((r) => {
          resolveSlow = r;
        }),
      )
      .mockReturnValueOnce(
        new Promise((r) => {
          resolveFast = r;
        }),
      );

    let slow;
    let fast;
    act(() => {
      slow = result.current.refresh(2);
      fast = result.current.refresh(3);
    });

    await act(async () => {
      resolveFast(listOk([{ id: 'newest' }], { page: 3 }));
      await fast;
      // The superseded request answers last and must be discarded.
      resolveSlow(listOk([{ id: 'stale' }], { page: 2 }));
      await slow;
    });

    expect(result.current.deployments).toEqual([
      { id: 'newest', key: 'newest' },
    ]);
    expect(result.current.activePage).toBe(3);
  });
});

describe('useDeploymentsData – search and pagination', () => {
  it('builds the search URL from the trimmed keyword and status', async () => {
    const { result } = await mountLoaded(listOk([]));

    await act(async () => {
      await result.current.searchDeployments({
        searchKeyword: '  gpu-node  ',
        searchStatus: ' running ',
      });
    });

    expect(API.get).toHaveBeenLastCalledWith(
      '/api/deployments/search?p=1&page_size=10&keyword=gpu-node&status=running',
    );
    expect(result.current.activePage).toBe(1);
  });

  it('drops back to the plain list endpoint when the query is cleared', async () => {
    const { result } = await mountLoaded(listOk([]));

    await act(async () => {
      await result.current.searchDeployments({ searchKeyword: 'x' });
    });
    await act(async () => {
      await result.current.searchDeployments({});
    });

    expect(API.get).toHaveBeenLastCalledWith(
      '/api/deployments/?p=1&page_size=10',
    );
  });

  it('keeps the active query while paging', async () => {
    const { result } = await mountLoaded(listOk([]));

    await act(async () => {
      await result.current.searchDeployments({ searchStatus: 'stopped' });
    });
    await act(async () => {
      result.current.handlePageChange(4);
    });

    expect(API.get).toHaveBeenLastCalledWith(
      '/api/deployments/search?p=4&page_size=10&status=stopped',
    );
    expect(result.current.activePage).toBe(4);
  });

  it('resets to the first page when the page size changes', async () => {
    const { result } = await mountLoaded(listOk([]));

    await act(async () => {
      result.current.handlePageChange(3);
    });
    await act(async () => {
      result.current.handlePageSizeChange(50);
    });

    expect(API.get).toHaveBeenLastCalledWith(
      '/api/deployments/?p=1&page_size=50',
    );
    await waitFor(() => expect(result.current.pageSize).toBe(50));
    expect(result.current.activePage).toBe(1);
  });
});

describe('useDeploymentsData – column visibility', () => {
  it('always forces the container name and action columns on', async () => {
    const { result } = await mountLoaded();

    act(() => {
      result.current.setVisibleColumns({
        container_name: false,
        actions: false,
        status: false,
      });
    });

    expect(result.current.visibleColumns.container_name).toBe(true);
    expect(result.current.visibleColumns.actions).toBe(true);
    expect(result.current.visibleColumns.status).toBe(false);
    expect(
      JSON.parse(window.localStorage.getItem('deployments_visible_columns')),
    ).toMatchObject({ container_name: true, actions: true, status: false });
  });

  it('restores saved columns and back-fills provider when it is absent', async () => {
    window.localStorage.setItem(
      'deployments_visible_columns',
      JSON.stringify({ status: false }),
    );

    const { result } = await mountLoaded();
    expect(result.current.visibleColumns.status).toBe(false);
    expect(result.current.visibleColumns.provider).toBe(true);
  });

  it('falls back to defaults when the saved value is not valid JSON', async () => {
    window.localStorage.setItem('deployments_visible_columns', '{oops');

    const { result } = await mountLoaded();
    expect(result.current.visibleColumns.status).toBe(true);
    expect(result.current.visibleColumns.deployment_name).toBe(false);
  });
});

describe('useDeploymentsData – selection and modal', () => {
  // Row selection / batch delete were removed: batchDeleteDeployments posted
  // to POST /api/deployments/batch_delete, a route nothing in the tree
  // registers, and the console page had already hard-disabled the feature
  // before this, so it never reached an operator. The hook no longer exposes
  // selectedKeys / rowSelection at all.
  it('exposes no row-selection state', async () => {
    const { result } = await mountLoaded(listOk([{ id: 1 }, { id: 2 }]));
    expect(result.current.selectedKeys).toBeUndefined();
    expect(result.current.rowSelection).toBeUndefined();
    expect(result.current.batchDeleteDeployments).toBeUndefined();
  });

  it('closeEdit hides the modal immediately and clears the record later', async () => {
    vi.useFakeTimers();
    try {
      API.get.mockResolvedValue(listOk([]));
      const { result } = renderHook(() => useDeploymentsData());
      await act(async () => {
        await vi.runAllTimersAsync();
      });

      act(() => {
        result.current.setEditingDeployment({ id: 5 });
        result.current.setShowEdit(true);
      });
      act(() => {
        result.current.closeEdit();
      });

      expect(result.current.showEdit).toBe(false);
      // Still holding the record so the closing animation keeps its content.
      expect(result.current.editingDeployment).toEqual({ id: 5 });

      await act(async () => {
        await vi.advanceTimersByTimeAsync(500);
      });
      expect(result.current.editingDeployment).toEqual({ id: undefined });
    } finally {
      vi.useRealTimers();
    }
  });

  it('getFormValues falls back to the initial values without a form api', async () => {
    const { result } = await mountLoaded();
    expect(result.current.getFormValues()).toEqual({
      searchKeyword: '',
      searchStatus: '',
    });

    act(() => {
      result.current.setFormApi({ getValues: () => ({ searchKeyword: 'k' }) });
    });
    expect(result.current.getFormValues()).toEqual({ searchKeyword: 'k' });
  });
});

describe('useDeploymentsData – lifecycle operations', () => {
  // startDeployment / restartDeployment (POST .../start, .../restart) and
  // batchDeleteDeployments (POST .../batch_delete) are gone: nothing in the
  // route tree ever answered any of the three, and the console buttons that
  // called them were removed along with them.
  it('does not expose startDeployment, restartDeployment or batchDeleteDeployments', async () => {
    const { result } = await mountLoaded(listOk([{ id: 3 }]));
    expect(result.current.startDeployment).toBeUndefined();
    expect(result.current.restartDeployment).toBeUndefined();
    expect(result.current.batchDeleteDeployments).toBeUndefined();
  });

  it('deletes and refreshes the list', async () => {
    const { result } = await mountLoaded(listOk([{ id: 3 }]));
    API.delete.mockResolvedValue({ data: { success: true } });

    await act(async () => {
      await result.current.deleteDeployment(3);
    });
    expect(API.delete).toHaveBeenCalledWith('/api/deployments/3');
    // 1 initial + one refresh for the delete.
    expect(API.get).toHaveBeenCalledTimes(2);
    expect(showSuccess).toHaveBeenCalledWith('部署删除成功');
  });

  it('updateDeploymentName reports success as a boolean', async () => {
    const { result } = await mountLoaded(listOk([]));

    API.put.mockResolvedValue({ data: { success: true } });
    let ok;
    await act(async () => {
      ok = await result.current.updateDeploymentName(8, 'renamed');
    });
    expect(API.put).toHaveBeenCalledWith('/api/deployments/8/name', {
      name: 'renamed',
    });
    expect(ok).toBe(true);

    API.put.mockResolvedValue({ data: { success: false, message: 'taken' } });
    let bad;
    await act(async () => {
      bad = await result.current.updateDeploymentName(8, 'renamed');
    });
    expect(bad).toBe(false);
    expect(showError).toHaveBeenCalledWith('taken');

    API.put.mockRejectedValue(new Error('boom'));
    let thrown;
    await act(async () => {
      thrown = await result.current.updateDeploymentName(8, 'renamed');
    });
    expect(thrown).toBe(false);
  });
});

describe('useDeploymentsData – syncDeploymentToChannel', () => {
  it('rejects a deployment without an id before touching the network', async () => {
    const { result } = await mountLoaded(listOk([]));
    API.get.mockClear();

    await act(async () => {
      await result.current.syncDeploymentToChannel({});
    });

    expect(showError).toHaveBeenCalledWith('同步渠道失败：缺少部署信息');
    expect(API.get).not.toHaveBeenCalled();
  });

  it('refuses to build a channel when no container exposes a public url', async () => {
    const { result } = await mountLoaded(listOk([]));
    API.get.mockResolvedValue({
      data: { success: true, data: { containers: [{ container_id: 'c1' }] } },
    });

    await act(async () => {
      await result.current.syncDeploymentToChannel({ id: 'dep-1' });
    });

    expect(showError).toHaveBeenCalledWith('未找到可用的容器访问地址');
    expect(API.post).not.toHaveBeenCalled();
  });

  it('builds the channel payload from the first container that has a url', async () => {
    const { result } = await mountLoaded(listOk([]));
    API.get.mockResolvedValue({
      data: {
        success: true,
        data: {
          containers: [
            { container_id: 'no-url' },
            {
              container_id: 'c2',
              public_url: '  https://node.example.com//  ',
            },
          ],
        },
      },
    });
    API.post.mockResolvedValue({ data: { success: true } });

    await act(async () => {
      await result.current.syncDeploymentToChannel({
        id: 'dep-9',
        container_name: 'gpu-box',
      });
    });

    expect(API.get).toHaveBeenLastCalledWith(
      '/api/deployments/dep-9/containers',
    );
    const [url, payload] = API.post.mock.calls.at(-1);
    expect(url).toBe('/api/channel/');
    expect(payload.mode).toBe('single');
    expect(payload.channel).toMatchObject({
      name: '[IO.NET] gpu-box',
      type: 4,
      // Trailing slashes stripped so the relay does not build '//v1/...'.
      base_url: 'https://node.example.com',
      group: 'default',
      tag: 'ionet',
    });
    expect(payload.channel.key).toMatch(/^ionet-/);
    expect(JSON.parse(payload.channel.other_info)).toEqual({
      source: 'ionet',
      deployment_id: 'dep-9',
      deployment_name: 'gpu-box',
      container_id: 'c2',
      public_url: 'https://node.example.com',
    });
    expect(showSuccess).toHaveBeenCalledWith('已同步到渠道');
  });

  it('surfaces the channel-creation error message', async () => {
    const { result } = await mountLoaded(listOk([]));
    API.get.mockResolvedValue({
      data: {
        success: true,
        data: { containers: [{ public_url: 'https://n' }] },
      },
    });
    API.post.mockResolvedValue({
      data: { success: false, message: 'duplicate channel name' },
    });

    await act(async () => {
      await result.current.syncDeploymentToChannel({ id: 'dep-1' });
    });

    expect(showError).toHaveBeenCalledWith('duplicate channel name');
  });

  it('stops when the container lookup itself fails', async () => {
    const { result } = await mountLoaded(listOk([]));
    API.get.mockResolvedValue({
      data: { success: false, message: 'container api down' },
    });

    await act(async () => {
      await result.current.syncDeploymentToChannel({ id: 'dep-1' });
    });

    expect(showError).toHaveBeenCalledWith('container api down');
    expect(API.post).not.toHaveBeenCalled();
  });
});
