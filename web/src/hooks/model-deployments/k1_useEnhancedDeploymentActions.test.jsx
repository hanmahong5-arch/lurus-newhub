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
}));

import { API, showError, showSuccess } from '../../helpers';
import { useEnhancedDeploymentActions } from './useEnhancedDeploymentActions';

// The hook takes `t` as an argument; identity keeps assertions on literals.
const t = (key, vars) => {
  if (!vars) return key;
  return Object.entries(vars).reduce(
    (acc, [k, v]) => acc.split(`{{${k}}}`).join(String(v)),
    key,
  );
};

const mount = () => renderHook(() => useEnhancedDeploymentActions(t));

beforeEach(() => {
  vi.clearAllMocks();
});

describe('useEnhancedDeploymentActions – per-operation loading keys', () => {
  it('scopes the loading flag to operation + deployment id', async () => {
    let release;
    API.post.mockReturnValue(
      new Promise((resolve) => {
        release = resolve;
      }),
    );

    const { result } = mount();
    expect(result.current.isOperationLoading('extend', 7)).toBe(false);

    let pending;
    act(() => {
      pending = result.current.extendDeployment(7, 3);
    });

    await waitFor(() =>
      expect(result.current.isOperationLoading('extend', 7)).toBe(true),
    );
    // A different deployment and a different operation stay idle.
    expect(result.current.isOperationLoading('extend', 8)).toBe(false);
    expect(result.current.isOperationLoading('delete', 7)).toBe(false);
    expect(result.current.loading).toEqual({ extend_7: true });

    await act(async () => {
      release({ data: { success: true, data: { id: 7 } } });
      await pending;
    });
    expect(result.current.isOperationLoading('extend', 7)).toBe(false);
  });
});

describe('useEnhancedDeploymentActions – single operations', () => {
  it('extendDeployment posts the duration and returns the payload', async () => {
    API.post.mockResolvedValue({
      data: { success: true, data: { expires_at: 1700000000 } },
    });

    const { result } = mount();
    let out;
    await act(async () => {
      out = await result.current.extendDeployment(42, 6);
    });

    expect(API.post).toHaveBeenCalledWith('/api/deployments/42/extend', {
      duration_hours: 6,
    });
    expect(out).toEqual({ expires_at: 1700000000 });
    expect(showSuccess).toHaveBeenCalledWith('容器时长延长成功');
  });

  it('surfaces the server message and rethrows when extend fails', async () => {
    API.post.mockRejectedValue({
      message: 'Request failed',
      response: { data: { message: 'no capacity left' } },
    });

    const { result } = mount();
    await act(async () => {
      await expect(result.current.extendDeployment(42, 6)).rejects.toBeTruthy();
    });

    expect(showError).toHaveBeenCalledWith('延长时长失败: no capacity left');
    // The loading flag must still be released on the error path.
    expect(result.current.isOperationLoading('extend', 42)).toBe(false);
  });

  it('falls back to error.message when the response carries no body', async () => {
    API.get.mockRejectedValue(new Error('socket hang up'));

    const { result } = mount();
    await act(async () => {
      await expect(result.current.getDeploymentDetails(9)).rejects.toBeTruthy();
    });

    expect(showError).toHaveBeenCalledWith('获取详情失败: socket hang up');
  });

  it('getDeploymentLogs only appends the options that were supplied', async () => {
    API.get.mockResolvedValue({ data: { success: true, data: { logs: [] } } });

    const { result } = mount();
    await act(async () => {
      await result.current.getDeploymentLogs(5, {
        level: 'error',
        limit: 100,
        follow: true,
      });
    });

    expect(API.get).toHaveBeenCalledWith(
      '/api/deployments/5/logs?level=error&limit=100&follow=true',
    );
  });

  it('getDeploymentLogs sends an empty query string with no options', async () => {
    API.get.mockResolvedValue({ data: { success: true, data: { logs: [] } } });

    const { result } = mount();
    await act(async () => {
      await result.current.getDeploymentLogs(5);
    });

    expect(API.get).toHaveBeenCalledWith('/api/deployments/5/logs?');
  });

  it('updateDeploymentConfig PUTs the config and toasts on success', async () => {
    API.put.mockResolvedValue({ data: { success: true, data: { ok: 1 } } });

    const { result } = mount();
    let out;
    await act(async () => {
      out = await result.current.updateDeploymentConfig(3, { replicas: 2 });
    });

    expect(API.put).toHaveBeenCalledWith('/api/deployments/3', { replicas: 2 });
    expect(out).toEqual({ ok: 1 });
    expect(showSuccess).toHaveBeenCalledWith('容器配置更新成功');
  });

  it('updateDeploymentName PUTs to the /name sub-resource', async () => {
    API.put.mockResolvedValue({ data: { success: true, data: {} } });

    const { result } = mount();
    await act(async () => {
      await result.current.updateDeploymentName(11, 'prod-a');
    });

    expect(API.put).toHaveBeenCalledWith('/api/deployments/11/name', {
      name: 'prod-a',
    });
    expect(showSuccess).toHaveBeenCalledWith('容器名称更新成功');
  });

  it('does not claim success when the server refuses the delete', async () => {
    API.delete.mockResolvedValue({
      data: { success: false, message: 'container is busy' },
    });

    const { result } = mount();
    await act(async () => {
      // Deliberately not asserting on the resolved value. The current code
      // resolves undefined on a refused delete, which is the defect the skipped
      // test below locks the fix for — pinning `toBeUndefined()` here would make
      // that fix show up as a regression and quietly block it. What this test
      // owns is the invariant that survives either shape: a refusal must never
      // be reported to the user as a success.
      await result.current.deleteDeployment(4).catch(() => {});
    });

    expect(showSuccess).not.toHaveBeenCalled();
  });
});

