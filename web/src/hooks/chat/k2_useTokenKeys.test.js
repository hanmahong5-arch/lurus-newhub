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
import { renderHook, act } from '@testing-library/react';

vi.mock('../../i18n/i18n', () => ({
  default: {
    t: (key, opts) =>
      opts
        ? String(key).replace(/\{\{(\w+)\}\}/g, (whole, name) =>
            name in opts ? opts[name] : whole,
          )
        : key,
  },
}));

vi.mock('../../helpers/token', () => ({
  fetchTokenKeys: vi.fn(),
  getServerAddress: vi.fn(() => 'https://hub.example'),
}));

vi.mock('../../helpers', () => ({
  showError: vi.fn(),
}));

import { useTokenKeys } from './useTokenKeys';
import { fetchTokenKeys, getServerAddress } from '../../helpers/token';
import { showError } from '../../helpers';

// Fake timers rather than waitFor: the hook schedules a real navigation on
// the empty-token path, and firing it would ask jsdom to navigate. Promise
// resolution is unaffected by fake timers, so act() alone drains the effect.
const settle = async () => {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
};

beforeEach(() => {
  vi.clearAllMocks();
  vi.useFakeTimers();
  getServerAddress.mockReturnValue('https://hub.example');
});

afterEach(() => {
  vi.clearAllTimers();
  vi.useRealTimers();
});

describe('useTokenKeys', () => {
  it('publishes the enabled keys and the server address', async () => {
    fetchTokenKeys.mockResolvedValue(['key-a', 'key-b']);

    const { result } = renderHook(() => useTokenKeys());
    expect(result.current.isLoading).toBe(true);
    expect(result.current.keys).toEqual([]);

    await settle();

    expect(result.current.isLoading).toBe(false);
    expect(result.current.keys).toEqual(['key-a', 'key-b']);
    expect(result.current.serverAddress).toBe('https://hub.example');
  });

  it('does not warn or schedule a bounce when a key is available', async () => {
    fetchTokenKeys.mockResolvedValue(['key-a']);

    renderHook(() => useTokenKeys());
    await settle();

    expect(showError).not.toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('warns and defers a bounce to the token page when nothing is enabled', async () => {
    fetchTokenKeys.mockResolvedValue([]);

    const { result } = renderHook(() => useTokenKeys());
    await settle();

    expect(showError).toHaveBeenCalledTimes(1);
    expect(showError.mock.calls[0][0]).toContain('令牌');
    // The redirect is deferred, not immediate, so the toast stays readable.
    expect(vi.getTimerCount()).toBe(1);

    // Loading still resolves and the address is still published.
    expect(result.current.isLoading).toBe(false);
    expect(result.current.keys).toEqual([]);
    expect(result.current.serverAddress).toBe('https://hub.example');
  });

  it('falls back to whatever the address helper reports', async () => {
    fetchTokenKeys.mockResolvedValue(['key-a']);
    getServerAddress.mockReturnValue('http://localhost:3000');

    const { result } = renderHook(() => useTokenKeys());
    await settle();

    expect(result.current.serverAddress).toBe('http://localhost:3000');
  });

  it('fetches once per mount', async () => {
    fetchTokenKeys.mockResolvedValue(['key-a']);

    const { rerender } = renderHook(() => useTokenKeys());
    await settle();

    rerender();
    rerender();

    expect(fetchTokenKeys).toHaveBeenCalledTimes(1);
  });
});
