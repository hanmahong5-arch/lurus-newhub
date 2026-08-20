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
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (k) => k,
    i18n: { language: 'en', changeLanguage: (...a) => changeLanguage(...a) },
  }),
}));
const changeLanguage = vi.hoisted(() => vi.fn());

// Every piece of chrome becomes a named marker rather than nothing, so
// "is the sidebar mounted?" and "is the footer mounted?" — the only questions
// this component answers — stay observable.
vi.mock('@douyinfe/semi-ui', () => {
  const Layout = ({ children }) =>
    React.createElement('div', { 'data-testid': 'layout' }, children);
  Layout.Sider = ({ children }) =>
    React.createElement('aside', { 'data-testid': 'sider' }, children);
  Layout.Header = ({ children }) =>
    React.createElement('header', { 'data-testid': 'header' }, children);
  Layout.Content = ({ children, style, role }) =>
    React.createElement(
      'main',
      { 'data-testid': 'content', role, 'data-padding': style?.padding },
      children,
    );
  Layout.Footer = ({ children }) =>
    React.createElement('footer', { 'data-testid': 'footer-slot' }, children);
  return { Layout };
});

vi.mock('./headerbar', () => ({
  default: ({ onMobileMenuToggle, drawerOpen }) =>
    React.createElement(
      'div',
      { 'data-testid': 'headerbar', 'data-drawer': String(drawerOpen) },
      React.createElement(
        'button',
        {
          type: 'button',
          'data-testid': 'menu-toggle',
          onClick: onMobileMenuToggle,
        },
        'menu',
      ),
    ),
}));

vi.mock('./SiderBar', () => ({
  default: ({ onNavigate }) =>
    React.createElement(
      'button',
      { type: 'button', 'data-testid': 'siderbar', onClick: onNavigate },
      'sider',
    ),
}));

vi.mock('./Footer', () => ({
  default: () => React.createElement('div', { 'data-testid': 'footerbar' }),
}));

vi.mock('../../App', () => ({
  default: () => React.createElement('div', { 'data-testid': 'app-routes' }),
}));

vi.mock('react-toastify', () => ({
  ToastContainer: () => React.createElement('div', { 'data-testid': 'toasts' }),
}));

const mobileState = { value: false };
vi.mock('../../hooks/common/useIsMobile', () => ({
  useIsMobile: () => mobileState.value,
}));

const collapsedState = { value: false };
const setCollapsed = vi.fn();
vi.mock('../../hooks/common/useSidebarCollapsed', () => ({
  useSidebarCollapsed: () => [
    collapsedState.value,
    vi.fn(),
    (...a) => setCollapsed(...a),
  ],
}));

const apiGet = vi.fn();
const showError = vi.fn();
const setStatusData = vi.fn();
const getLogo = vi.fn(() => '');
const getSystemName = vi.fn(() => '');
vi.mock('../../helpers', () => ({
  API: { get: (...a) => apiGet(...a) },
  showError: (...a) => showError(...a),
  setStatusData: (...a) => setStatusData(...a),
  getLogo: (...a) => getLogo(...a),
  getSystemName: (...a) => getSystemName(...a),
}));

const locationValue = { pathname: '/console/dashboard' };
vi.mock('react-router-dom', () => ({
  useLocation: () => locationValue,
}));

import { UserContext } from '../../context/User';
import { StatusContext } from '../../context/Status';
import PageLayout from './PageLayout';

const renderAt = (pathname) => {
  locationValue.pathname = pathname;
  const userDispatch = vi.fn();
  const statusDispatch = vi.fn();
  const utils = render(
    React.createElement(
      UserContext.Provider,
      { value: [{}, userDispatch] },
      React.createElement(
        StatusContext.Provider,
        { value: [{}, statusDispatch] },
        React.createElement(PageLayout, null),
      ),
    ),
  );
  return { userDispatch, statusDispatch, ...utils };
};

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  mobileState.value = false;
  collapsedState.value = false;
  apiGet.mockResolvedValue({ data: { success: true, data: { setup: true } } });
  getLogo.mockReturnValue('');
  getSystemName.mockReturnValue('');
});

describe('PageLayout — chrome selection', () => {
  it('wraps a console page in header, sidebar, content and footer', () => {
    renderAt('/console/dashboard');
    expect(screen.getByTestId('headerbar')).toBeInTheDocument();
    expect(screen.getByTestId('siderbar')).toBeInTheDocument();
    expect(screen.getByTestId('app-routes')).toBeInTheDocument();
    expect(screen.getByTestId('footerbar')).toBeInTheDocument();
    expect(screen.getByTestId('toasts')).toBeInTheDocument();
  });

  it('hands v2 screens the bare app so they do not get two sets of chrome', () => {
    renderAt('/console/v2/dashboard');
    expect(screen.getByTestId('app-routes')).toBeInTheDocument();
    expect(screen.queryByTestId('headerbar')).not.toBeInTheDocument();
    expect(screen.queryByTestId('siderbar')).not.toBeInTheDocument();
    expect(screen.queryByTestId('footerbar')).not.toBeInTheDocument();
    // The toast host still has to be there, or nothing can report an error.
    expect(screen.getByTestId('toasts')).toBeInTheDocument();
  });

  it('keeps the sidebar off non-console routes', () => {
    renderAt('/pricing');
    expect(screen.queryByTestId('siderbar')).not.toBeInTheDocument();
    expect(screen.getByTestId('headerbar')).toBeInTheDocument();
  });

  it.each([
    '/console/channel',
    '/console/log',
    '/console/redemption',
    '/console/user',
    '/console/token',
    '/console/midjourney',
    '/console/task',
    '/console/models',
    '/pricing',
  ])('drops the footer on the full-bleed table page %s', (path) => {
    renderAt(path);
    expect(screen.queryByTestId('footerbar')).not.toBeInTheDocument();
  });

  it('pads ordinary console pages but not the chat and playground surfaces', () => {
    const { unmount } = renderAt('/console/dashboard');
    expect(screen.getByTestId('content')).toHaveAttribute(
      'data-padding',
      '24px',
    );
    unmount();

    const { unmount: u2 } = renderAt('/console/playground');
    expect(screen.getByTestId('content')).toHaveAttribute('data-padding', '0');
    u2();

    renderAt('/console/chat/abc');
    expect(screen.getByTestId('content')).toHaveAttribute('data-padding', '0');
  });

  it('uses tighter padding on mobile', () => {
    mobileState.value = true;
    renderAt('/console/dashboard');
    expect(screen.getByTestId('content')).toHaveAttribute(
      'data-padding',
      '5px',
    );
  });

  it('exposes the content region as the main landmark', () => {
    renderAt('/console/dashboard');
    expect(screen.getByRole('main')).toBe(screen.getByTestId('content'));
  });
});

