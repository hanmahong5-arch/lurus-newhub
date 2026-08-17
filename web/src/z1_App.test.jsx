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
import { describe, it, expect, afterEach, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom';

// App.jsx is a route table. Every page it mounts is replaced by a marker so
// the assertions are about ROUTING (which path lands where, and which guard
// wraps it) rather than about page internals.
const { stub } = vi.hoisted(() => ({
  stub: (testId) => ({
    default: () => <div data-testid={testId} />,
  }),
}));

vi.mock('./components/layout/SetupCheck', () => ({
  default: ({ children }) => <div data-testid='setup-check'>{children}</div>,
}));
vi.mock('./components/common/ui/Loading', () => stub('loading'));
vi.mock('./components/hifi/HfRouteFallback', () => stub('hf-fallback'));
vi.mock('./helpers', () => ({
  PrivateRoute: ({ children }) => (
    <div data-testid='private-gate'>{children}</div>
  ),
  AdminRoute: ({ children }) => <div data-testid='admin-gate'>{children}</div>,
  AuthRedirect: ({ children }) => <div data-testid='auth-gate'>{children}</div>,
}));

vi.mock('./pages/User', () => stub('page-user'));
vi.mock('./pages/NotFound', () => stub('page-not-found'));
vi.mock('./pages/Forbidden', () => stub('page-forbidden'));
vi.mock('./pages/Setting', () => stub('page-setting'));
vi.mock('./pages/OpenRouterSync', () => stub('page-openrouter-sync'));
vi.mock('./pages/Redemption', () => stub('page-redemption'));
vi.mock('./pages/TopUp', () => stub('page-topup'));
vi.mock('./pages/Chat', () => stub('page-chat'));
vi.mock('./pages/Chat2Link', () => stub('page-chat2link'));
vi.mock('./pages/Midjourney', () => stub('page-midjourney'));
vi.mock('./pages/Pricing', () => stub('page-pricing'));
vi.mock('./pages/Task', () => stub('page-task'));
vi.mock('./pages/Setup', () => stub('page-setup'));
vi.mock('./pages/Home', () => stub('page-home'));
vi.mock('./pages/About', () => stub('page-about'));
vi.mock('./pages/UserAgreement', () => stub('page-user-agreement'));
vi.mock('./pages/PrivacyPolicy', () => stub('page-privacy-policy'));
vi.mock('./components/auth/OidcRedirect', () => stub('oidc-redirect'));
vi.mock('./components/auth/OidcCallback', () => stub('oidc-callback'));
vi.mock('./components/settings/PersonalSetting', () => stub('page-personal'));

vi.mock('./pages/v2/Log', () => stub('v2-log'));
vi.mock('./pages/v2/Channel', () => stub('v2-channel'));
vi.mock('./pages/v2/Dashboard', () => stub('v2-dashboard'));
vi.mock('./pages/v2/Token', () => stub('v2-token'));
vi.mock('./pages/v2/Playground', () => stub('v2-playground'));
vi.mock('./pages/v2/CommandPalette', () => stub('v2-cmdk'));
vi.mock('./pages/v2/Models', () => stub('v2-models'));
vi.mock('./pages/v2/Chat', () => stub('v2-chat'));
vi.mock('./pages/v2/Tenants', () => stub('v2-tenants'));
vi.mock('./pages/v2/Pricing', () => stub('v2-pricing'));
vi.mock('./pages/v2/Redemption', () => stub('v2-redemption'));
vi.mock('./pages/v2/Billing', () => stub('v2-billing'));
vi.mock('./pages/v2/Settings', () => stub('v2-settings'));
vi.mock('./pages/v2/Flows', () => stub('v2-flows'));
vi.mock('./pages/v2/DesignSystem', () => stub('v2-design-system'));
vi.mock('./pages/v2/States', () => stub('v2-states'));
vi.mock('./pages/v2/Variants', () => stub('v2-variants'));
vi.mock('./pages/v2/AccountDisabled', () => stub('v2-account-disabled'));
vi.mock('./pages/v2/Admin/Users', () => stub('v2-admin-users'));
vi.mock('./pages/v2/Admin/Audit', () => stub('v2-admin-audit'));
vi.mock('./pages/v2/Admin/Gateway', () => stub('v2-admin-gateway'));
vi.mock('./pages/v2/Admin/Settings', () => stub('v2-admin-settings'));
vi.mock('./pages/v2/Admin/CostIntelligence', () => stub('v2-cost'));
vi.mock('./pages/v2/Admin/ModelPerformance', () => stub('v2-model-perf'));
vi.mock('./pages/v2/Admin/ModelRateLimits', () => stub('v2-model-limits'));

import App from './App';
import { StatusContext } from './context/Status';

const LocationProbe = () => {
  const location = useLocation();
  return <span data-testid='pathname'>{location.pathname}</span>;
};

const BackButton = () => {
  const navigate = useNavigate();
  return <button onClick={() => navigate(-1)}>go-back</button>;
};

const renderAt = (path, status = undefined) =>
  render(
    <MemoryRouter initialEntries={[path]}>
      <StatusContext.Provider value={[{ status }, () => {}]}>
        <App />
        <LocationProbe />
      </StatusContext.Provider>
    </MemoryRouter>,
  );

const pathname = () => screen.getByTestId('pathname').textContent;

afterEach(() => {
  vi.restoreAllMocks();
});

describe('App — legacy console redirects', () => {
  // These redirects are the v1 sunset contract (story-11-3). If a target
  // slug is edited on one side only, users silently land on 404.
  it.each([
    ['/console', '/console/v2/dashboard'],
    ['/console/v2', '/console/v2/dashboard'],
    ['/console/log', '/console/v2/log'],
    ['/console/models', '/console/v2/models'],
    ['/console/channel', '/console/v2/channel'],
    ['/console/token', '/console/v2/token'],
    ['/console/playground', '/console/v2/playground'],
  ])('%s redirects to %s', async (from, to) => {
    renderAt(from);
    expect(pathname()).toBe(to);
    expect(await screen.findByTestId('private-gate')).toBeInTheDocument();
  });

  // Deliberate asymmetry: the retired "deployment" screen folds into the
  // models page rather than a same-named v2 route.
  it('/console/deployment folds into the v2 models page', async () => {
    renderAt('/console/deployment');
    expect(pathname()).toBe('/console/v2/models');
    expect(await screen.findByTestId('v2-models')).toBeInTheDocument();
  });

  // `replace` on the redirects is what keeps the browser Back button out of
  // a bounce loop: without it, Back returns to /console which immediately
  // forwards to the dashboard again and the user can never leave.
  it('redirects replace the history entry instead of stacking one', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter initialEntries={['/about', '/console']} initialIndex={1}>
        <StatusContext.Provider value={[{ status: undefined }, () => {}]}>
          <App />
          <LocationProbe />
          <BackButton />
        </StatusContext.Provider>
      </MemoryRouter>,
    );
    expect(pathname()).toBe('/console/v2/dashboard');

    await user.click(screen.getByText('go-back'));
    expect(pathname()).toBe('/about');
  });
});

