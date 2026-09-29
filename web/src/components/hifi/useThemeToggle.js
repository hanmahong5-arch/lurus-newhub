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
import { useEffect, useRef, useState } from 'react';

// lurus-hf-theme is also the key the zero-flicker inline script in
// index.html reads before React ever mounts — the two must stay in sync.
const STORAGE_KEY = 'lurus-hf-theme';

const readStoredTheme = () => {
  try {
    const saved = window.localStorage.getItem(STORAGE_KEY);
    if (saved === 'light' || saved === 'dark') return saved;
  } catch (e) {
    // ignore — private browsing / disabled storage
  }
  return null;
};

const prefersDark = () => {
  try {
    return (
      typeof window.matchMedia === 'function' &&
      window.matchMedia('(prefers-color-scheme: dark)').matches
    );
  } catch (e) {
    return false;
  }
};

// Order: localStorage (explicit past choice) → the `data-theme` attribute the
// index.html pre-paint script may have already set on <html> (so hydration
// doesn't fight it) → matchMedia → 'light'.
const readInitialTheme = () => {
  if (typeof window === 'undefined') return 'light';
  const stored = readStoredTheme();
  if (stored) return stored;
  if (typeof document !== 'undefined') {
    const pre = document.documentElement.dataset.theme;
    if (pre === 'light' || pre === 'dark') return pre;
  }
  return prefersDark() ? 'dark' : 'light';
};

/**
 * useThemeToggle — the shell's dark/light state.
 *
 * This replaces a two-effect version that had a race: one effect wrote the
 * state's default ('light') into localStorage on every mount, and a second
 * effect then read localStorage back to hydrate — but the write always ran
 * first, so a saved 'dark' choice was clobbered by 'light' before it could
 * ever be read. A dark preference was lost on every page refresh.
 *
 * Fix: decide the theme synchronously in the useState initializer (no
 * "default then correct" step), and only ever persist to localStorage from
 * an explicit user action (`toggle`). Absent that explicit choice, the theme
 * keeps following the OS `prefers-color-scheme` live.
 */
export const useThemeToggle = () => {
  const [theme, setTheme] = useState(readInitialTheme);
  // Whether the user has ever explicitly picked a theme (vs. inheriting the
  // OS preference) — decided once, at mount, from localStorage.
  const explicitRef = useRef(readStoredTheme() !== null);

  // The ONLY effect that touches the DOM. Never writes localStorage — that
  // used to happen here and is exactly the race described above.
  useEffect(() => {
    if (typeof document === 'undefined') return;
    document.documentElement.dataset.theme = theme;
  }, [theme]);

  // No explicit choice yet: keep tracking the system preference live, so a
  // user who never touched the toggle still sees their OS theme change
  // reflected without a reload.
  useEffect(() => {
    if (explicitRef.current) return undefined;
    if (
      typeof window === 'undefined' ||
      typeof window.matchMedia !== 'function'
    ) {
      return undefined;
    }
    let mql;
    try {
      mql = window.matchMedia('(prefers-color-scheme: dark)');
    } catch (e) {
      return undefined;
    }
    const onChange = (e) => {
      if (explicitRef.current) return;
      setTheme(e.matches ? 'dark' : 'light');
    };
    if (typeof mql.addEventListener === 'function') {
      mql.addEventListener('change', onChange);
      return () => mql.removeEventListener('change', onChange);
    }
    // Older Safari never got addEventListener on MediaQueryList.
    if (typeof mql.addListener === 'function') {
      mql.addListener(onChange);
      return () => mql.removeListener(onChange);
    }
    return undefined;
  }, []);

  const toggle = () => {
    setTheme((t) => {
      const next = t === 'dark' ? 'light' : 'dark';
      explicitRef.current = true;
      try {
        window.localStorage.setItem(STORAGE_KEY, next);
      } catch (e) {
        // ignore — private browsing / disabled storage
      }
      return next;
    });
  };

  return [theme, toggle];
};

export default useThemeToggle;
