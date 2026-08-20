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
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, renderHook, act } from '@testing-library/react';

import { ThemeProvider, useTheme, useActualTheme, useSetTheme } from './index';

const STORAGE_KEY = 'theme-mode';

// jsdom has no matchMedia. Install a controllable stub so the "system
// prefers dark" branch and the change-listener wiring are both reachable.
let systemPrefersDark = false;
let changeListeners = [];
let removeCalls = 0;

const installMatchMedia = (prefersDark) => {
  systemPrefersDark = prefersDark;
  changeListeners = [];
  removeCalls = 0;
  window.matchMedia = vi.fn((query) => ({
    media: query,
    get matches() {
      return systemPrefersDark;
    },
    addEventListener: (event, cb) => {
      if (event === 'change') changeListeners.push(cb);
    },
    removeEventListener: (event, cb) => {
      if (event === 'change') {
        removeCalls += 1;
        changeListeners = changeListeners.filter((l) => l !== cb);
      }
    },
    addListener: () => {},
    removeListener: () => {},
  }));
};

const emitSystemChange = (matches) => {
  systemPrefersDark = matches;
  act(() => {
    changeListeners.forEach((cb) => cb({ matches }));
  });
};

const wrapper = ({ children }) => <ThemeProvider>{children}</ThemeProvider>;

const renderTheme = () =>
  renderHook(
    () => ({
      theme: useTheme(),
      actual: useActualTheme(),
      setTheme: useSetTheme(),
    }),
    { wrapper },
  );

beforeEach(() => {
  window.localStorage.clear();
  document.body.removeAttribute('theme-mode');
  document.documentElement.classList.remove('dark');
  installMatchMedia(false);
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('ThemeProvider — initial resolution', () => {
  it("defaults to 'auto' and resolves to the light system theme", () => {
    const { result } = renderTheme();
    expect(result.current.theme).toBe('auto');
    expect(result.current.actual).toBe('light');
  });

  it("'auto' resolves to dark when the system prefers dark", () => {
    installMatchMedia(true);
    const { result } = renderTheme();
    expect(result.current.theme).toBe('auto');
    expect(result.current.actual).toBe('dark');
  });

  it('an explicit persisted choice overrides the system preference', () => {
    installMatchMedia(true);
    window.localStorage.setItem(STORAGE_KEY, 'light');
    const { result } = renderTheme();
    expect(result.current.theme).toBe('light');
    expect(result.current.actual).toBe('light');
  });

  it("restores a persisted 'dark' choice on a light system", () => {
    window.localStorage.setItem(STORAGE_KEY, 'dark');
    const { result } = renderTheme();
    expect(result.current.theme).toBe('dark');
    expect(result.current.actual).toBe('dark');
  });

  it("falls back to 'auto' when localStorage reads throw", () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('SecurityError: storage disabled');
    });
    const { result } = renderTheme();
    expect(result.current.theme).toBe('auto');
    expect(result.current.actual).toBe('light');
  });

  it("falls back to 'light' when matchMedia is unavailable", () => {
    delete window.matchMedia;
    const { result } = renderTheme();
    expect(result.current.actual).toBe('light');
  });
});

describe('ThemeProvider — DOM side effects', () => {
  it('marks body and documentElement when the resolved theme is dark', () => {
    window.localStorage.setItem(STORAGE_KEY, 'dark');
    renderTheme();
    expect(document.body.getAttribute('theme-mode')).toBe('dark');
    expect(document.documentElement.classList.contains('dark')).toBe(true);
  });

  it('leaves body and documentElement unmarked for the light theme', () => {
    document.body.setAttribute('theme-mode', 'dark');
    document.documentElement.classList.add('dark');
    window.localStorage.setItem(STORAGE_KEY, 'light');
    renderTheme();
    expect(document.body.hasAttribute('theme-mode')).toBe(false);
    expect(document.documentElement.classList.contains('dark')).toBe(false);
  });

  it('flips the DOM markers when the theme changes at runtime', () => {
    const { result } = renderTheme();
    expect(document.documentElement.classList.contains('dark')).toBe(false);

    act(() => result.current.setTheme('dark'));
    expect(document.body.getAttribute('theme-mode')).toBe('dark');
    expect(document.documentElement.classList.contains('dark')).toBe(true);

    act(() => result.current.setTheme('light'));
    expect(document.body.hasAttribute('theme-mode')).toBe(false);
    expect(document.documentElement.classList.contains('dark')).toBe(false);
  });
});