describe('App — route guards', () => {
  it('every v2 console screen sits behind PrivateRoute', async () => {
    const slugs = [
      ['dashboard', 'v2-dashboard'],
      ['log', 'v2-log'],
      ['channel', 'v2-channel'],
      ['token', 'v2-token'],
      ['playground', 'v2-playground'],
      ['cmdk', 'v2-cmdk'],
      ['models', 'v2-models'],
      ['chat', 'v2-chat'],
      ['tenants', 'v2-tenants'],
      ['pricing', 'v2-pricing'],
      ['redemption', 'v2-redemption'],
      ['billing', 'v2-billing'],
      ['settings', 'v2-settings'],
      ['flows', 'v2-flows'],
      ['design-system', 'v2-design-system'],
      ['states', 'v2-states'],
      ['variants', 'v2-variants'],
      ['admin/users', 'v2-admin-users'],
      ['admin/audit', 'v2-admin-audit'],
      ['admin/gateway', 'v2-admin-gateway'],
      ['admin/settings', 'v2-admin-settings'],
      ['admin/cost-intelligence', 'v2-cost'],
      ['admin/model-performance', 'v2-model-perf'],
      ['admin/model-limits', 'v2-model-limits'],
    ];

    for (const [slug, testId] of slugs) {
      const view = renderAt(`/console/v2/${slug}`);
      const page = await screen.findByTestId(testId);
      expect(page, slug).toBeInTheDocument();
      expect(screen.getByTestId('private-gate'), slug).toContainElement(page);
      view.unmount();
    }
  });

  // Public on purpose: a suspended account must be able to read the
  // explanation page without PrivateRoute bouncing it back into the login
  // bridge (see the comment in App.jsx).
  it('the account-disabled landing is reachable without PrivateRoute', async () => {
    renderAt('/console/v2/account-disabled');
    expect(
      await screen.findByTestId('v2-account-disabled'),
    ).toBeInTheDocument();
    expect(screen.queryByTestId('private-gate')).toBeNull();
  });

  it.each([
    ['/console/user', 'page-user'],
    ['/console/redemption', 'page-redemption'],
    ['/console/openrouter-sync', 'page-openrouter-sync'],
    ['/console/setting', 'page-setting'],
  ])('%s is admin-only', async (path, testId) => {
    renderAt(path);
    const page = await screen.findByTestId(testId);
    expect(screen.getByTestId('admin-gate')).toContainElement(page);
    expect(screen.queryByTestId('private-gate')).toBeNull();
  });

  it.each([
    ['/console/personal', 'page-personal'],
    ['/console/topup', 'page-topup'],
    ['/console/midjourney', 'page-midjourney'],
    ['/console/task', 'page-task'],
    ['/chat2link', 'page-chat2link'],
  ])('%s requires a signed-in user', async (path, testId) => {
    renderAt(path);
    const page = await screen.findByTestId(testId);
    expect(screen.getByTestId('private-gate')).toContainElement(page);
    expect(screen.queryByTestId('admin-gate')).toBeNull();
  });

  it.each([['/login'], ['/login/acme'], ['/register']])(
    '%s bounces an already-authenticated visitor away via AuthRedirect',
    async (path) => {
      renderAt(path);
      const redirect = await screen.findByTestId('oidc-redirect');
      expect(screen.getByTestId('auth-gate')).toContainElement(redirect);
    },
  );

  it('the OIDC callback is public so the code exchange can complete', async () => {
    renderAt('/oauth/oidc');
    expect(await screen.findByTestId('oidc-callback')).toBeInTheDocument();
    expect(screen.queryByTestId('private-gate')).toBeNull();
    expect(screen.queryByTestId('auth-gate')).toBeNull();
  });
});

