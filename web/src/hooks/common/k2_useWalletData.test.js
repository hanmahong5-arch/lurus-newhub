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

import useWalletData from './useWalletData';
import { API } from '../../helpers';

beforeEach(() => {
  vi.clearAllMocks();
});

describe('useWalletData', () => {
  it('loads the wallet and asks the interceptor to stay quiet', async () => {
    API.get.mockResolvedValue({
      data: { success: true, data: { balance: 1234, currency: 'CNY' } },
    });

    const { result } = renderHook(() => useWalletData());
    expect(result.current.loading).toBe(true);

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.wallet).toEqual({ balance: 1234, currency: 'CNY' });
    expect(API.get).toHaveBeenCalledWith('/api/wallet/info', {
      skipErrorHandler: true,
    });
  });

  it('keeps the wallet null when the platform answers success=false', async () => {
    API.get.mockResolvedValue({
      data: { success: false, message: 'platform unavailable' },
    });

    const { result } = renderHook(() => useWalletData());
    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(result.current.wallet).toBeNull();
  });

  it('degrades silently when the request throws', async () => {
    API.get.mockRejectedValue(new Error('network down'));

    const { result } = renderHook(() => useWalletData());
    await waitFor(() => expect(result.current.loading).toBe(false));

    // No throw escaped and the caller can render an empty wallet card.
    expect(result.current.wallet).toBeNull();
  });

  it('refresh re-fetches and replaces the cached wallet', async () => {
    API.get.mockResolvedValue({
      data: { success: true, data: { balance: 1 } },
    });
    const { result } = renderHook(() => useWalletData());
    await waitFor(() => expect(result.current.wallet).toEqual({ balance: 1 }));

    API.get.mockResolvedValue({
      data: { success: true, data: { balance: 99 } },
    });
    await act(async () => {
      await result.current.refresh();
    });

    expect(result.current.wallet).toEqual({ balance: 99 });
    expect(API.get).toHaveBeenCalledTimes(2);
  });

  it('a failing refresh leaves the previously loaded wallet in place', async () => {
    API.get.mockResolvedValue({
      data: { success: true, data: { balance: 7 } },
    });
    const { result } = renderHook(() => useWalletData());
    await waitFor(() => expect(result.current.wallet).toEqual({ balance: 7 }));

    API.get.mockRejectedValue(new Error('gone'));
    await act(async () => {
      await result.current.refresh();
    });

    expect(result.current.wallet).toEqual({ balance: 7 });
    expect(result.current.loading).toBe(false);
  });

  it('fetches exactly once per mount', async () => {
    API.get.mockResolvedValue({ data: { success: true, data: {} } });
    const { rerender, result } = renderHook(() => useWalletData());
    await waitFor(() => expect(result.current.loading).toBe(false));

    rerender();
    rerender();

    expect(API.get).toHaveBeenCalledTimes(1);
  });
});
