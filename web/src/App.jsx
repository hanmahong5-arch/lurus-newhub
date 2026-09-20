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

import React, { lazy, Suspense, useContext, useMemo } from 'react';
import { Navigate, Route, Routes, useLocation } from 'react-router-dom';
import Loading from './components/common/ui/Loading';
import HfRouteFallback from './components/hifi/HfRouteFallback';
import { AuthRedirect, PrivateRoute, AdminRoute, RootRoute } from './helpers';
import ErrorBoundary from './components/common/ErrorBoundary';
import OidcRedirect from './components/auth/OidcRedirect';
import NotFound from './pages/NotFound';
import Forbidden from './pages/Forbidden';
import { StatusContext } from './context/Status';
import SetupCheck from './components/layout/SetupCheck';

// Every routed page below is code-split. These eleven were static imports, so
// their whole widget trees sat in the entry chunk a first-time visitor
// downloads before anything paints. NotFound / Forbidden / OidcRedirect /
// SetupCheck stay static: they are what a failed or suspended load falls back
// TO.
const User = lazy(() => import('./pages/User'));
const Setting = lazy(() => import('./pages/Setting'));
const OpenRouterSync = lazy(() => import('./pages/OpenRouterSync'));
const Chat = lazy(() => import('./pages/Chat'));
const Chat2Link = lazy(() => import('./pages/Chat2Link'));
const Midjourney = lazy(() => import('./pages/Midjourney'));
const Pricing = lazy(() => import('./pages/Pricing'));
const Task = lazy(() => import('./pages/Task'));
const OidcCallback = lazy(() => import('./components/auth/OidcCallback'));
const PersonalSetting = lazy(
  () => import('./components/settings/PersonalSetting'),
);
const Setup = lazy(() => import('./pages/Setup'));

// Lazy like the routes below, not a static import: this screen is reached
// only on a deployment without single sign-on, and pulling its editor
// widgets into the app entry would load them for everyone.
const BridgeLogin = lazy(() => import('./components/auth/BridgeLogin'));
const Home = lazy(() => import('./pages/Home'));
const About = lazy(() => import('./pages/About'));
const UserAgreement = lazy(() => import('./pages/UserAgreement'));
const PrivacyPolicy = lazy(() => import('./pages/PrivacyPolicy'));

// v2 hi-fi (editorial dev-tool aesthetic) — see web/src/styles/hifi-tokens.css.
// Standalone shell, bypassed by PageLayout's legacy chrome for /console/v2/*.
const V2Log = lazy(() => import('./pages/v2/Log'));
const V2Channel = lazy(() => import('./pages/v2/Channel'));
const V2Dashboard = lazy(() => import('./pages/v2/Dashboard'));
const V2Token = lazy(() => import('./pages/v2/Token'));
const V2Playground = lazy(() => import('./pages/v2/Playground'));
const V2CmdK = lazy(() => import('./pages/v2/CommandPalette'));
const V2Models = lazy(() => import('./pages/v2/Models'));
const V2Chat = lazy(() => import('./pages/v2/Chat'));
const V2Tenants = lazy(() => import('./pages/v2/Tenants'));
const V2Pricing = lazy(() => import('./pages/v2/Pricing'));
const V2Redemption = lazy(() => import('./pages/v2/Redemption'));
const V2Billing = lazy(() => import('./pages/v2/Billing'));
const V2Settings = lazy(() => import('./pages/v2/Settings'));
const V2Flows = lazy(() => import('./pages/v2/Flows'));
const V2DesignSystem = lazy(() => import('./pages/v2/DesignSystem'));
const V2States = lazy(() => import('./pages/v2/States'));
const V2AccountDisabled = lazy(() => import('./pages/v2/AccountDisabled'));
const V2AdminUsers = lazy(() => import('./pages/v2/Admin/Users'));
const V2AdminAudit = lazy(() => import('./pages/v2/Admin/Audit'));
const V2AdminGateway = lazy(() => import('./pages/v2/Admin/Gateway'));
const V2AdminSettings = lazy(() => import('./pages/v2/Admin/Settings'));
const V2CostIntelligence = lazy(
  () => import('./pages/v2/Admin/CostIntelligence'),
);
const V2ModelPerformance = lazy(
  () => import('./pages/v2/Admin/ModelPerformance'),
);
const V2Rankings = lazy(() => import('./pages/v2/Analytics/Rankings'));
const V2SystemTasks = lazy(() => import('./pages/v2/Admin/SystemTasks'));
const V2AdminAuthz = lazy(() => import('./pages/v2/Admin/Authz'));
const V2Projects = lazy(() => import('./pages/v2/Projects'));
const V2ModelRateLimits = lazy(
  () => import('./pages/v2/Admin/ModelRateLimits'),
);
const V2Diagnostics = lazy(() => import('./pages/v2/Admin/Diagnostics'));
// Self-scoped async job logs (task + midjourney), replacing the nav rail's
// former disabled:true "MJ / Task logs" placeholder (see HFShell.jsx).
const V2Tasks = lazy(() => import('./pages/v2/Tasks'));