describe('App — public routes', () => {
  it.each([
    ['/', 'page-home'],
    ['/setup', 'page-setup'],
    ['/forbidden', 'page-forbidden'],
    ['/about', 'page-about'],
    ['/user-agreement', 'page-user-agreement'],
    ['/privacy-policy', 'page-privacy-policy'],
    ['/console/chat/42', 'page-chat'],
  ])('%s renders without a guard', async (path, testId) => {
    renderAt(path);
    expect(await screen.findByTestId(testId)).toBeInTheDocument();
    expect(screen.queryByTestId('private-gate')).toBeNull();
    expect(screen.queryByTestId('admin-gate')).toBeNull();
  });

  it('the chat id segment is optional', async () => {
    renderAt('/console/chat');
    expect(await screen.findByTestId('page-chat')).toBeInTheDocument();
  });

  it.each([
    ['/definitely-not-a-route'],
    ['/console/v2/does-not-exist'],
    ['/console/legacy/log'],
  ])('%s falls through to NotFound', async (path) => {
    renderAt(path);
    expect(await screen.findByTestId('page-not-found')).toBeInTheDocument();
  });

  it('mounts the whole route table inside SetupCheck', () => {
    renderAt('/forbidden');
    expect(screen.getByTestId('setup-check')).toContainElement(
      screen.getByTestId('page-forbidden'),
    );
  });
});