describe('PageLayout — mobile drawer', () => {
  it('hides the sidebar on mobile until the menu is opened', () => {
    mobileState.value = true;
    renderAt('/console/dashboard');
    expect(screen.queryByTestId('siderbar')).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId('menu-toggle'));
    expect(screen.getByTestId('siderbar')).toBeInTheDocument();
    expect(screen.getByTestId('headerbar')).toHaveAttribute(
      'data-drawer',
      'true',
    );
  });

  it('closes the drawer once a navigation happens inside it', () => {
    mobileState.value = true;
    renderAt('/console/dashboard');
    fireEvent.click(screen.getByTestId('menu-toggle'));
    fireEvent.click(screen.getByTestId('siderbar'));
    expect(screen.queryByTestId('siderbar')).not.toBeInTheDocument();
  });

  it('leaves the drawer open when a desktop navigation happens', () => {
    renderAt('/console/dashboard');
    fireEvent.click(screen.getByTestId('siderbar'));
    expect(screen.getByTestId('siderbar')).toBeInTheDocument();
  });

  it('force-expands a collapsed sidebar when the mobile drawer opens', () => {
    mobileState.value = true;
    collapsedState.value = true;
    renderAt('/console/dashboard');
    expect(setCollapsed).not.toHaveBeenCalled();
    fireEvent.click(screen.getByTestId('menu-toggle'));
    expect(setCollapsed).toHaveBeenCalledWith(false);
  });
});

describe('PageLayout — bootstrap side effects', () => {
  it('restores a cached user into the user context', () => {
    localStorage.setItem('user', JSON.stringify({ id: 3, username: 'dave' }));
    const { userDispatch } = renderAt('/console/dashboard');
    expect(userDispatch).toHaveBeenCalledWith({
      type: 'login',
      payload: { id: 3, username: 'dave' },
    });
  });

  it('dispatches nothing when there is no cached user', () => {
    const { userDispatch } = renderAt('/console/dashboard');
    expect(userDispatch).not.toHaveBeenCalled();
  });

  it('publishes the server status to both the context and the helper cache', async () => {
    apiGet.mockResolvedValue({
      data: { success: true, data: { setup: true, version: '1.2.3' } },
    });
    const { statusDispatch } = renderAt('/console/dashboard');
    await waitFor(() =>
      expect(statusDispatch).toHaveBeenCalledWith({
        type: 'set',
        payload: { setup: true, version: '1.2.3' },
      }),
    );
    expect(setStatusData).toHaveBeenCalledWith({
      setup: true,
      version: '1.2.3',
    });
  });

  it('reports an unreachable server rather than rendering a silent blank', async () => {
    apiGet.mockResolvedValue({ data: { success: false } });
    const { statusDispatch } = renderAt('/console/dashboard');
    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('Unable to connect to server'),
    );
    expect(statusDispatch).not.toHaveBeenCalled();
  });

  it('reports a transport failure distinctly from a refusal', async () => {
    apiGet.mockRejectedValue(new Error('offline'));
    renderAt('/console/dashboard');
    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('Failed to load status'),
    );
  });

  it('titles the document from the configured system name', () => {
    getSystemName.mockReturnValue('Acme Hub');
    renderAt('/console/dashboard');
    expect(document.title).toBe('Acme Hub');
  });

  it('leaves the document title alone when no system name is configured', () => {
    document.title = 'untouched';
    getSystemName.mockReturnValue('');
    renderAt('/console/dashboard');
    expect(document.title).toBe('untouched');
  });

  it('points the favicon at the configured logo when one exists', () => {
    const link = document.createElement('link');
    link.setAttribute('rel', 'icon');
    link.href = 'https://old/favicon.ico';
    document.head.appendChild(link);
    getLogo.mockReturnValue('https://cdn.example.com/logo.png');
    renderAt('/console/dashboard');
    expect(document.querySelector("link[rel~='icon']").href).toBe(
      'https://cdn.example.com/logo.png',
    );
    link.remove();
  });

  it('restores the previously chosen UI language', () => {
    localStorage.setItem('i18nextLng', 'ja');
    renderAt('/console/dashboard');
    expect(changeLanguage).toHaveBeenCalledWith('ja');
  });

  it('does not force a language when the user has never chosen one', () => {
    renderAt('/console/dashboard');
    expect(changeLanguage).not.toHaveBeenCalled();
  });
});