function App() {
  const location = useLocation();
  const [statusState] = useContext(StatusContext);

  // 获取模型广场权限配置
  const pricingRequireAuth = useMemo(() => {
    const headerNavModulesConfig = statusState?.status?.HeaderNavModules;
    if (headerNavModulesConfig) {
      try {
        const modules = JSON.parse(headerNavModulesConfig);

        // 处理向后兼容性：如果pricing是boolean，默认不需要登录
        if (typeof modules.pricing === 'boolean') {
          return false; // 默认不需要登录鉴权
        }

        // 如果是对象格式，使用requireAuth配置
        return modules.pricing?.requireAuth === true;
      } catch (error) {
        console.error('解析顶栏模块配置失败:', error);
        return false; // 默认不需要登录
      }
    }
    return false; // 默认不需要登录
  }, [statusState?.status?.HeaderNavModules]);

  return (
    <SetupCheck>
      <Routes>
        <Route
          path='/'
          element={
            <Suspense fallback={<Loading></Loading>} key={location.pathname}>
              <ErrorBoundary>
                <Home />
              </ErrorBoundary>
            </Suspense>
          }
        />
        <Route
          path='/setup'
          element={
            <Suspense fallback={<Loading></Loading>} key={location.pathname}>
              <ErrorBoundary>
                <Setup />
              </ErrorBoundary>
            </Suspense>
          }
        />
        <Route path='/forbidden' element={<Forbidden />} />
        {/* v2 pages fully wired (story-11-2). /console/* redirects to the
            v2 hi-fi surfaces. /console/legacy/* escape-hatch routes
            removed in story-11-3 (v1 web UI sunset, 2026-05-12). */}
        <Route
          path='/console/models'
          element={<Navigate to='/console/v2/models' replace />}
        />
        <Route
          path='/console/channel'
          element={<Navigate to='/console/v2/channel' replace />}
        />
        <Route
          path='/console/token'
          element={<Navigate to='/console/v2/token' replace />}
        />
        <Route
          path='/console/playground'
          element={<Navigate to='/console/v2/playground' replace />}
        />
        <Route
          path='/console/deployment'
          element={<Navigate to='/console/v2/models' replace />}
        />
        <Route
          path='/console/openrouter-sync'
          element={
            <RootRoute>
              <Suspense fallback={<Loading></Loading>} key={location.pathname}>
                <ErrorBoundary>
                  <OpenRouterSync />
                </ErrorBoundary>
              </Suspense>
            </RootRoute>
          }
        />
        {/* Legacy Semi UI redemption shell retired (console-one-surface,
            2026-09-07) — the v2 redemption page supersedes it. The target
            route is PrivateRoute-only (admin is enforced server-side by the
            redemptions API, not by the route), so this redirect keeps the
            AdminRoute wrapper the legacy path had: a non-admin following an
            old link still gets the Forbidden page instead of an admin page
            shell whose list call answers 403. */}
        <Route
          path='/console/redemption'
          element={
            <AdminRoute>
              <Navigate to='/console/v2/redemption' replace />
            </AdminRoute>
          }
        />
        <Route
          path='/console/user'
          element={
            <AdminRoute>
              <Suspense fallback={<Loading></Loading>} key={location.pathname}>
                <ErrorBoundary>
                  <User />
                </ErrorBoundary>
              </Suspense>
            </AdminRoute>
          }
        />
        {/* Legacy /user/reset and /login/password removed — auth delegated to OIDC IdP */}
        <Route
          path='/login/:tenantSlug'
          element={
            <AuthRedirect>
              <OidcRedirect />
            </AuthRedirect>
          }
        />
        <Route
          path='/login'
          element={
            <AuthRedirect>
              <OidcRedirect />
            </AuthRedirect>
          }
        />
        {/* Sign-in for a deployment with no single sign-on (the isolated
            acceptance instance). Deliberately outside AuthRedirect: a stale
            or half-established session is exactly when this page is needed,
            and bouncing it away would leave no way back in. The route the
            page calls exists only where the backend registered it. */}
        <Route
          path='/bridge-login'
          element={
            <Suspense fallback={<Loading></Loading>} key={location.pathname}>
              <ErrorBoundary>
                <BridgeLogin />
              </ErrorBoundary>
            </Suspense>
          }
        />
        {/* Legacy /register/password removed — registration delegated to OIDC IdP */}
        <Route
          path='/register'
          element={
            <AuthRedirect>
              <OidcRedirect register />
            </AuthRedirect>
          }
        />
        {/* Legacy /reset and /oauth/{github,discord,oidc,linuxdo} removed — auth delegated to OIDC IdP */}
        {/* redirect URI /oauth/oidc 须在 IdP(Casdoor)Application 注册,owner-gated */}
        <Route
          path='/oauth/oidc'
          element={
            <Suspense fallback={<Loading></Loading>} key={location.pathname}>
              <ErrorBoundary>
                <OidcCallback />
              </ErrorBoundary>
            </Suspense>
          }
        />
        <Route
          path='/console/setting'
          element={
            <RootRoute>
              <Suspense fallback={<Loading></Loading>} key={location.pathname}>
                <ErrorBoundary>
                  <Setting />
                </ErrorBoundary>
              </Suspense>
            </RootRoute>
          }
        />
        <Route
          path='/console/personal'
          element={
            <PrivateRoute>
              <Suspense fallback={<Loading></Loading>} key={location.pathname}>
                <ErrorBoundary>
                  <PersonalSetting />
                </ErrorBoundary>
              </Suspense>
            </PrivateRoute>
          }
        />
        {/* Legacy Semi UI topup shell retired (console-one-surface,
            2026-09-07) — the v2 billing page (which also carries the
            redeem-a-code flow this shell used to own) supersedes it. */}
        <Route
          path='/console/topup'
          element={<Navigate to='/console/v2/billing' replace />}
        />
        {/* Entry-level /console routes go to v2. */}
        <Route
          path='/console/log'
          element={<Navigate to='/console/v2/log' replace />}
        />
        <Route
          path='/console'
          element={<Navigate to='/console/v2/dashboard' replace />}
        />
        <Route
          path='/console/midjourney'
          element={
            <PrivateRoute>
              <Suspense fallback={<Loading></Loading>} key={location.pathname}>
                <ErrorBoundary>
                  <Midjourney />
                </ErrorBoundary>
              </Suspense>
            </PrivateRoute>
          }
        />
        <Route
          path='/console/task'
          element={
            <PrivateRoute>
              <Suspense fallback={<Loading></Loading>} key={location.pathname}>
                <ErrorBoundary>
                  <Task />
                </ErrorBoundary>
              </Suspense>
            </PrivateRoute>
          }
        />
        <Route
          path='/pricing'
          element={
            pricingRequireAuth ? (
              <PrivateRoute>
                <Suspense
                  fallback={<Loading></Loading>}
                  key={location.pathname}
                >
                  <ErrorBoundary>
                    <Pricing />
                  </ErrorBoundary>
                </Suspense>
              </PrivateRoute>
            ) : (
              <Suspense fallback={<Loading></Loading>} key={location.pathname}>
                <ErrorBoundary>
                  <Pricing />
                </ErrorBoundary>
              </Suspense>
            )
          }
        />
        <Route
          path='/about'
          element={
            <Suspense fallback={<Loading></Loading>} key={location.pathname}>
              <ErrorBoundary>
                <About />
              </ErrorBoundary>
            </Suspense>
          }
        />
        <Route
          path='/user-agreement'
          element={
            <Suspense fallback={<Loading></Loading>} key={location.pathname}>
              <ErrorBoundary>
                <UserAgreement />
              </ErrorBoundary>
            </Suspense>
          }
        />
        <Route
          path='/privacy-policy'
          element={
            <Suspense fallback={<Loading></Loading>} key={location.pathname}>
              <ErrorBoundary>
                <PrivacyPolicy />
              </ErrorBoundary>
            </Suspense>
          }
        />
        <Route
          path='/console/chat/:id?'
          element={
            <PrivateRoute>
              <Suspense fallback={<Loading></Loading>} key={location.pathname}>
                <ErrorBoundary>
                  <Chat />
                </ErrorBoundary>
              </Suspense>
            </PrivateRoute>
          }
        />
        {/* 方便使用chat2link直接跳转聊天... */}
        <Route
          path='/chat2link'
          element={
            <PrivateRoute>
              <Suspense fallback={<Loading></Loading>} key={location.pathname}>
                <ErrorBoundary>
                  <Chat2Link />
                </ErrorBoundary>
              </Suspense>
            </PrivateRoute>
          }
        />
        {/* ─── v2 hi-fi screens (editorial dev-tool aesthetic) ─── */}
        <Route
          path='/console/v2'
          element={<Navigate to='/console/v2/dashboard' replace />}
        />
        {/* Account-disabled landing — public so suspended visitors don't
            bounce off PrivateRoute back into the bridge loop. */}
        <Route
          path='/console/v2/account-disabled'
          element={
            <Suspense fallback={<HfRouteFallback />}>
              <ErrorBoundary>
                <V2AccountDisabled />
              </ErrorBoundary>
            </Suspense>
          }
        />
        {[
          ['dashboard', V2Dashboard],
          ['log', V2Log],
          ['tasks', V2Tasks],
          ['channel', V2Channel, AdminRoute],
          ['token', V2Token],
          ['playground', V2Playground],
          ['cmdk', V2CmdK],
          ['models', V2Models, AdminRoute],
          ['chat', V2Chat],
          ['tenants', V2Tenants, RootRoute],
          ['pricing', V2Pricing, AdminRoute],
          ['redemption', V2Redemption, AdminRoute],
          ['projects', V2Projects, AdminRoute],
          ['billing', V2Billing],
          ['settings', V2Settings],
          ['flows', V2Flows, AdminRoute],
          ['design-system', V2DesignSystem],
          ['states', V2States],
          ['admin/users', V2AdminUsers, RootRoute],
          ['admin/audit', V2AdminAudit],
          ['admin/gateway', V2AdminGateway, RootRoute],
          ['admin/settings', V2AdminSettings, RootRoute],
          ['admin/cost-intelligence', V2CostIntelligence, RootRoute],
          ['admin/model-performance', V2ModelPerformance, RootRoute],
          ['admin/rankings', V2Rankings],
          ['admin/model-limits', V2ModelRateLimits, RootRoute],
          ['admin/system-tasks', V2SystemTasks, RootRoute],
          ['admin/authz', V2AdminAuthz, RootRoute],
          ['admin/diagnostics', V2Diagnostics, RootRoute],
          // Third tuple slot = the route guard, defaulting to PrivateRoute.
          // The RootRoute rows mirror components/hifi/HFShell.jsx minRole:100
          // exactly: every backend call those pages make is under
          // /api/v2/admin, which api-v2-router.go mounts behind RootJWTAuth.
          // The AdminRoute rows mirror HFShell.jsx minRole:10 the same way:
          // channel/models/pricing/redemption/projects/flows are hidden from a
          // plain member's navigation, and their write endpoints refuse one,
          // but before cycle 13 the URL still rendered the whole screen — a
          // member who typed or bookmarked it got an admin console full of
          // failing calls instead of an honest refusal.
        ].map(([slug, Component, Guard = PrivateRoute]) => (
          <Route
            key={slug}
            path={`/console/v2/${slug}`}
            element={
              <Guard>
                <Suspense
                  fallback={<HfRouteFallback />}
                  key={location.pathname}
                >
                  <ErrorBoundary>
                    <Component />
                  </ErrorBoundary>
                </Suspense>
              </Guard>
            }
          />
        ))}
        <Route path='*' element={<NotFound />} />
      </Routes>
    </SetupCheck>
  );
}

export default App;
