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
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { renderHook, act } from '@testing-library/react';

// utils.jsx touches window.matchMedia at module scope; jsdom has no
// implementation, so stub it before any import runs.
vi.hoisted(() => {
  if (typeof window !== 'undefined' && !window.matchMedia) {
    window.matchMedia = (query) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    });
  }
});

// The Semi UI barrel drags in lottie-web, which needs a canvas jsdom does not
// provide. The real helpers module is kept (this hook's whole job is the
// localStorage round-trip through it) — only the UI barrel is stubbed.
vi.mock('@douyinfe/semi-ui', () => {
  const Passthrough = ({ children, ...rest }) =>
    React.createElement('span', rest, children);
  return {
    Toast: {
      success: vi.fn(),
      error: vi.fn(),
      warning: vi.fn(),
      info: vi.fn(),
    },
    Pagination: Passthrough,
    Progress: Passthrough,
    Divider: Passthrough,
    Empty: Passthrough,
    Modal: { error: vi.fn(), info: vi.fn(), confirm: vi.fn() },
    Tag: Passthrough,
    Typography: { Text: Passthrough },
    Avatar: Passthrough,
  };
});

vi.mock('@douyinfe/semi-illustrations', () => ({
  IllustrationNoContent: () => null,
  IllustrationNoContentDark: () => null,
  IllustrationConstruction: () => null,
  IllustrationConstructionDark: () => null,
}));

import { useTableCompactMode } from './useTableCompactMode';
import { TABLE_COMPACT_MODES_KEY } from '../../constants/common.constant';

const readModes = () =>
  JSON.parse(window.localStorage.getItem(TABLE_COMPACT_MODES_KEY) || '{}');

beforeEach(() => {
  window.localStorage.clear();
});

describe('useTableCompactMode', () => {
  it('starts false when nothing was ever persisted', () => {
    const { result } = renderHook(() => useTableCompactMode('tokens'));
    expect(result.current[0]).toBe(false);
  });

  it('hydrates from the persisted per-table flag', () => {
    window.localStorage.setItem(
      TABLE_COMPACT_MODES_KEY,
      JSON.stringify({ tokens: true, logs: false }),
    );

    const tokens = renderHook(() => useTableCompactMode('tokens'));
    const logs = renderHook(() => useTableCompactMode('logs'));

    expect(tokens.result.current[0]).toBe(true);
    expect(logs.result.current[0]).toBe(false);
  });

  it('writes only its own table key, leaving siblings intact', () => {
    window.localStorage.setItem(
      TABLE_COMPACT_MODES_KEY,
      JSON.stringify({ logs: true }),
    );

    const { result } = renderHook(() => useTableCompactMode('tokens'));
    act(() => {
      result.current[1](true);
    });

    expect(result.current[0]).toBe(true);
    expect(readModes()).toEqual({ logs: true, tokens: true });
  });

  it('falls back to false when the stored blob is corrupt', () => {
    window.localStorage.setItem(TABLE_COMPACT_MODES_KEY, 'not-json{{');
    const { result } = renderHook(() => useTableCompactMode('tokens'));
    expect(result.current[0]).toBe(false);
  });

  it('defaults the table key to "global"', () => {
    const { result } = renderHook(() => useTableCompactMode());
    act(() => {
      result.current[1](true);
    });
    expect(readModes()).toEqual({ global: true });
  });

  it('adopts a cross-tab storage event for its own key', () => {
    const { result } = renderHook(() => useTableCompactMode('tokens'));
    expect(result.current[0]).toBe(false);

    act(() => {
      window.dispatchEvent(
        new StorageEvent('storage', {
          key: TABLE_COMPACT_MODES_KEY,
          newValue: JSON.stringify({ tokens: true }),
        }),
      );
    });

    expect(result.current[0]).toBe(true);
  });

  it('ignores storage events for unrelated localStorage keys', () => {
    const { result } = renderHook(() => useTableCompactMode('tokens'));

    act(() => {
      window.dispatchEvent(
        new StorageEvent('storage', {
          key: 'some_other_key',
          newValue: JSON.stringify({ tokens: true }),
        }),
      );
    });

    expect(result.current[0]).toBe(false);
  });

  it('survives a storage event carrying an unparsable payload', () => {
    const { result } = renderHook(() => useTableCompactMode('tokens'));
    act(() => {
      result.current[1](true);
    });

    act(() => {
      window.dispatchEvent(
        new StorageEvent('storage', {
          key: TABLE_COMPACT_MODES_KEY,
          newValue: '{{broken',
        }),
      );
    });

    // Parse failure is swallowed — the last known good value stands.
    expect(result.current[0]).toBe(true);
  });

  it('treats a cleared storage entry (newValue null) as not compact', () => {
    const { result } = renderHook(() => useTableCompactMode('tokens'));
    act(() => {
      result.current[1](true);
    });

    act(() => {
      window.dispatchEvent(
        new StorageEvent('storage', {
          key: TABLE_COMPACT_MODES_KEY,
          newValue: null,
        }),
      );
    });

    expect(result.current[0]).toBe(false);
  });

  it('stops listening after unmount', () => {
    const { result, unmount } = renderHook(() => useTableCompactMode('tokens'));
    unmount();

    // Dispatching after unmount must not throw (listener was removed).
    expect(() =>
      window.dispatchEvent(
        new StorageEvent('storage', {
          key: TABLE_COMPACT_MODES_KEY,
          newValue: JSON.stringify({ tokens: true }),
        }),
      ),
    ).not.toThrow();
    expect(result.current[0]).toBe(false);
  });
});
