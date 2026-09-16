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

import HFShell, { visibleNavItems, NAV_SECTIONS } from './HFShell';

beforeEach(() => {
  window.localStorage.clear();
});

const setBridgedUser = (role) =>
  window.localStorage.setItem(
    'user',
    JSON.stringify({ role, username: 'shell-test', display_name: 'Shell T.' }),
  );

// Mirrors components/layout/SiderBar.jsx's own workspaceItems gate for the
// same two flags (set from GET /api/status by helpers/data.js in real use).
const setTasksFeatureFlags = ({ drawing = false, task = false } = {}) => {
  window.localStorage.setItem('enable_drawing', String(drawing));
  window.localStorage.setItem('enable_task', String(task));
};

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
  // Cycle-10 L7: "MJ / Task logs" was a disabled:true placeholder
  // (href:null, aria-disabled, an honest "not available in v2 yet" title)
  // until pages/v2/Tasks existed. It is a real page now, so this asserts
  // the opposite of what this block asserted before — un-skipping a nav
  // item without updating its test would have left the OLD assertions
  // (disabled, no href) passing right alongside the new href, silently
  // proving nothing.
  it('MJ/Task logs is no longer a disabled placeholder', () => {
    setTasksFeatureFlags({ drawing: true });
    renderShell();
    expect(screen.queryByTestId('nav-disabled-mj-logs')).toBeNull();
  });

  it('MJ/Task logs is a real nav link pointing at the tasks page when either feature flag is on', () => {
    setTasksFeatureFlags({ drawing: true });
    renderShell();
    const link = screen.getByText('MJ / Task logs').closest('a');
    expect(link).toBeTruthy();
    expect(link.getAttribute('href')).toBe('/console/v2/tasks');
  });

  it('MJ/Task logs is a real nav link when only the task flag is on', () => {
    setTasksFeatureFlags({ task: true });
    renderShell();
    const link = screen.getByText('MJ / Task logs').closest('a');
    expect(link).toBeTruthy();
    expect(link.getAttribute('href')).toBe('/console/v2/tasks');
  });

  // Both flags default unset (beforeEach clears localStorage) — mirrors
  // components/layout/SiderBar.jsx hiding '绘图日志'/'任务日志' the same way,
  // so a deployment without either feature doesn't advertise a rail link to
  // a page that can only ever render empty.
  it('MJ/Task logs is absent when both feature flags are off', () => {
    renderShell();
    expect(screen.queryByText('MJ / Task logs')).toBeNull();
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

  // L4, 2026-09-12: the rankings leaderboard nav entry sits in the
  // 'operations & insights' section (minRole 10) beside admin-analytics.
  it('admin-rankings is a real nav link pointing at the rankings page', () => {
    setBridgedUser(10);
    renderShell();

    const rankingsLink = screen.getByText('Rankings').closest('a');
    expect(rankingsLink).toBeTruthy();
    expect(rankingsLink.getAttribute('href')).toBe(
      '/console/v2/admin/rankings',
    );
  });

  // Cycle-10 L7: Flows (pages/v2/Flows, wired to real backends by L4) had a
  // registered route but no nav entry at all — reachable only by typing the
  // URL. It sits in 'routing & models' (minRole 10) beside Channels.
  it('Flows is a real nav link pointing at the flows page', () => {
    setBridgedUser(10);
    renderShell();

    const link = screen.getByText('Flows').closest('a');
    expect(link).toBeTruthy();
    expect(link.getAttribute('href')).toBe('/console/v2/flows');
  });

  it('Flows is hidden from a non-admin (role 1), same as Channels', () => {
    setBridgedUser(1);
    renderShell();
    expect(screen.queryByText('Flows')).toBeNull();
  });

  // Cycle-10 L7: /console/openrouter-sync (pages/OpenRouterSync) already had
  // a legacy-rail entry (components/layout/SiderBar.jsx's adminItems) — this
  // is its first entry in the v2 rail. minRole:100 here (not 10) because
  // every mutating endpoint the page calls is RootAuth server-side and the
  // page itself has no client-side role check — see the item's own comment
  // in HFShell.jsx.
  it('OpenRouter sync is hidden from a role-10 admin (its mutating buttons would 403)', () => {
    setBridgedUser(10);
    renderShell();
    expect(screen.queryByText('OpenRouter sync')).toBeNull();
  });

  it('OpenRouter sync is a real nav link pointing at its legacy route for root', () => {
    setBridgedUser(100);
    renderShell();

    const link = screen.getByText('OpenRouter sync').closest('a');
    expect(link).toBeTruthy();
    expect(link.getAttribute('href')).toBe('/console/openrouter-sync');
  });

  it('visibleNavItems hides openrouter-sync from role 10 and shows it to root', () => {
    const forAdmin = visibleNavItems({ role: 10 });
    const routingForAdmin = forAdmin.find((s) => s.h === 'routing & models');
    expect(
      routingForAdmin.items.some((it) => it.id === 'openrouter-sync'),
    ).toBe(false);

    const forRoot = visibleNavItems({ role: 100 });
    const routingForRoot = forRoot.find((s) => s.h === 'routing & models');
    expect(routingForRoot.items.some((it) => it.id === 'openrouter-sync')).toBe(
      true,
    );
  });

  // Cycle-10 L7: the legacy /console/personal page carries real,
  // unported-to-v2 capability (quota-warning notification channels, the
  // legacy system access token) — this is the explicit, labelled nav-rail
  // link to it, not a port. Lives in 'my account' (no minRole), same as
  // Settings beside it — /console/personal is PrivateRoute-only in App.jsx.
  it('the personal-settings link is labelled and points at /console/personal, unrestricted by role', () => {
    setBridgedUser(1);
    renderShell();

    const link = screen.getByText('Notifications & access token').closest('a');
    expect(link).toBeTruthy();
    expect(link.getAttribute('href')).toBe('/console/personal');
  });

  // Both /console/personal and /console/openrouter-sync render the legacy
  // HeaderBar/SiderBar chrome, not this shell (PageLayout.jsx only bypasses
  // it for /console/v2/*), so clicking either leaves the rail entirely with
  // nothing to highlight on return. legacyBridge:true on those two items
  // renders a small marked tag so that isn't a silent surprise.
  it('personal and openrouter-sync carry the "leaves v2" marker; other links do not', () => {
    setBridgedUser(100);
    renderShell();

    expect(screen.getByTestId('nav-legacy-tag-personal')).toBeInTheDocument();
    expect(
      screen.getByTestId('nav-legacy-tag-openrouter-sync'),
    ).toBeInTheDocument();
    // Settings is a real v2 destination (renders inside this shell) — no tag.
    expect(screen.queryByTestId('nav-legacy-tag-settings')).toBeNull();
  });

  // NAV_SECTIONS has zero `disabled: true` items at HEAD ("MJ / Task logs"
  // was the last one, un-disabled by this lane), so the greyed-placeholder
  // render branch (HFShell.jsx's `if (it.disabled)`) has no live nav item
  // exercising it. This pushes a synthetic item into the real, exported
  // NAV_SECTIONS array (not a hand-assembled stub component) so the actual
  // render branch runs, then removes it — mirrors the file's regular data
  // shape (disabled:true, href:null) rather than inventing a new one.
  it('a disabled:true item renders through the real placeholder branch, not as a link', () => {
    const section = NAV_SECTIONS.find((s) => s.h === 'my account');
    const fixtureItem = {
      id: 'fixture-disabled-item',
      href: null,
      disabled: true,
      title: 'fixture reason',
      glyph: section.items[0].glyph,
      label: 'Fixture placeholder',
      key: 'console.nav.__fixture_disabled__',
      badge: '',
    };
    section.items.push(fixtureItem);
    try {
      renderShell();
      const placeholder = screen.getByTestId(
        'nav-disabled-fixture-disabled-item',
      );
      expect(placeholder.getAttribute('aria-disabled')).toBe('true');
      expect(placeholder.tagName).not.toBe('A');
      expect(placeholder.getAttribute('title')).toBe('fixture reason');
    } finally {
      section.items.pop();
    }
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

  // A-F3 regression: HFShell.jsx:594's per-item minRole filter (added
  // alongside admin-system-tasks, minRole:100 inside the minRole:10
  // "operations & insights" section) had no test — deleting it kept every
  // other test in this file green, which would have let a role-10 admin
  // see the root-only "Background tasks" link with no test noticing.
  it('hides the root-only "Background tasks" nav entry from an admin (role 10)', () => {
    setBridgedUser(10);
    renderShell();

    // The section itself (minRole:10) is visible — a sibling item proves it.
    expect(screen.getByText('Gateway health').closest('a')).toBeTruthy();
    expect(screen.queryByText('Background tasks')).toBeNull();
  });

  it('shows the root-only "Background tasks" nav entry to root (role 100)', () => {
    setBridgedUser(100);
    renderShell();

    const link = screen.getByText('Background tasks').closest('a');
    expect(link).toBeTruthy();
    expect(link.getAttribute('href')).toBe('/console/v2/admin/system-tasks');
  });

  // Pure-function companion to the two DOM tests above: pins the actual
  // mechanism (visibleNavItems) directly, independent of how HFShell.jsx
  // renders it — this is what CommandPalette/index.jsx's own tests rely on
  // being correct.
  it('visibleNavItems applies both the section- and item-level minRole gate', () => {
    const forAdmin = visibleNavItems({ role: 10 });
    const opsForAdmin = forAdmin.find((s) => s.h === 'operations & insights');
    expect(opsForAdmin).toBeTruthy();
    expect(opsForAdmin.items.some((it) => it.id === 'admin-system-tasks')).toBe(
      false,
    );

    const forRoot = visibleNavItems({ role: 100 });
    const opsForRoot = forRoot.find((s) => s.h === 'operations & insights');
    expect(opsForRoot.items.some((it) => it.id === 'admin-system-tasks')).toBe(
      true,
    );
  });

  // L8 (cycle 9): the "admin-diagnostics" (Diagnostics) nav entry has the
  // same per-item minRole:100 override as admin-system-tasks above, inside
  // the same minRole:10 "operations & insights" section — both backends it
  // renders (session-affinity purge, TOTP adoption) sit behind RootJWTAuth
  // server-side.
  it('hides the root-only "Diagnostics" nav entry from an admin (role 10)', () => {
    setBridgedUser(10);
    renderShell();

    // The section itself (minRole:10) is visible — a sibling item proves it.
    expect(screen.getByText('Gateway health').closest('a')).toBeTruthy();
    expect(screen.queryByText('Diagnostics')).toBeNull();
  });

  it('shows the root-only "Diagnostics" nav entry to root (role 100)', () => {
    setBridgedUser(100);
    renderShell();

    const link = screen.getByText('Diagnostics').closest('a');
    expect(link).toBeTruthy();
    expect(link.getAttribute('href')).toBe('/console/v2/admin/diagnostics');
  });

  it('visibleNavItems hides admin-diagnostics from role 10 and shows it to root', () => {
    const forAdmin = visibleNavItems({ role: 10 });
    const opsForAdmin = forAdmin.find((s) => s.h === 'operations & insights');
    expect(opsForAdmin.items.some((it) => it.id === 'admin-diagnostics')).toBe(
      false,
    );

    const forRoot = visibleNavItems({ role: 100 });
    const opsForRoot = forRoot.find((s) => s.h === 'operations & insights');
    expect(opsForRoot.items.some((it) => it.id === 'admin-diagnostics')).toBe(
      true,
    );
  });

  // A-F7 (cycle-8 L4 repair round): the "admin-authz" (Permission grants)
  // nav entry has the same per-item minRole:100 override as
  // admin-system-tasks above, inside the same minRole:10 "governance"
  // section — grant MANAGEMENT stays root-only server-side even though the
  // section itself is reachable by a role-10 admin. Unpinned before this:
  // deleting the item's minRole would have kept every other test in this
  // file green.
  it('hides the root-only "Permission grants" nav entry from an admin (role 10)', () => {
    setBridgedUser(10);
    renderShell();

    // The section itself (minRole:10) is visible — a sibling item proves it.
    expect(screen.getByText('Audit trail').closest('a')).toBeTruthy();
    expect(screen.queryByText('Permission grants')).toBeNull();
  });

  it('shows the root-only "Permission grants" nav entry to root (role 100)', () => {
    setBridgedUser(100);
    renderShell();

    const link = screen.getByText('Permission grants').closest('a');
    expect(link).toBeTruthy();
    expect(link.getAttribute('href')).toBe('/console/v2/admin/authz');
  });

  it('visibleNavItems hides admin-authz from role 10 and shows it to root', () => {
    const forAdmin = visibleNavItems({ role: 10 });
    const govForAdmin = forAdmin.find((s) => s.h === 'governance');
    expect(govForAdmin).toBeTruthy();
    expect(govForAdmin.items.some((it) => it.id === 'admin-authz')).toBe(false);

    const forRoot = visibleNavItems({ role: 100 });
    const govForRoot = forRoot.find((s) => s.h === 'governance');
    expect(govForRoot.items.some((it) => it.id === 'admin-authz')).toBe(true);
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