describe('App — /pricing auth gating from HeaderNavModules', () => {
  it('is public when the backend sent no HeaderNavModules at all', async () => {
    renderAt('/pricing');
    expect(await screen.findByTestId('page-pricing')).toBeInTheDocument();
    expect(screen.queryByTestId('private-gate')).toBeNull();
  });

  it('requires login when pricing.requireAuth is exactly true', async () => {
    renderAt('/pricing', {
      HeaderNavModules: JSON.stringify({ pricing: { requireAuth: true } }),
    });
    const page = await screen.findByTestId('page-pricing');
    expect(screen.getByTestId('private-gate')).toContainElement(page);
  });

  it('stays public when pricing.requireAuth is false', async () => {
    renderAt('/pricing', {
      HeaderNavModules: JSON.stringify({ pricing: { requireAuth: false } }),
    });
    expect(await screen.findByTestId('page-pricing')).toBeInTheDocument();
    expect(screen.queryByTestId('private-gate')).toBeNull();
  });

  // requireAuth is compared with === true, so only a real boolean gates the
  // page; a stringly-typed "true" from an older settings row does not.
  it('ignores a truthy non-boolean requireAuth', async () => {
    renderAt('/pricing', {
      HeaderNavModules: JSON.stringify({ pricing: { requireAuth: 'true' } }),
    });
    expect(await screen.findByTestId('page-pricing')).toBeInTheDocument();
    expect(screen.queryByTestId('private-gate')).toBeNull();
  });

  it('keeps the legacy boolean module format public', async () => {
    renderAt('/pricing', {
      HeaderNavModules: JSON.stringify({ pricing: true }),
    });
    expect(await screen.findByTestId('page-pricing')).toBeInTheDocument();
    expect(screen.queryByTestId('private-gate')).toBeNull();
  });

  it('is public when the config omits the pricing module', async () => {
    renderAt('/pricing', {
      HeaderNavModules: JSON.stringify({ chat: { requireAuth: true } }),
    });
    expect(await screen.findByTestId('page-pricing')).toBeInTheDocument();
    expect(screen.queryByTestId('private-gate')).toBeNull();
  });

  // Fail-open on a corrupted config: the parse error is logged and the page
  // reverts to public, which silently undoes an administrator's decision to
  // gate it. Pinned here because it is a security-visible default.
  it('currently falls back to public and logs when HeaderNavModules is not valid JSON', async () => {
    const error = vi.spyOn(console, 'error').mockImplementation(() => {});

    renderAt('/pricing', { HeaderNavModules: '{not json' });

    expect(await screen.findByTestId('page-pricing')).toBeInTheDocument();
    expect(screen.queryByTestId('private-gate')).toBeNull();
    expect(error).toHaveBeenCalled();
    expect(error.mock.calls[0][0]).toBe('解析顶栏模块配置失败:');
  });

  // Paired contract lock for the defect above, matching how every other defect
  // in this round is recorded. Without it the test above reads like an endorsed
  // design to anyone skimming the test names, rather than a pinned-down bug.
  // Un-skip together with the fix: a config the app cannot parse should not
  // silently drop a route the administrator marked as gated.
  it.skip('should keep a gated route gated when HeaderNavModules cannot be parsed', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});

    renderAt('/pricing', { HeaderNavModules: '{not json' });

    expect(await screen.findByTestId('private-gate')).toBeInTheDocument();
  });

  it('does not gate other public routes when pricing requires auth', async () => {
    renderAt('/about', {
      HeaderNavModules: JSON.stringify({ pricing: { requireAuth: true } }),
    });
    expect(await screen.findByTestId('page-about')).toBeInTheDocument();
    expect(screen.queryByTestId('private-gate')).toBeNull();
  });
});
