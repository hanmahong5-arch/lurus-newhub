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
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';

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

  it('admin-users and admin-settings are now real nav links (round 2 wired)', () => {
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
