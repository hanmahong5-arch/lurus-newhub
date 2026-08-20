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

import { useMinimumLoadingTime } from './useMinimumLoadingTime';

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date('2026-01-01T00:00:00Z'));
});

afterEach(() => {
  vi.useRealTimers();
});

describe('useMinimumLoadingTime', () => {
  it('mirrors loading=true immediately', () => {
    const { result } = renderHook(() => useMinimumLoadingTime(true));
    expect(result.current).toBe(true);
  });

  it('holds the skeleton for the remainder of the minimum window', () => {
    const { result, rerender } = renderHook(
      ({ loading }) => useMinimumLoadingTime(loading, 1000),
      { initialProps: { loading: true } },
    );

    // Data came back after 200ms — 800ms of the window is still owed.
    act(() => {
      vi.advanceTimersByTime(200);
    });
    rerender({ loading: false });
    expect(result.current).toBe(true);

    act(() => {
      vi.advanceTimersByTime(799);
    });
    expect(result.current).toBe(true);

    act(() => {
      vi.advanceTimersByTime(1);
    });
    expect(result.current).toBe(false);
  });

  it('drops the skeleton synchronously once the window has elapsed', () => {
    const { result, rerender } = renderHook(
      ({ loading }) => useMinimumLoadingTime(loading, 1000),
      { initialProps: { loading: true } },
    );

    act(() => {
      vi.advanceTimersByTime(1500);
    });
    rerender({ loading: false });

    // No pending timer needed — the debt was already paid.
    expect(result.current).toBe(false);
  });

  it('restarts the window when loading flips back on', () => {
    const { result, rerender } = renderHook(
      ({ loading }) => useMinimumLoadingTime(loading, 1000),
      { initialProps: { loading: true } },
    );

    act(() => {
      vi.advanceTimersByTime(1200);
    });
    rerender({ loading: false });
    expect(result.current).toBe(false);

    // A refresh starts a brand new minimum window.
    rerender({ loading: true });
    expect(result.current).toBe(true);

    act(() => {
      vi.advanceTimersByTime(500);
    });
    rerender({ loading: false });
    expect(result.current).toBe(true);

    act(() => {
      vi.advanceTimersByTime(500);
    });
    expect(result.current).toBe(false);
  });

  it('honours a custom minimum window', () => {
    const { result, rerender } = renderHook(
      ({ loading }) => useMinimumLoadingTime(loading, 3000),
      { initialProps: { loading: true } },
    );

    rerender({ loading: false });
    act(() => {
      vi.advanceTimersByTime(2999);
    });
    expect(result.current).toBe(true);

    act(() => {
      vi.advanceTimersByTime(1);
    });
    expect(result.current).toBe(false);
  });

  it('cancels the pending timer on unmount', () => {
    const clearSpy = vi.spyOn(globalThis, 'clearTimeout');
    const { rerender, unmount } = renderHook(
      ({ loading }) => useMinimumLoadingTime(loading, 1000),
      { initialProps: { loading: true } },
    );

    rerender({ loading: false });
    const before = clearSpy.mock.calls.length;
    unmount();
    expect(clearSpy.mock.calls.length).toBeGreaterThan(before);
    clearSpy.mockRestore();
  });

  it('starts hidden when the caller mounts already loaded', () => {
    const { result } = renderHook(() => useMinimumLoadingTime(false, 1000));
    // Mount timestamp == now, so elapsed 0 and 1000ms is still owed.
    expect(result.current).toBe(false);

    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(result.current).toBe(false);
  });
});
