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
import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';

// The real @douyinfe/semi-ui package eager-loads a lottie animation asset
// that touches HTMLCanvasElement — unrenderable in jsdom (see
// cx_page_layout.test.jsx's identical mock for the same reason). Nav is
// stubbed just enough to reproduce its renderWrapper contract: a Nav.Item's
// itemKey is looked up via renderWrapper and, if a target exists, wrapped in
// the real react-router Link — the exact seam SiderBar's legacy-shell
// retirement lives on. Defined inline (not vi.hoisted) because the mock
// factory itself is lazily evaluated on first import — vi.hoisted would run
// before the `React` import above resolves.
vi.mock('@douyinfe/semi-ui', () => {
  const NavContext = React.createContext(null);

  const Nav = ({ children, renderWrapper }) =>
    React.createElement(
      NavContext.Provider,
      { value: { renderWrapper } },
      React.createElement('nav', null, children),
    );
  Nav.Item = ({ itemKey, text }) => {
    const ctx = React.useContext(NavContext);
    const itemElement = React.createElement(
      'span',
      { 'data-testid': `nav-item-${itemKey}` },
      text,
    );
    if (ctx?.renderWrapper) {
      return ctx.renderWrapper({ itemElement, props: { itemKey } });
    }
    return itemElement;
  };
  Nav.Sub = ({ children }) => React.createElement('div', null, children);

  return {
    Nav,
    Divider: () => React.createElement('hr'),
    Button: ({ children, onClick }) =>
      React.createElement('button', { onClick }, children),
    Skeleton: () => null,
  };
});

vi.mock('../../helpers', () => ({
  isAdmin: () => true,
  isRoot: () => true,
  showError: vi.fn(),
}));

vi.mock('../../helpers/render', () => ({
  getLucideIcon: () => null,
}));

vi.mock('../../hooks/common/useSidebarCollapsed', () => ({
  useSidebarCollapsed: () => [false, vi.fn()],
}));

vi.mock('../../hooks/common/useSidebar', () => ({
  useSidebar: () => ({
    isModuleVisible: () => true,
    hasSectionVisibleModules: () => true,
    loading: false,
  }),
}));

vi.mock('../../hooks/common/useMinimumLoadingTime', () => ({
  useMinimumLoadingTime: () => false,
}));

import SiderBar from './SiderBar';

// The legacy Semi UI topup/redemption shells are retired
// (console-one-surface, 2026-09-07). This drives the real SiderBar
// component's real renderWrapper — a regression that brought either link
// back to the dead /console/topup or /console/redemption paths fails here.
describe('SiderBar — legacy shell links retired', () => {
  it('routes the topup and redemption nav items to their v2 replacements', () => {
    render(
      <MemoryRouter>
        <SiderBar />
      </MemoryRouter>,
    );

    const topupLink = screen.getByTestId('nav-item-topup').closest('a');
    expect(topupLink).toHaveAttribute('href', '/console/v2/billing');

    const redemptionLink = screen
      .getByTestId('nav-item-redemption')
      .closest('a');
    expect(redemptionLink).toHaveAttribute('href', '/console/v2/redemption');
  });
});