describe('useEnhancedDeploymentActions – batchDelete', () => {
  it('counts fulfilled vs rejected deletions', async () => {
    API.delete.mockImplementation((url) =>
      url.endsWith('/2')
        ? Promise.reject(new Error('gone'))
        : Promise.resolve({ data: { success: true, data: {} } }),
    );

    const { result } = mount();
    let out;
    await act(async () => {
      out = await result.current.batchDelete([1, 2, 3]);
    });

    expect(out).toEqual({ successful: 2, failed: 1 });
    expect(showSuccess).toHaveBeenCalledWith('批量操作完成: 2个成功, 1个失败');
    expect(result.current.isOperationLoading('batch_delete', 'all')).toBe(
      false,
    );
  });

  it('stays silent and reports zeros for an empty selection', async () => {
    const { result } = mount();
    let out;
    await act(async () => {
      out = await result.current.batchDelete([]);
    });

    expect(out).toEqual({ successful: 0, failed: 0 });
    expect(API.delete).not.toHaveBeenCalled();
    expect(showSuccess).not.toHaveBeenCalled();
  });

  // DEFECT: deleteDeployment only returns data on `success === true`; a
  // payload of `{ success: false }` falls off the end of the try block and
  // RESOLVES with undefined. Promise.allSettled therefore records it as
  // 'fulfilled', so batchDelete reports "N个成功, 0个失败" for deletions the
  // server refused, and the single-delete path shows no error toast at all.
  // Skipped: it encodes the correct contract, which the code does not meet.
  it.skip('counts a server-refused deletion as failed', async () => {
    API.delete.mockResolvedValue({
      data: { success: false, message: 'container is busy' },
    });

    const { result } = mount();
    let out;
    await act(async () => {
      out = await result.current.batchDelete([1, 2]);
    });

    expect(out).toEqual({ successful: 0, failed: 2 });
  });
});

describe('useEnhancedDeploymentActions – exportLogs', () => {
  let clicked;
  let createdBlob;

  beforeEach(() => {
    clicked = null;
    createdBlob = null;
    global.URL.createObjectURL = vi.fn((blob) => {
      createdBlob = blob;
      return 'blob:stub';
    });
    global.URL.revokeObjectURL = vi.fn();
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(
      function stubClick() {
        clicked = { href: this.href, download: this.download };
      },
    );
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('serialises logs to a text blob and triggers a download', async () => {
    API.get.mockResolvedValue({
      data: {
        success: true,
        data: {
          logs: [
            {
              timestamp: '2026-01-02T03:04:05.000Z',
              level: 'INFO',
              source: 'engine',
              message: 'started',
            },
            {
              timestamp: '2026-01-02T03:04:06.000Z',
              level: 'ERROR',
              message: 'crashed',
            },
          ],
        },
      },
    });

    const { result } = mount();
    await act(async () => {
      await result.current.exportLogs(77);
    });

    // Export raises the limit so the file is not truncated to the page size.
    expect(API.get).toHaveBeenCalledWith(
      '/api/deployments/77/logs?limit=10000',
    );

    const text = await createdBlob.text();
    expect(text.split('\n')).toEqual([
      '[2026-01-02T03:04:05.000Z] [INFO] [engine] started',
      // No source => no bracketed source segment.
      '[2026-01-02T03:04:06.000Z] [ERROR] crashed',
    ]);
    expect(createdBlob.type).toBe('text/plain');
    expect(clicked.download).toMatch(
      /^deployment-77-logs-\d{4}-\d{2}-\d{2}\.txt$/,
    );
    expect(global.URL.revokeObjectURL).toHaveBeenCalledWith('blob:stub');
    expect(showSuccess).toHaveBeenCalledWith('日志导出成功');
    // The anchor is removed again — no stray nodes left in the document.
    expect(document.querySelectorAll('a')).toHaveLength(0);
  });

  it('does nothing when the log payload has no logs array', async () => {
    API.get.mockResolvedValue({ data: { success: true, data: {} } });

    const { result } = mount();
    await act(async () => {
      await result.current.exportLogs(77);
    });

    expect(global.URL.createObjectURL).not.toHaveBeenCalled();
    expect(showSuccess).not.toHaveBeenCalled();
  });

  it('reports and rethrows when the underlying log fetch fails', async () => {
    API.get.mockRejectedValue(new Error('502 bad gateway'));

    const { result } = mount();
    await act(async () => {
      await expect(result.current.exportLogs(77)).rejects.toBeTruthy();
    });

    // Both the inner fetch and the export wrapper report; the export must not
    // leave its own loading flag pinned.
    expect(showError).toHaveBeenCalledWith('导出日志失败: 502 bad gateway');
    expect(result.current.isOperationLoading('export_logs', 77)).toBe(false);
    expect(result.current.isOperationLoading('logs', 77)).toBe(false);
  });
});
