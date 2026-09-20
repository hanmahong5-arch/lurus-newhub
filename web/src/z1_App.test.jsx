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
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import React from 'react';
import { describe, it, expect, afterEach, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import {
  MemoryRouter,
  Navigate,
  useLocation,
  useNavigate,
} from 'react-router-dom';

// App.jsx is a route table. Every page it mounts is replaced by a marker so
// the assertions are about ROUTING (which path lands where, and which guard
// wraps it) rather than about page internals.
//
// `<Navigate>` fires its redirect in a passive effect that render()'s
// synchronous act() flush has already resolved by the time render() returns
// — so a route whose element is `<AdminRoute><Navigate .../></AdminRoute>`
// never shows an `admin-gate` node in the final DOM, only in the target
// route's own guard. adminRouteChildren records what AdminRoute was called
// with (the child element, before it redirects away) so a test can still
// prove the wrapper was there even though the DOM has already moved on.
const { stub, adminRouteChildren } = vi.hoisted(() => ({
  stub: (testId) => ({
    default: () => <div data-testid={testId} />,
  }),
  adminRouteChildren: [],
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
  AdminRoute: ({ children }) => {
    adminRouteChildren.push(children);
    return <div data-testid='admin-gate'>{children}</div>;
  },
  RootRoute: ({ children }) => <div data-testid='root-gate'>{children}</div>,
  AuthRedirect: ({ children }) => <div data-testid='auth-gate'>{children}</div>,
}));

vi.mock('./pages/User', () => stub('page-user'));
vi.mock('./pages/NotFound', () => stub('page-not-found'));
vi.mock('./pages/Forbidden', () => stub('page-forbidden'));
vi.mock('./pages/Setting', () => stub('page-setting'));
vi.mock('./pages/OpenRouterSync', () => stub('page-openrouter-sync'));
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
vi.mock('./components/auth/BridgeLogin', () => stub('page-bridge-login'));
vi.mock('./components/auth/OidcCallback', () => stub('oidc-callback'));
vi.mock('./components/settings/PersonalSetting', () => stub('page-personal'));

vi.mock('./pages/v2/Log', () => stub('v2-log'));
vi.mock('./pages/v2/Tasks', () => stub('v2-tasks'));
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
vi.mock('./pages/v2/Projects', () => stub('v2-projects'));
vi.mock('./pages/v2/Billing', () => stub('v2-billing'));
vi.mock('./pages/v2/Settings', () => stub('v2-settings'));
vi.mock('./pages/v2/Flows', () => stub('v2-flows'));
vi.mock('./pages/v2/DesignSystem', () => stub('v2-design-system'));
vi.mock('./pages/v2/States', () => stub('v2-states'));
vi.mock('./pages/v2/AccountDisabled', () => stub('v2-account-disabled'));
vi.mock('./pages/v2/Admin/Users', () => stub('v2-admin-users'));
vi.mock('./pages/v2/Admin/Audit', () => stub('v2-admin-audit'));
vi.mock('./pages/v2/Admin/Gateway', () => stub('v2-admin-gateway'));
vi.mock('./pages/v2/Admin/Settings', () => stub('v2-admin-settings'));
vi.mock('./pages/v2/Admin/CostIntelligence', () => stub('v2-cost'));
vi.mock('./pages/v2/Admin/ModelPerformance', () => stub('v2-model-perf'));
vi.mock('./pages/v2/Analytics/Rankings', () => stub('v2-rankings'));
vi.mock('./pages/v2/Admin/ModelRateLimits', () => stub('v2-model-limits'));
vi.mock('./pages/v2/Admin/Diagnostics', () => stub('v2-diagnostics'));
vi.mock('./pages/v2/Admin/SystemTasks', () => stub('v2-system-tasks'));
vi.mock('./pages/v2/Admin/Authz', () => stub('v2-admin-authz'));

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
  adminRouteChildren.length = 0;
});

