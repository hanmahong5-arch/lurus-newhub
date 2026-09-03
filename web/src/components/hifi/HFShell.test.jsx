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
import { render, screen, fireEvent } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router-dom';

// Mock the helper chain + heavy children so the shell renders in isolation.
vi.mock('../../helpers', () => ({
  API: {
    get: vi.fn().mockResolvedValue({ data: { data: {} } }),
    post: vi.fn(),
  },
}));

vi.mock('./TenantSwitcher', () => ({
  default: () =>
    React.createElement('div', { 'data-testid': 'tenant-switcher' }),
}));

vi.mock('../../hooks/common/useFormDraft', () => ({
  clearAllDrafts: vi.fn(),
}));

import HFShell from './HFShell';

beforeEach(() => {
  window.localStorage.clear();
});

const setBridgedUser = (role) =>
  window.localStorage.setItem(
    'user',
    JSON.stringify({ role, username: 'shell-test', display_name: 'Shell T.' }),
  );

const renderShell = () =>
  render(
    React.createElement(
      MemoryRouter,
      null,
      React.createElement(
        HFShell,
        { active: 'dashboard' },
        React.createElement('div', null, 'body'),
      ),
    ),
  );

describe('HFShell deferred-surface nav placeholders', () => {
  it('renders MJ/Task logs as a disabled placeholder with an honest title', () => {
    renderShell();

    const el = screen.getByTestId('nav-disabled-mj-logs');
    expect(el).toBeTruthy();
    expect(el.getAttribute('aria-disabled')).toBe('true');
    expect((el.getAttribute('title') || '').length).toBeGreaterThan(0);
  });

  it('the remaining disabled placeholder is not a link (no navigation target)', () => {
    renderShell();
    const el = screen.getByTestId('nav-disabled-mj-logs');
    expect(el.tagName.toLowerCase()).not.toBe('a');
  });

  it('admin-users and admin-settings are real nav links for an admin', () => {
    setBridgedUser(10);
    renderShell();

    // No longer disabled placeholders.
    expect(screen.queryByTestId('nav-disabled-admin-users')).toBeNull();
    expect(screen.queryByTestId('nav-disabled-admin-settings')).toBeNull();

    // Rendered as anchors pointing at the new admin routes.
    const usersLink = screen.getByText('Users (admin)').closest('a');
    expect(usersLink).toBeTruthy();
    expect(usersLink.getAttribute('href')).toBe('/console/v2/admin/users');

    const settingsLink = screen.getByText('Admin settings').closest('a');
    expect(settingsLink).toBeTruthy();
    expect(settingsLink.getAttribute('href')).toBe(
      '/console/v2/admin/settings',
    );
  });
});

describe('HFShell role-gated nav sections', () => {
  it('hides every admin section from a regular user (role 1)', () => {
    setBridgedUser(1);
    renderShell();

    // One representative item per admin section.
    expect(screen.queryByText('Channels')).toBeNull(); // routing & models
    expect(screen.queryByText('Tenants')).toBeNull(); // governance
    expect(screen.queryByText('Gateway health')).toBeNull(); // operations
  });

  it('hides admin sections when no bridged user exists at all', () => {
    renderShell();
    expect(screen.queryByText('Channels')).toBeNull();
    expect(screen.queryByText('Audit trail')).toBeNull();
  });

  it('shows all three admin sections to an admin (role 10)', () => {
    setBridgedUser(10);
    renderShell();

    expect(screen.getByText('Channels').closest('a')).toBeTruthy();
    expect(screen.getByText('Tenants').closest('a')).toBeTruthy();
    expect(screen.getByText('Gateway health').closest('a')).toBeTruthy();
    expect(screen.getByText('Audit trail').closest('a')).toBeTruthy();
  });

  it('keeps account Settings in "my account", visible to a regular user', () => {
    setBridgedUser(1);
    renderShell();

    const settingsLink = screen.getByText('Settings').closest('a');
    expect(settingsLink).toBeTruthy();
    expect(settingsLink.getAttribute('href')).toBe('/console/v2/settings');
  });

  it('renders no hardcoded demo badges (the $241 / counts were fake)', () => {
    setBridgedUser(10);
    const { container } = renderShell();
    expect(container.querySelector('.nav-badge')).toBeNull();
  });
});

