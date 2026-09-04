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

// β5: render-only smoke tests for static / illustrative v2 pages.
// These pages carry no backend API calls; we assert they mount without
// throwing and render at least one DOM node.
//
// CommandPalette left this file on 2026-09-03: it now fetches models,
// pricing, tokens, logs and channels, so "renders without error" is no longer
// a meaningful assertion about it. It has its own suite with real mocks.

import React from 'react';
import { describe, it, expect, vi } from 'vitest';
import { render } from '@testing-library/react';

// States prints a copy-pasteable curl command and must resolve the relay host
// from the serving server rather than a hardcoded domain (the retired
// api.lurus.cn). Stub the helpers barrel: importing it for real pulls
// helpers/api.js → Semi UI → lottie-web, which paints into a canvas jsdom
// doesn't have, and these are deliberately dependency-light smoke tests.
vi.mock('../../helpers', () => ({
  getServerAddress: () => 'https://hub.example.test',
}));

// Stub HFShell so static pages don't drag in the full shell dependency tree.
vi.mock('../../components/hifi/HFShell', () => ({
  default: ({ children, actions }) =>
    React.createElement(
      'div',
      { 'data-testid': 'hf-shell' },
      actions,
      children,
    ),
}));

// AccountDisabled is hi-fi now (no Semi UI / no illustrations) — nothing to
// stub for it beyond react-i18next below.

// react-i18next — fallback-returning mock (simulates the default en render),
// same pattern as Channel/index.test.jsx.
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key, fallback, opts) => {
      const vars =
        typeof fallback === 'object' && fallback !== null ? fallback : opts;
      let out = typeof fallback === 'string' ? fallback : key;
      if (vars) {
        for (const [k, v] of Object.entries(vars)) {
          out = out.split(`{{${k}}}`).join(String(v));
        }
      }
      return out;
    },
  }),
}));

// Variants uses TweaksPanel from hifi — stub all exported components.
vi.mock('../../components/hifi/TweaksPanel', () => ({
  useTweaks: (defaults) => {
    // eslint-disable-next-line react-hooks/rules-of-hooks
    const [state, setState] = React.useState(defaults);
    return [state, setState];
  },
  TweaksPanel: ({ children }) =>
    React.createElement('div', { 'data-testid': 'tweaks-panel' }, children),
  TweakSection: ({ children }) => React.createElement('div', null, children),
  TweakRadio: ({ children }) => React.createElement('div', null, children),
  TweakColor: ({ children }) => React.createElement('div', null, children),
  TweakToggle: ({ children }) => React.createElement('div', null, children),
  TweakSlider: ({ children }) => React.createElement('div', null, children),
  TweakSelect: ({ children }) => React.createElement('div', null, children),
}));

// ─── imports after mocks ──────────────────────────────────────────────────────

import AccountDisabled from './AccountDisabled/index';
import DesignSystem from './DesignSystem/index';
import States from './States/index';
import Variants from './Variants/index';

// ─── tests ───────────────────────────────────────────────────────────────────

describe('AccountDisabled page', () => {
  it('renders without error', () => {
    const { container } = render(React.createElement(AccountDisabled));
    expect(container.firstChild).toBeTruthy();
  });

  it('renders a hi-fi error card with both CTAs and no nav shell', () => {
    const { getByText, queryByTestId, container } = render(
      React.createElement(AccountDisabled),
    );
    // hi-fi scope is active (the `.hf` root powers --hf-* vars + shared classes)…
    expect(container.querySelector('.hf')).toBeTruthy();
    expect(container.querySelector('.btn.primary')).toBeTruthy();
    // …and a suspended account must NOT mount the nav shell.
    expect(queryByTestId('hf-shell')).toBeNull();
    // Both i18n CTAs render (mock t() returns the English fallback).
    expect(getByText('Contact administrator')).toBeTruthy();
    expect(getByText('Switch account')).toBeTruthy();
  });
});

describe('DesignSystem page', () => {
  it('renders without error', () => {
    const { container } = render(React.createElement(DesignSystem));
    expect(container.firstChild).toBeTruthy();
  });
});

describe('States page', () => {
  it('renders without error', () => {
    const { container } = render(React.createElement(States));
    expect(container.firstChild).toBeTruthy();
  });

  it('renders the shell wrapper', () => {
    const { getByTestId } = render(React.createElement(States));
    expect(getByTestId('hf-shell')).toBeTruthy();
  });
});

describe('Variants page', () => {
  it('renders without error', () => {
    const { container } = render(React.createElement(Variants));
    expect(container.firstChild).toBeTruthy();
  });

  it('renders the shell wrapper', () => {
    const { getByTestId } = render(React.createElement(Variants));
    expect(getByTestId('hf-shell')).toBeTruthy();
  });
});