describe('setTheme argument handling', () => {
  it('accepts the three string modes and persists them', () => {
    const { result } = renderTheme();
    for (const mode of ['dark', 'light', 'auto']) {
      act(() => result.current.setTheme(mode));
      expect(result.current.theme).toBe(mode);
      expect(window.localStorage.getItem(STORAGE_KEY)).toBe(mode);
    }
  });

  it('keeps the legacy boolean API: true => dark, false => light', () => {
    const { result } = renderTheme();
    act(() => result.current.setTheme(true));
    expect(result.current.theme).toBe('dark');
    expect(window.localStorage.getItem(STORAGE_KEY)).toBe('dark');

    act(() => result.current.setTheme(false));
    expect(result.current.theme).toBe('light');
    expect(window.localStorage.getItem(STORAGE_KEY)).toBe('light');
  });

  it.each([[undefined], [null], [0], [42], [{}]])(
    "coerces a non-string, non-boolean argument (%s) to 'auto'",
    (arg) => {
      const { result } = renderTheme();
      act(() => result.current.setTheme('dark'));
      act(() => result.current.setTheme(arg));
      expect(result.current.theme).toBe('auto');
      expect(window.localStorage.getItem(STORAGE_KEY)).toBe('auto');
    },
  );

  // DEFECT (see report): any string is accepted and persisted verbatim.
  // A typo'd or future value round-trips through storage and then silently
  // resolves to the LIGHT branch, because the DOM effect only tests for the
  // exact literal 'dark'. Pinned here so the blast radius is visible.
  it('persists an unrecognised string and silently resolves it as light', () => {
    const { result } = renderTheme();
    act(() => result.current.setTheme('Dark'));
    expect(result.current.theme).toBe('Dark');
    expect(result.current.actual).toBe('Dark');
    expect(window.localStorage.getItem(STORAGE_KEY)).toBe('Dark');
    expect(document.documentElement.classList.contains('dark')).toBe(false);
  });

  // DEFECT (see report): the READ path is wrapped in try/catch but the
  // WRITE path is not. Where localStorage throws on write (Safari private
  // mode, quota exhausted, storage disabled by policy) the theme toggle
  // throws out of the click handler even though the in-memory state was
  // already updated. Unskip once setTheme guards its setItem the same way
  // the initialiser guards its getItem.
  it.skip('does not throw when localStorage writes are rejected', () => {
    const { result } = renderTheme();
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('QuotaExceededError');
    });
    expect(() => act(() => result.current.setTheme('dark'))).not.toThrow();
    expect(result.current.theme).toBe('dark');
  });

  it('currently propagates the localStorage write failure to the caller', () => {
    const { result } = renderTheme();
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('QuotaExceededError');
    });
    expect(() => act(() => result.current.setTheme('dark'))).toThrow(
      /QuotaExceededError/,
    );
  });

  it('keeps a stable setTheme identity across re-renders', () => {
    const { result, rerender } = renderTheme();
    const first = result.current.setTheme;
    rerender();
    expect(result.current.setTheme).toBe(first);
  });
});

describe('system theme change subscription', () => {
  it("follows live system changes while the mode is 'auto'", () => {
    const { result } = renderTheme();
    expect(result.current.actual).toBe('light');

    emitSystemChange(true);
    expect(result.current.actual).toBe('dark');
    expect(document.documentElement.classList.contains('dark')).toBe(true);

    emitSystemChange(false);
    expect(result.current.actual).toBe('light');
    expect(document.documentElement.classList.contains('dark')).toBe(false);
  });

  it('ignores system changes once an explicit mode is chosen', () => {
    const { result } = renderTheme();
    act(() => result.current.setTheme('light'));

    emitSystemChange(true);
    expect(result.current.actual).toBe('light');
    expect(document.documentElement.classList.contains('dark')).toBe(false);
  });

  it('unsubscribes the media listener on unmount', () => {
    const { unmount } = renderTheme();
    expect(changeListeners).toHaveLength(1);
    unmount();
    expect(removeCalls).toBe(1);
    expect(changeListeners).toHaveLength(0);
  });
});

describe('hooks outside ThemeProvider', () => {
  // Documented contract: all three theme contexts are seeded with null, so
  // standalone consumers must null-check rather than assume a theme.
  it('return null so callers can detect the missing provider', () => {
    const seen = {};
    const Capture = () => {
      seen.theme = useTheme();
      seen.actual = useActualTheme();
      seen.setTheme = useSetTheme();
      return null;
    };
    render(<Capture />);
    expect(seen.theme).toBeNull();
    expect(seen.actual).toBeNull();
    expect(seen.setTheme).toBeNull();
  });
});