describe('App — legacy console redirects', () => {
  // These redirects are the v1 sunset contract (story-11-3). If a target
  // slug is edited on one side only, users silently land on 404.
  //
  // The third column is the guard the TARGET route carries. It is asserted by
  // name rather than as "any gate": models and channel moved to AdminRoute in
  // cycle 13 L7/W, and a test that accepted whichever gate happened to render
  // would go on passing if one of them were downgraded back to PrivateRoute.
  it.each([
    ['/console', '/console/v2/dashboard', 'private-gate'],
    ['/console/v2', '/console/v2/dashboard', 'private-gate'],
    ['/console/log', '/console/v2/log', 'private-gate'],
    ['/console/models', '/console/v2/models', 'admin-gate'],
    ['/console/channel', '/console/v2/channel', 'admin-gate'],
    ['/console/token', '/console/v2/token', 'private-gate'],
    ['/console/playground', '/console/v2/playground', 'private-gate'],
  ])('%s redirects to %s', async (from, to, gate) => {
    renderAt(from);
    expect(pathname()).toBe(to);
    expect(await screen.findByTestId(gate)).toBeInTheDocument();
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
      ['tasks', 'v2-tasks'],
      ['token', 'v2-token'],
      ['playground', 'v2-playground'],
      ['cmdk', 'v2-cmdk'],
      ['chat', 'v2-chat'],
      ['billing', 'v2-billing'],
      ['settings', 'v2-settings'],
      ['design-system', 'v2-design-system'],
      ['states', 'v2-states'],
      // Stays on PrivateRoute: the audit subgroup is mounted at
      // api-v2-router.go with middleware.RootOrGranted("audit","read"), so a
      // delegated grant reaches it at role 10.
      ['admin/audit', 'v2-admin-audit'],
      // Stays on PrivateRoute: a non-root caller falls back to
      // /api/v2/:tenant_slug/analytics/rankings.
      ['admin/rankings', 'v2-rankings'],
    ];

    for (const [slug, testId] of slugs) {
      const view = renderAt(`/console/v2/${slug}`);
      const page = await screen.findByTestId(testId);
      expect(page, slug).toBeInTheDocument();
      expect(screen.getByTestId('private-gate'), slug).toContainElement(page);
      view.unmount();
    }
  });

  // Cycle-12 L3/W: the ten v2 screens whose every backend call is under
  // /api/v2/admin (mounted behind RootJWTAuth) now carry RootRoute, mirroring
  // components/hifi/HFShell.jsx's minRole:100 nav entries exactly. Before
  // this, a role-10 tenant admin could open the page and watch every panel
  // answer 403 — the console offered a screen the server would never serve.
  it('the root-only v2 screens sit behind RootRoute, not PrivateRoute', async () => {
    const slugs = [
      ['tenants', 'v2-tenants'],
      ['admin/users', 'v2-admin-users'],
      ['admin/gateway', 'v2-admin-gateway'],
      ['admin/settings', 'v2-admin-settings'],
      ['admin/cost-intelligence', 'v2-cost'],
      ['admin/model-performance', 'v2-model-perf'],
      ['admin/model-limits', 'v2-model-limits'],
      ['admin/system-tasks', 'v2-system-tasks'],
      ['admin/authz', 'v2-admin-authz'],
      ['admin/diagnostics', 'v2-diagnostics'],
    ];

    for (const [slug, testId] of slugs) {
      const view = renderAt(`/console/v2/${slug}`);
      const page = await screen.findByTestId(testId);
      expect(page, slug).toBeInTheDocument();
      expect(screen.getByTestId('root-gate'), slug).toContainElement(page);
      expect(screen.queryByTestId('private-gate'), slug).toBeNull();
      view.unmount();
    }
  });

  // Public on purpose: a suspended account must be able to read the
  // explanation page without PrivateRoute bouncing it back into the login
  // bridge (see the comment in App.jsx).
  // Cycle-13 L7/W: six console screens whose nav entries carry minRole:10 in
  // components/hifi/HFShell.jsx, and whose write endpoints refuse a plain
  // member, still RENDERED for one if they typed the URL — an admin console
  // full of failing calls instead of an honest refusal. AdminRoute now gates
  // the route element itself.
  //
  // Both assertions matter: admin-gate must CONTAIN the page (not merely be
  // somewhere on screen), and private-gate must be absent, so swapping the
  // guard back to PrivateRoute cannot pass by leaving both gates rendered.
  it.each([
    ['channel', 'v2-channel'],
    ['models', 'v2-models'],
    ['pricing', 'v2-pricing'],
    ['redemption', 'v2-redemption'],
    ['projects', 'v2-projects'],
    ['flows', 'v2-flows'],
  ])('/console/v2/%s is admin-only', async (slug, testId) => {
    const view = renderAt(`/console/v2/${slug}`);
    const page = await screen.findByTestId(testId);
    expect(screen.getByTestId('admin-gate'), slug).toContainElement(page);
    expect(screen.queryByTestId('private-gate'), slug).toBeNull();
    view.unmount();
  });

  it('the account-disabled landing is reachable without PrivateRoute', async () => {
    renderAt('/console/v2/account-disabled');
    expect(
      await screen.findByTestId('v2-account-disabled'),
    ).toBeInTheDocument();
    expect(screen.queryByTestId('private-gate')).toBeNull();
  });

  it.each([['/console/user', 'page-user']])(
    '%s is admin-only',
    async (path, testId) => {
      renderAt(path);
      const page = await screen.findByTestId(testId);
      expect(screen.getByTestId('admin-gate')).toContainElement(page);
      expect(screen.queryByTestId('private-gate')).toBeNull();
    },
  );

  // Cycle-12 L3/W: both write through /api/v2/admin/*-gated endpoints and both
  // carry minRole:100 in HFShell's nav, so AdminRoute (role >= 10) was one
  // tier too loose — it admitted a tenant admin to a page whose every call
  // the server refuses.
  it.each([
    ['/console/openrouter-sync', 'page-openrouter-sync'],
    ['/console/setting', 'page-setting'],
  ])('%s is root-only', async (path, testId) => {
    renderAt(path);
    const page = await screen.findByTestId(testId);
    expect(screen.getByTestId('root-gate')).toContainElement(page);
    expect(screen.queryByTestId('admin-gate')).toBeNull();
  });

  // The legacy Semi UI shells are gone (console-one-surface, 2026-09-07) —
  // both routes now redirect straight into the v2 pages that replaced them,
  // same as the other /console/* -> /console/v2/* redirects above.
  // The fourth column is the guard the target route carries: /console/topup
  // lands on Billing (PrivateRoute), /console/redemption on Redemption, which
  // is AdminRoute since cycle 13 L7/W.
  it.each([
    ['/console/topup', '/console/v2/billing', 'v2-billing', 'private-gate'],
    [
      '/console/redemption',
      '/console/v2/redemption',
      'v2-redemption',
      'admin-gate',
    ],
  ])('%s redirects to %s', async (from, to, testId, gate) => {
    renderAt(from);
    expect(pathname()).toBe(to);
    expect(await screen.findByTestId(testId)).toBeInTheDocument();
    expect(screen.getByTestId(gate)).toBeInTheDocument();
  });

  // /console/redemption was AdminRoute-gated at HEAD and the redirect must
  // stay wrapped in AdminRoute too. This used to be the only thing standing
  // between a non-admin and the redemption page, because the v2 target was
  // PrivateRoute-only; since cycle 13 L7/W the target carries AdminRoute as
  // well, so the two now agree instead of the legacy path being the weaker
  // of the pair. The final DOM after the redirect completes only shows the
  // TARGET route's guard (private-gate) — see the adminRouteChildren comment
  // above — so this asserts on what AdminRoute was invoked with instead.
  it('/console/redemption stays behind AdminRoute even though it only redirects', () => {
    renderAt('/console/redemption');
    const wrapped = adminRouteChildren.find(
      (child) =>
        React.isValidElement(child) &&
        child.type === Navigate &&
        child.props.to === '/console/v2/redemption',
    );
    expect(wrapped).toBeTruthy();
  });

  it.each([
    ['/console/personal', 'page-personal'],
    ['/console/midjourney', 'page-midjourney'],
    ['/console/task', 'page-task'],
    ['/chat2link', 'page-chat2link'],
    // Cycle-12 L3/W: the chat page had NO guard at all. It reads and writes
    // the signed-in user's own conversations, so an anonymous visitor got a
    // shell whose every call answers 401.
    ['/console/chat/42', 'page-chat'],
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
    // Sign-in for a deployment with no single sign-on. It has to be
    // reachable by a browser that is not signed in — that is the whole
    // situation it exists for — so no guard may sit in front of it.
    ['/bridge-login', 'page-bridge-login'],
  ])('%s renders without a guard', async (path, testId) => {
    renderAt(path);
    expect(await screen.findByTestId(testId)).toBeInTheDocument();
    expect(screen.queryByTestId('private-gate')).toBeNull();
    expect(screen.queryByTestId('admin-gate')).toBeNull();
  });

  it('the chat id segment is optional', async () => {
    renderAt('/console/chat');
    const page = await screen.findByTestId('page-chat');
    expect(page).toBeInTheDocument();
    // Same PrivateRoute as /console/chat/:id — the optional segment must not
    // be a way around the guard.
    expect(screen.getByTestId('private-gate')).toContainElement(page);
  });

  it.each([
    ['/definitely-not-a-route'],
    ['/console/v2/does-not-exist'],
    ['/console/legacy/log'],
    // cycle-11 L4/W: /console/v2/variants was retired (App.jsx no longer
    // imports or mounts pages/v2/Variants) — the route now falls through
    // to NotFound like any other unmounted /console/v2/* path.
    ['/console/v2/variants'],
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

// Cycle-13 L7/W: every routed page in App.jsx is a lazy() chunk, so every
// route element is a <Suspense>. A page that throws while rendering — or a
// chunk whose import rejects, which happens for real to a tab left open
// across a rolling release — unmounts the tree back to the nearest boundary,
// and with none in the route element the visitor got a blank white screen.
// index.jsx's single top-level boundary is not enough on its own: it catches
// the throw, but it replaces the WHOLE app including the navigation, so
// there is no way back. One boundary per route element keeps the failure
// inside the page.
//
// This is asserted against App.jsx's source rather than the DOM because
// ErrorBoundary renders its children untouched on the happy path — it leaves
// no marker to query. ErrorBoundary's own catching behaviour is proven in
// components/common/ErrorBoundary.test.jsx.
describe('App — every Suspense route element has an error boundary', () => {
  const APP_SRC = readFileSync(
    join(dirname(fileURLToPath(import.meta.url)), 'App.jsx'),
    'utf8',
  );

  it('imports ErrorBoundary', () => {
    expect(APP_SRC).toMatch(
      /import\s+ErrorBoundary\s+from\s+'\.\/components\/common\/ErrorBoundary'/,
    );
  });

  it('opens one ErrorBoundary for every Suspense', () => {
    const suspense = (APP_SRC.match(/<Suspense[\s>]/g) || []).length;
    const boundaries = (APP_SRC.match(/<ErrorBoundary>/g) || []).length;
    expect(
      suspense,
      'App.jsx has no <Suspense> blocks — this gate is counting nothing',
    ).toBeGreaterThan(10);
    expect(
      boundaries,
      `App.jsx has ${suspense} <Suspense> blocks but ${boundaries} <ErrorBoundary> wrappers — a route element without one turns a page-level throw into a blank screen`,
    ).toBe(suspense);
  });

  it('puts the boundary INSIDE the Suspense, not around it', () => {
    // Outside, the boundary would also swallow the lazy() suspension
    // promise's rejection path differently and would replace the fallback
    // rather than the page. Every opening tag pair must read
    // `<Suspense ...>` then `<ErrorBoundary>`.
    const pairs = APP_SRC.match(/<Suspense[\s\S]*?>\s*<ErrorBoundary>/g) || [];
    const suspense = (APP_SRC.match(/<Suspense[\s>]/g) || []).length;
    expect(pairs.length).toBe(suspense);
  });
});
