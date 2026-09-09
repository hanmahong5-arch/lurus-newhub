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
}));

import { API } from '../../helpers';
import { useTenantModels } from './useTenantModels';

beforeEach(() => {
  API.get.mockReset();
});

describe('useTenantModels', () => {
  it('parses the real {items, total, vendor_counts} wire shape', async () => {
    API.get.mockResolvedValueOnce({
      data: {
        success: true,
        data: {
          items: [{ model_name: 'deepseek-chat' }],
          total: 1,
          vendor_counts: { DeepSeek: 1 },
        },
      },
    });

    const { result } = renderHook(() => useTenantModels('acme'));

    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(result.current.items).toEqual([{ model_name: 'deepseek-chat' }]);
    expect(result.current.total).toBe(1);
    expect(result.current.vendorCounts).toEqual({ DeepSeek: 1 });
    expect(result.current.error).toBeNull();
    expect(API.get).toHaveBeenCalledWith(
      expect.stringContaining('/api/v2/acme/models?'),
      expect.objectContaining({ skipErrorHandler: false }),
    );
  });

  // If `data` itself were ever treated as the array (the bug this hook
  // replaces), an array payload would silently produce whatever Array.map
  // does to an object-shaped response. Here it must produce an empty list,
  // not throw — the payload has success:true, so this is not an error case
  // and the hook records no error for it.
  it('an array payload for data yields items=[] and no thrown error', async () => {
    API.get.mockResolvedValueOnce({
      data: { success: true, data: [{ model_name: 'stray' }] },
    });

    const { result } = renderHook(() => useTenantModels('acme'));

    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(result.current.items).toEqual([]);
    expect(result.current.error).toBeNull();
  });

  it('success:false yields items=[] and error set without throwing', async () => {
    API.get.mockResolvedValueOnce({
      data: { success: false, message: 'tenant not found' },
    });

    const { result } = renderHook(() => useTenantModels('acme'));

    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(result.current.items).toEqual([]);
    expect(result.current.error).toBeTruthy();
  });

  // A failure whose body carries an empty message must still be truthy:
  // callers distinguish "catalogue is empty" from "the request failed" by
  // truthiness alone, so a falsy error here would put the Playground back to
  // claiming an empty catalogue on a failed fetch.
  it('success:false with an empty message still yields a truthy error', async () => {
    API.get.mockResolvedValueOnce({
      data: { success: false, message: '' },
    });

    const { result } = renderHook(() => useTenantModels('acme'));

    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(result.current.items).toEqual([]);
    expect(result.current.error).toBeTruthy();
  });

  it('a rejected request yields items=[] and error set without throwing', async () => {
    API.get.mockRejectedValueOnce(new Error('network down'));

    const { result } = renderHook(() => useTenantModels('acme'));

    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(result.current.items).toEqual([]);
    expect(result.current.error).toBeTruthy();
  });

  it('enabled:false does not fetch until flipped on', async () => {
    API.get.mockResolvedValue({
      data: { success: true, data: { items: [], total: 0 } },
    });

    const { rerender } = renderHook(
      ({ enabled }) => useTenantModels('acme', { enabled }),
      { initialProps: { enabled: false } },
    );

    expect(API.get).not.toHaveBeenCalled();

    rerender({ enabled: true });

    await waitFor(() => expect(API.get).toHaveBeenCalledTimes(1));
  });

  // skipErrorHandler defaults to false so Models/index.jsx and
  // Playground/index.jsx keep the interceptor's 401 self-heal
  // (helpers/api.js:110-136); only a caller that opts in (CommandPalette)
  // gets it skipped.
  it('does not skip the global error handler unless the caller asks for it', async () => {
    API.get.mockResolvedValueOnce({
      data: { success: true, data: { items: [], total: 0 } },
    });

    renderHook(() => useTenantModels('acme'));

    await waitFor(() => expect(API.get).toHaveBeenCalledTimes(1));
    expect(API.get).toHaveBeenCalledWith(
      expect.any(String),
      expect.objectContaining({ skipErrorHandler: false }),
    );
  });

  it('skips the global error handler when the caller passes skipErrorHandler:true', async () => {
    API.get.mockResolvedValueOnce({
      data: { success: true, data: { items: [], total: 0 } },
    });

    renderHook(() => useTenantModels('acme', { skipErrorHandler: true }));

    await waitFor(() => expect(API.get).toHaveBeenCalledTimes(1));
    expect(API.get).toHaveBeenCalledWith(
      expect.any(String),
      expect.objectContaining({ skipErrorHandler: true }),
    );
  });

  // Lock for the empty-tenantSlug half of the enabled guard.
  it('does not call API.get when tenantSlug is empty', async () => {
    renderHook(() => useTenantModels(''));

    await new Promise((r) => setTimeout(r, 0));
    expect(API.get).not.toHaveBeenCalled();
  });

  // Lock for the cancellation guard: a tenant switch mid-flight must not let
  // the first (now-stale) tenant's response land in state after the second
  // request already resolved.
  it('a slug change mid-flight does not apply the stale response', async () => {
    let resolveFirst;
    const first = new Promise((r) => {
      resolveFirst = r;
    });
    API.get.mockImplementationOnce(() => first);
    API.get.mockResolvedValueOnce({
      data: {
        success: true,
        data: { items: [{ model_name: 'tenant-b-model' }], total: 1 },
      },
    });

    const { result, rerender } = renderHook(
      ({ slug }) => useTenantModels(slug),
      { initialProps: { slug: 'tenant-a' } },
    );

    rerender({ slug: 'tenant-b' });

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.items).toEqual([{ model_name: 'tenant-b-model' }]);

    // The stale tenant-a response resolves after tenant-b already landed —
    // it must not overwrite the current, correct state.
    resolveFirst({
      data: {
        success: true,
        data: { items: [{ model_name: 'tenant-a-model' }], total: 1 },
      },
    });
    await new Promise((r) => setTimeout(r, 0));
    expect(result.current.items).toEqual([{ model_name: 'tenant-b-model' }]);
  });

  it('refetch re-issues exactly one additional request', async () => {
    API.get.mockResolvedValue({
      data: {
        success: true,
        data: { items: [{ model_name: 'deepseek-chat' }], total: 1 },
      },
    });

    const { result } = renderHook(() => useTenantModels('acme'));

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(API.get).toHaveBeenCalledTimes(1);

    act(() => {
      result.current.refetch();
    });

    await waitFor(() => expect(API.get).toHaveBeenCalledTimes(2));
  });
});
