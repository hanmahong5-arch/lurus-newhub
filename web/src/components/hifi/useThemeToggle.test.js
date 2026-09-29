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
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import { useThemeToggle } from './useThemeToggle';

// matchMedia is not implemented in jsdom by default.
const setMatchMedia = (matches) => {
  const listeners = [];
  const mql = {
    matches,
    media: '(prefers-color-scheme: dark)',
    addEventListener: (event, cb) => listeners.push(cb),
    removeEventListener: (event, cb) => {
      const i = listeners.indexOf(cb);
      if (i >= 0) listeners.splice(i, 1);
    },
  };
  window.matchMedia = vi.fn().mockReturnValue(mql);
  return {
    fire: (nextMatches) => {
      mql.matches = nextMatches;
      listeners.forEach((cb) => cb({ matches: nextMatches }));
    },
  };
};

// Plain React.createElement, not JSX — this file is intentionally .js (not
// .jsx), matching the plan's file list, and this repo's Vite config does not
// run the JSX transform on plain .js files.
const Probe = () => {
  const [theme, toggle] = useThemeToggle();
  return React.createElement(
    'div',
    null,
    React.createElement('span', { 'data-testid': 'theme' }, theme),
    React.createElement(
      'button',
      { type: 'button', onClick: toggle, 'data-testid': 'toggle' },
      'toggle',
    ),
  );
};

beforeEach(() => {
  window.localStorage.clear();
  delete document.documentElement.dataset.theme;
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('useThemeToggle', () => {
  // The regression this hook exists to fix: a saved 'dark' choice used to be
  // clobbered by a 'light' default written to localStorage on mount, before
  // the hydrate-from-storage effect ran. If that race is back, this fails.
  it('honours a saved dark preference and never rewrites it to light on mount', () => {
    window.localStorage.setItem('lurus-hf-theme', 'dark');
    // dataset intentionally left empty — the pre-paint script may not have
    // run in this environment (e.g. an old cached index.html).
    setMatchMedia(false);

    render(React.createElement(Probe));

    expect(screen.getByTestId('theme').textContent).toBe('dark');
    expect(document.documentElement.dataset.theme).toBe('dark');
    expect(window.localStorage.getItem('lurus-hf-theme')).toBe('dark');
  });

  it('falls back to matchMedia when nothing is stored, without writing localStorage', () => {
    setMatchMedia(true);

    render(React.createElement(Probe));

    expect(screen.getByTestId('theme').textContent).toBe('dark');
    expect(window.localStorage.getItem('lurus-hf-theme')).toBeNull();
  });

  it('falls back to light when nothing is stored and the system prefers light', () => {
    setMatchMedia(false);

    render(React.createElement(Probe));

    expect(screen.getByTestId('theme').textContent).toBe('light');
    expect(window.localStorage.getItem('lurus-hf-theme')).toBeNull();
  });

  it('toggle writes the explicit choice to localStorage', () => {
    setMatchMedia(false);
    render(React.createElement(Probe));

    expect(screen.getByTestId('theme').textContent).toBe('light');
    fireEvent.click(screen.getByTestId('toggle'));

    expect(screen.getByTestId('theme').textContent).toBe('dark');
    expect(window.localStorage.getItem('lurus-hf-theme')).toBe('dark');
  });

  it('keeps following the OS preference change when the user never made an explicit choice', () => {
    const media = setMatchMedia(false);
    render(React.createElement(Probe));
    expect(screen.getByTestId('theme').textContent).toBe('light');

    act(() => {
      media.fire(true);
    });
    expect(screen.getByTestId('theme').textContent).toBe('dark');
  });

  it('stops following the OS preference once the user has toggled explicitly', () => {
    const media = setMatchMedia(false);
    render(React.createElement(Probe));
    fireEvent.click(screen.getByTestId('toggle')); // explicit -> dark

    act(() => {
      media.fire(false); // system flips back to light — must NOT override the explicit choice
    });
    expect(screen.getByTestId('theme').textContent).toBe('dark');
  });
});
