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
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import Loading from '../common/ui/Loading';
import { Button, Card, Typography } from '@douyinfe/semi-ui';
import { API } from '../../helpers';
import { setTenantSlug } from '../../helpers/apiMode';

/**
 * What this deployment can actually sign someone in with.
 *
 * login_methods.oidc answers a different question (the legacy OAuth
 * settings block) and reads false even where sign-on works, so it cannot be
 * used here. login_methods.sso is the login handler's own nil check.
 *
 * Only a definite "no" changes the destination: an unreachable /api/status,
 * or a payload with no sso entry at all, still redirects. That is the path
 * this screen has always taken and the one that reports its own failure, so
 * an absent flag must not be read as "cannot sign in". The bridge flag is
 * the opposite — absent means do not offer it, because offering a sign-in
 * form for a route that is not registered collects a token and 404s.
 */
async function loginCapability() {
  try {
    const res = await API.get('/api/status', { skipErrorHandler: true });
    const methods = res?.data?.data?.login_methods ?? {};
    return {
      sso: methods.sso?.enabled !== false,
      bridge: methods.bridge?.enabled === true,
    };
  } catch (_) {
    return { sso: true, bridge: false };
  }
}

// register prop kept for backward compat with the route declaration in
// App.jsx; platform identity.lurus.cn renders a unified "登录/注册" UI
// so the same redirect target serves both flows. tenantSlug routing
// dropped — Layer C of ADR-0011 routes login through identity, which
// resolves the account globally rather than per-tenant.
const OidcRedirect = (_props) => {
  const { t } = useTranslation();
  const [showFallback, setShowFallback] = useState(false);
  // Set only when the instance reports no single sign-on, so this screen
  // stops instead of navigating into a 503 it cannot come back from.
  const [noSSO, setNoSSO] = useState(null);

  useEffect(() => {
    let cancelled = false;
    let timer;

    // POST /api/v2/auth/zita-bootstrap: SDK cookie → newhub session +
    // real user row. Replaces the band-aid that synthesized a fake
    // localStorage user from /me/zita (no DB binding, no session, every
    // subsequent V1 API call would 401). The bridge endpoint:
    //   - validates the platform-issued cookie via zita.AuthMiddleware
    //   - looks up users WHERE lurus_account_id = ? (auto-creates first
    //     time the visitor lands on newhub)
    //   - establishes the V1 gin session that middleware.UserAuth() expects
    //   - returns the canonical user record for localStorage
    //
    // On 401/network failure (no platform session yet) we fall through
    // to the identity login redirect — same as the prior behavior.
    const bootstrap = async () => {
      try {
        const res = await API.post(
          '/api/v2/auth/zita-bootstrap',
          {},
          { skipErrorHandler: true },
        );
        if (cancelled) return;
        if (res?.data?.success && res.data.data?.id) {
          localStorage.setItem('user', JSON.stringify(res.data.data));
          // Persist tenant_slug so subsequent /api/v2/:tenant_slug/* calls
          // route correctly without an extra round trip. Backend resolves
          // user.TenantId → slug; defaults to "default" if unresolvable.
          if (res.data.data.tenant_slug) {
            setTenantSlug(res.data.data.tenant_slug);
          }
          window.location.replace(
            window.location.origin + '/console/v2/dashboard',
          );
          return;
        }
      } catch (err) {
        // 403 = account disabled. Bouncing back to identity-login would loop
        // (bootstrap rejects on every retry). Surface the terminal state
        // explicitly so the user can contact support or switch accounts.
        if (err?.response?.status === 403) {
          if (cancelled) return;
          window.location.replace(
            window.location.origin + '/console/v2/account-disabled',
          );
          return;
        }
        // 401 / network — fall through to identity redirect.
      }
      if (cancelled) return;

      // The isolated acceptance instance runs with sign-on off on purpose.
      // Sending a browser to /api/v2/auth/zita-login there ends on a raw
      // 503, so ask what this instance supports before navigating.
      const capability = await loginCapability();
      if (cancelled) return;
      if (!capability.sso) {
        setNoSSO(capability);
        return;
      }

      const returnTo = `${window.location.origin}/console/v2/dashboard`;
      const url = `/api/v2/auth/zita-login?return_to=${encodeURIComponent(returnTo)}`;
      timer = setTimeout(() => setShowFallback(true), 3000);
      window.location.href = url;
    };

    bootstrap();
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, []);

  if (noSSO) {
    return (
      <div className='flex flex-col items-center justify-center min-h-screen bg-gray-50'>
        <Card className='p-6 shadow-lg'>
          <Typography.Text className='text-gray-600 block text-center'>
            {t('此实例未启用统一登录')}
          </Typography.Text>
          {noSSO.bridge && (
            <div className='mt-4 flex justify-center'>
              <Button
                theme='solid'
                onClick={() =>
                  window.location.replace(
                    window.location.origin + '/bridge-login',
                  )
                }
              >
                {t('前往验收登录')}
              </Button>
            </div>
          )}
        </Card>
      </div>
    );
  }

  return (
    <div className='flex flex-col items-center justify-center min-h-screen bg-gray-50'>
      <Loading />
      {showFallback && (
        <div className='mt-8'>
          <Card className='p-6 shadow-lg'>
            <Typography.Text className='text-gray-600 block text-center'>
              {t('正在跳转到统一登录，请稍候...')}
            </Typography.Text>
          </Card>
        </div>
      )}
    </div>
  );
};

export default OidcRedirect;
