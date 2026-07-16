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
import User from './pages/User';
import { AuthRedirect, PrivateRoute, AdminRoute } from './helpers';
import OidcRedirect from './components/auth/OidcRedirect';
import NotFound from './pages/NotFound';
import Forbidden from './pages/Forbidden';
import Setting from './pages/Setting';
import { StatusContext } from './context/Status';

import OpenRouterSync from './pages/OpenRouterSync';
import Redemption from './pages/Redemption';
import TopUp from './pages/TopUp';
import Chat from './pages/Chat';
import Chat2Link from './pages/Chat2Link';
import Midjourney from './pages/Midjourney';
import Pricing from './pages/Pricing';
import Task from './pages/Task';
import OidcCallback from './components/auth/OidcCallback';
import PersonalSetting from './components/settings/PersonalSetting';
import Setup from './pages/Setup';
import SetupCheck from './components/layout/SetupCheck';

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
const V2Variants = lazy(() => import('./pages/v2/Variants'));
const V2AccountDisabled = lazy(() => import('./pages/v2/AccountDisabled'));
const V2AdminUsers = lazy(() => import('./pages/v2/Admin/Users'));
const V2AdminSettings = lazy(() => import('./pages/v2/Admin/Settings'));
const V2CostIntelligence = lazy(
  () => import('./pages/v2/Admin/CostIntelligence'),
);
const V2ModelPerformance = lazy(
  () => import('./pages/v2/Admin/ModelPerformance'),
);

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
              <Home />
            </Suspense>
          }
        />
        <Route
          path='/setup'
          element={
            <Suspense fallback={<Loading></Loading>} key={location.pathname}>
              <Setup />
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
            <AdminRoute>
              <OpenRouterSync />
            </AdminRoute>
          }
        />
        <Route
          path='/console/redemption'
          element={
            <AdminRoute>
              <Redemption />
            </AdminRoute>
          }
        />
        <Route
          path='/console/user'
          element={
            <AdminRoute>
              <User />
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
              <OidcCallback />
            </Suspense>
          }
        />
        <Route
          path='/console/setting'
          element={
            <AdminRoute>
              <Suspense fallback={<Loading></Loading>} key={location.pathname}>
                <Setting />
              </Suspense>
            </AdminRoute>
          }
        />
        <Route
          path='/console/personal'
          element={
            <PrivateRoute>
              <Suspense fallback={<Loading></Loading>} key={location.pathname}>
                <PersonalSetting />
              </Suspense>
            </PrivateRoute>
          }
        />
        <Route
          path='/console/topup'
          element={
            <PrivateRoute>
              <Suspense fallback={<Loading></Loading>} key={location.pathname}>
                <TopUp />
              </Suspense>
            </PrivateRoute>
          }
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
                <Midjourney />
              </Suspense>
            </PrivateRoute>
          }
        />
        <Route
          path='/console/task'
          element={
            <PrivateRoute>
              <Suspense fallback={<Loading></Loading>} key={location.pathname}>
                <Task />
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
                  <Pricing />
                </Suspense>
              </PrivateRoute>
            ) : (
              <Suspense fallback={<Loading></Loading>} key={location.pathname}>
                <Pricing />
              </Suspense>
            )
          }
        />
        <Route
          path='/about'
          element={
            <Suspense fallback={<Loading></Loading>} key={location.pathname}>
              <About />
            </Suspense>
          }
        />
        <Route
          path='/user-agreement'
          element={
            <Suspense fallback={<Loading></Loading>} key={location.pathname}>
              <UserAgreement />
            </Suspense>
          }
        />
        <Route
          path='/privacy-policy'
          element={
            <Suspense fallback={<Loading></Loading>} key={location.pathname}>
              <PrivacyPolicy />
            </Suspense>
          }
        />
        <Route
          path='/console/chat/:id?'
          element={
            <Suspense fallback={<Loading></Loading>} key={location.pathname}>
              <Chat />
            </Suspense>
          }
        />
        {/* 方便使用chat2link直接跳转聊天... */}
        <Route
          path='/chat2link'
          element={
            <PrivateRoute>
              <Suspense fallback={<Loading></Loading>} key={location.pathname}>
                <Chat2Link />
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
              <V2AccountDisabled />
            </Suspense>
          }
        />
        {[
          ['dashboard', V2Dashboard],
          ['log', V2Log],
          ['channel', V2Channel],
          ['token', V2Token],
          ['playground', V2Playground],
          ['cmdk', V2CmdK],
          ['models', V2Models],
          ['chat', V2Chat],
          ['tenants', V2Tenants],
          ['pricing', V2Pricing],
          ['redemption', V2Redemption],
          ['billing', V2Billing],
          ['settings', V2Settings],
          ['flows', V2Flows],
          ['design-system', V2DesignSystem],
          ['states', V2States],
          ['variants', V2Variants],
          ['admin/users', V2AdminUsers],
          ['admin/settings', V2AdminSettings],
          ['admin/cost-intelligence', V2CostIntelligence],
          ['admin/model-performance', V2ModelPerformance],
        ].map(([slug, Component]) => (
          <Route
            key={slug}
            path={`/console/v2/${slug}`}
            element={
              <PrivateRoute>
                <Suspense
                  fallback={<HfRouteFallback />}
                  key={location.pathname}
                >
                  <Component />
                </Suspense>
              </PrivateRoute>
            }
          />
        ))}
        <Route path='*' element={<NotFound />} />
      </Routes>
    </SetupCheck>
  );
}

export default App;
