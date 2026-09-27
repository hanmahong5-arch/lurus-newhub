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
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';

const apiMock = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock('../../helpers', () => ({ API: apiMock }));

import { useTenantRead } from './useTenantRead';

const ok = (data) => ({ status: 200, data: { success: true, data } });
const httpError = (status, body) => {
  const err = new Error(`http ${status}`);
  err.isAxiosError = true;
  err.response = { status, data: body ?? { success: false } };
  return err;
};

beforeEach(() => {
  apiMock.get.mockReset();
});

describe('useTenantRead', () => {
  it('starts loading, then exposes the parsed body on success', async () => {
    apiMock.get.mockResolvedValue(ok({ items: [{ id: 1 }] }));
    const { result } = renderHook(() =>
      useTenantRead('/api/v2/acme/tokens', { parse: (d) => d.items }),
    );
    expect(result.current.loading).toBe(true);
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.status).toBe('ok');
    expect(result.current.data).toEqual([{ id: 1 }]);
    expect(result.current.error).toBeNull();
    expect(apiMock.get).toHaveBeenCalledWith('/api/v2/acme/tokens', {
      skipErrorHandler: true,
    });
  });

  it('a rejected 500 is status error with data null and loading false', async () => {
    apiMock.get.mockRejectedValue(httpError(500));
    const { result } = renderHook(() => useTenantRead('/api/v2/acme/tokens'));
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.status).toBe('error');
    expect(result.current.data).toBeNull();
    expect(result.current.error).not.toBeNull();
  });

  it('a 200 carrying success:false is status error, not an empty result', async () => {
    apiMock.get.mockResolvedValue({
      status: 200,
      data: { success: false, message: 'boom', data: { items: [] } },
    });
    const { result } = renderHook(() => useTenantRead('/api/v2/acme/tokens'));
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.status).toBe('error');
    expect(result.current.data).toBeNull();
  });

  it('401 and 403 keep the classifyLoad vocabulary', async () => {
    apiMock.get.mockRejectedValueOnce(httpError(401));
    const a = renderHook(() => useTenantRead('/x'));
    await waitFor(() => expect(a.result.current.loading).toBe(false));
    expect(a.result.current.status).toBe('unauthenticated');

    apiMock.get.mockRejectedValueOnce(httpError(403));
    const b = renderHook(() => useTenantRead('/y'));
    await waitFor(() => expect(b.result.current.loading).toBe(false));
    expect(b.result.current.status).toBe('forbidden');
  });

  it('retry() calls API.get again and resolves once the read has settled', async () => {
    apiMock.get.mockRejectedValueOnce(httpError(500));
    const { result } = renderHook(() => useTenantRead('/api/v2/acme/tokens'));
    await waitFor(() => expect(result.current.status).toBe('error'));
    expect(apiMock.get).toHaveBeenCalledTimes(1);

    apiMock.get.mockResolvedValueOnce(ok({ items: [{ id: 7 }] }));
    await act(async () => {
      await result.current.retry();
    });
    expect(apiMock.get).toHaveBeenCalledTimes(2);
    expect(result.current.status).toBe('ok');
    expect(result.current.data).toEqual({ items: [{ id: 7 }] });
    expect(result.current.error).toBeNull();
  });

  it('does not read while disabled or without a path, and reads once enabled', async () => {
    apiMock.get.mockResolvedValue(ok({ items: [] }));
    const { result, rerender } = renderHook(
      ({ path, enabled }) => useTenantRead(path, { enabled }),
      { initialProps: { path: '', enabled: true } },
    );
    expect(result.current.loading).toBe(false);
    expect(result.current.status).toBeNull();
    rerender({ path: '/api/v2/acme/tokens', enabled: false });
    expect(apiMock.get).not.toHaveBeenCalled();
    rerender({ path: '/api/v2/acme/tokens', enabled: true });
    await waitFor(() => expect(result.current.status).toBe('ok'));
    expect(apiMock.get).toHaveBeenCalledTimes(1);
  });

  it('a stale response never overwrites the answer to a newer path', async () => {
    let resolveFirst;
    apiMock.get.mockImplementationOnce(
      () =>
        new Promise((r) => {
          resolveFirst = r;
        }),
    );
    apiMock.get.mockResolvedValueOnce(ok({ items: ['second'] }));
    const { result, rerender } = renderHook(({ path }) => useTenantRead(path), {
      initialProps: { path: '/a' },
    });
    rerender({ path: '/b' });
    await waitFor(() =>
      expect(result.current.data).toEqual({ items: ['second'] }),
    );
    await act(async () => {
      resolveFirst(ok({ items: ['first'] }));
    });
    expect(result.current.data).toEqual({ items: ['second'] });
  });

  it('re-reads when a deps entry changes even though the path did not', async () => {
    apiMock.get.mockResolvedValue(ok({ items: [] }));
    const { rerender } = renderHook(
      ({ tick }) => useTenantRead('/api/v2/acme/tokens', { deps: [tick] }),
      { initialProps: { tick: 0 } },
    );
    await waitFor(() => expect(apiMock.get).toHaveBeenCalledTimes(1));
    rerender({ tick: 1 });
    await waitFor(() => expect(apiMock.get).toHaveBeenCalledTimes(2));
  });
});
