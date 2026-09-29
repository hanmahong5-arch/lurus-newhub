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
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';

// utils.jsx touches window.matchMedia at module scope; jsdom has no
// implementation, so stub it before any import runs (mirrors
// hooks/common/k2_useTableCompactMode.test.js, which hit the same issue).
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

// The Semi UI barrel (pulled in transitively by helpers/utils.jsx, which
// useTableCompactMode imports for the localStorage round-trip) drags in
// lottie-web, which needs a canvas jsdom does not provide.
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

vi.mock('react-i18next', () => ({
  initReactI18next: { type: '3rdParty', init: () => {} },
  useTranslation: () => ({
    t: (key, fallback) => (typeof fallback === 'string' ? fallback : key),
  }),
}));

import HfDensityToggle from './HfDensityToggle';

beforeEach(() => {
  window.localStorage.clear();
});

describe('HfDensityToggle', () => {
  it('starts unpressed, writes localStorage on click and flips aria-pressed', () => {
    render(<HfDensityToggle tableKey='v2-test-table' />);
    const btn = screen.getByTestId('density-toggle-v2-test-table');
    expect(btn).toHaveAttribute('aria-pressed', 'false');

    fireEvent.click(btn);
    expect(btn).toHaveAttribute('aria-pressed', 'true');
    // useTableCompactMode persists under TABLE_COMPACT_MODES_KEY, keyed by
    // tableKey — assert the write happened without hardcoding the storage
    // key's own name (that belongs to the hook, not this component).
    const stored = Object.keys(window.localStorage).some((k) => {
      try {
        return (
          JSON.parse(window.localStorage.getItem(k))?.['v2-test-table'] === true
        );
      } catch {
        return false;
      }
    });
    expect(stored).toBe(true);
  });

  it('remounting with the same tableKey retains the compact value', () => {
    const { unmount } = render(<HfDensityToggle tableKey='v2-retain' />);
    fireEvent.click(screen.getByTestId('density-toggle-v2-retain'));
    expect(screen.getByTestId('density-toggle-v2-retain')).toHaveAttribute(
      'aria-pressed',
      'true',
    );
    unmount();

    render(<HfDensityToggle tableKey='v2-retain' />);
    expect(screen.getByTestId('density-toggle-v2-retain')).toHaveAttribute(
      'aria-pressed',
      'true',
    );
  });

  it('a different tableKey starts independently unpressed', () => {
    render(<HfDensityToggle tableKey='v2-retain' />);
    fireEvent.click(screen.getByTestId('density-toggle-v2-retain'));

    render(<HfDensityToggle tableKey='v2-other' />);
    expect(screen.getByTestId('density-toggle-v2-other')).toHaveAttribute(
      'aria-pressed',
      'false',
    );
  });

  it('calls onChange with the current compact value', () => {
    const onChange = vi.fn();
    render(<HfDensityToggle tableKey='v2-onchange' onChange={onChange} />);
    expect(onChange).toHaveBeenCalledWith(false);

    onChange.mockClear();
    fireEvent.click(screen.getByTestId('density-toggle-v2-onchange'));
    expect(onChange).toHaveBeenCalledWith(true);
  });
});
