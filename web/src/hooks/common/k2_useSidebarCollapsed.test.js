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
import { describe, it, expect, beforeEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';

import { useSidebarCollapsed } from './useSidebarCollapsed';

const KEY = 'default_collapse_sidebar';

beforeEach(() => {
  window.localStorage.clear();
});

describe('useSidebarCollapsed', () => {
  it('defaults to expanded when nothing is stored', () => {
    const { result } = renderHook(() => useSidebarCollapsed());
    expect(result.current[0]).toBe(false);
  });

  it('only the exact string "true" counts as collapsed', () => {
    window.localStorage.setItem(KEY, 'TRUE');
    expect(renderHook(() => useSidebarCollapsed()).result.current[0]).toBe(
      false,
    );

    window.localStorage.setItem(KEY, '1');
    expect(renderHook(() => useSidebarCollapsed()).result.current[0]).toBe(
      false,
    );

    window.localStorage.setItem(KEY, 'true');
    expect(renderHook(() => useSidebarCollapsed()).result.current[0]).toBe(
      true,
    );
  });

  it('toggle flips the flag and persists the new value as a string', () => {
    const { result } = renderHook(() => useSidebarCollapsed());

    act(() => result.current[1]());
    expect(result.current[0]).toBe(true);
    expect(window.localStorage.getItem(KEY)).toBe('true');

    act(() => result.current[1]());
    expect(result.current[0]).toBe(false);
    expect(window.localStorage.getItem(KEY)).toBe('false');
  });

  it('two toggles inside one batch land on the original value', () => {
    const { result } = renderHook(() => useSidebarCollapsed());

    // Functional updater form means the second toggle sees the first's result
    // rather than the stale render value.
    act(() => {
      result.current[1]();
      result.current[1]();
    });

    expect(result.current[0]).toBe(false);
  });

  it('set() writes the given value without consulting the previous one', () => {
    const { result } = renderHook(() => useSidebarCollapsed());

    act(() => result.current[2](true));
    expect(result.current[0]).toBe(true);
    expect(window.localStorage.getItem(KEY)).toBe('true');

    act(() => result.current[2](true));
    expect(result.current[0]).toBe(true);
    expect(window.localStorage.getItem(KEY)).toBe('true');

    act(() => result.current[2](false));
    expect(result.current[0]).toBe(false);
    expect(window.localStorage.getItem(KEY)).toBe('false');
  });

  it('persists across a remount', () => {
    const first = renderHook(() => useSidebarCollapsed());
    act(() => first.result.current[1]());
    first.unmount();

    const second = renderHook(() => useSidebarCollapsed());
    expect(second.result.current[0]).toBe(true);
  });
});
