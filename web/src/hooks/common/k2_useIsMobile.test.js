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

import { useIsMobile, MOBILE_BREAKPOINT } from './useIsMobile';

// Minimal MediaQueryList stand-in: jsdom ships no matchMedia at all.
const createMatchMedia = () => {
  const state = { matches: false, listeners: new Set(), queries: [] };
  const mql = {
    get matches() {
      return state.matches;
    },
    addEventListener: (type, cb) => {
      if (type === 'change') state.listeners.add(cb);
    },
    removeEventListener: (type, cb) => {
      if (type === 'change') state.listeners.delete(cb);
    },
  };
  const fn = vi.fn((query) => {
    state.queries.push(query);
    return mql;
  });
  return { fn, state };
};

let media;
let originalMatchMedia;

beforeEach(() => {
  media = createMatchMedia();
  originalMatchMedia = window.matchMedia;
  window.matchMedia = media.fn;
});

afterEach(() => {
  window.matchMedia = originalMatchMedia;
});

describe('useIsMobile', () => {
  it('queries one pixel below the documented breakpoint', () => {
    renderHook(() => useIsMobile());

    expect(MOBILE_BREAKPOINT).toBe(768);
    expect(media.state.queries[0]).toBe('(max-width: 767px)');
  });

  it('reports the current match state on mount', () => {
    media.state.matches = true;
    const { result } = renderHook(() => useIsMobile());
    expect(result.current).toBe(true);

    media.state.matches = false;
    const desktop = renderHook(() => useIsMobile());
    expect(desktop.result.current).toBe(false);
  });

  it('re-renders when the viewport crosses the breakpoint', () => {
    const { result } = renderHook(() => useIsMobile());
    expect(result.current).toBe(false);

    act(() => {
      media.state.matches = true;
      media.state.listeners.forEach((cb) => cb());
    });
    expect(result.current).toBe(true);

    act(() => {
      media.state.matches = false;
      media.state.listeners.forEach((cb) => cb());
    });
    expect(result.current).toBe(false);
  });

  it('detaches its listener on unmount', () => {
    const { unmount } = renderHook(() => useIsMobile());
    expect(media.state.listeners.size).toBe(1);

    unmount();
    expect(media.state.listeners.size).toBe(0);
  });
});