describe('HFShell search entry', () => {
  it('clicking the search button navigates to the command palette', () => {
    // Location probe rendered as the shell's page body: after the click the
    // router must be on /console/v2/cmdk — a fake ⌘K button that goes nowhere
    // was the previous (dishonest) behavior.
    const LocationProbe = () => {
      const loc = useLocation();
      return React.createElement(
        'div',
        { 'data-testid': 'loc-probe' },
        loc.pathname,
      );
    };
    render(
      React.createElement(
        MemoryRouter,
        { initialEntries: ['/console/v2/dashboard'] },
        React.createElement(
          HFShell,
          { active: 'dashboard' },
          React.createElement(LocationProbe),
        ),
      ),
    );

    expect(screen.getByTestId('loc-probe').textContent).toBe(
      '/console/v2/dashboard',
    );
    fireEvent.click(screen.getByTestId('shell-search-button'));
    expect(screen.getByTestId('loc-probe').textContent).toBe(
      '/console/v2/cmdk',
    );
  });

  // The rail has rendered a ⌘K badge next to that button since the shell was
  // built, and the whole repo contained exactly one keydown listener — in
  // Playground, for something else. The badge advertised a shortcut that did
  // nothing at all.
  it('⌘K / Ctrl-K opens the command palette', () => {
    const LocationProbe = () => {
      const loc = useLocation();
      return React.createElement(
        'div',
        { 'data-testid': 'loc-probe' },
        loc.pathname,
      );
    };
    const renderAt = (path) =>
      render(
        React.createElement(
          MemoryRouter,
          { initialEntries: [path] },
          React.createElement(
            HFShell,
            { active: 'dashboard' },
            React.createElement(LocationProbe),
          ),
        ),
      );

    const { unmount } = renderAt('/console/v2/dashboard');
    fireEvent.keyDown(window, { key: 'k', metaKey: true });
    expect(screen.getByTestId('loc-probe').textContent).toBe(
      '/console/v2/cmdk',
    );
    unmount();

    // Ctrl-K for non-mac keyboards.
    renderAt('/console/v2/dashboard');
    fireEvent.keyDown(window, { key: 'k', ctrlKey: true });
    expect(screen.getByTestId('loc-probe').textContent).toBe(
      '/console/v2/cmdk',
    );
  });

  it('a bare k does not hijack typing', () => {
    const LocationProbe = () => {
      const loc = useLocation();
      return React.createElement(
        'div',
        { 'data-testid': 'loc-probe' },
        loc.pathname,
      );
    };
    render(
      React.createElement(
        MemoryRouter,
        { initialEntries: ['/console/v2/dashboard'] },
        React.createElement(
          HFShell,
          { active: 'dashboard' },
          React.createElement(LocationProbe),
        ),
      ),
    );

    fireEvent.keyDown(window, { key: 'k' });
    expect(screen.getByTestId('loc-probe').textContent).toBe(
      '/console/v2/dashboard',
    );
  });
});

describe('HFShell help escape hatches', () => {
  it('sidebar footer exposes docs and support links', () => {
    renderShell();

    const docs = screen.getByTestId('shell-docs-link');
    expect(docs.getAttribute('href')).toBe('https://docs.lurus.cn');
    // External target must not hand the opener window to the doc site.
    expect(docs.getAttribute('target')).toBe('_blank');
    expect(docs.getAttribute('rel')).toContain('noopener');

    const support = screen.getByTestId('shell-support-link');
    expect(support.getAttribute('href')).toBe('mailto:support@lurus.cn');
  });

  it('both links carry a visible label (i18n key resolves to a default)', () => {
    renderShell();

    expect(
      screen.getByTestId('shell-docs-link').textContent.trim().length,
    ).toBeGreaterThan(0);
    expect(
      screen.getByTestId('shell-support-link').textContent.trim().length,
    ).toBeGreaterThan(0);
  });
});
