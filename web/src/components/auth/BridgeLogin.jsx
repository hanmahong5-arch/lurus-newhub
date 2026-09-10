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
import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Card, Input, Typography } from '@douyinfe/semi-ui';
import { API } from '../../helpers';
import { setTenantSlug } from '../../helpers/apiMode';

/**
 * Sign-in for a deployment that has no single sign-on.
 *
 * The isolated acceptance instance runs with the identity SDK off on
 * purpose, and this fork carries no password column, so its only way in is
 * POST /api/v2/bridge/exchange — a route the backend registers only where
 * E2E_BRIDGE_TOKEN is set. Before this page existed the only way to use it
 * from a browser was to paste a fetch into the devtools console.
 *
 * The token may ride in the URL fragment (#t=…&u=…) so a single link signs
 * someone in with one click. A fragment is not sent to the server and does
 * not reach access logs; it is still a credential in a URL, so the page
 * strips it from the address bar as soon as it has been exchanged.
 *
 * Which user to become defaults to 1 (the seeded root of an acceptance
 * instance) and can be overridden with u= in the same fragment.
 */

const DEFAULT_USER_ID = '1';

/** Parse `#t=<token>&u=<id>` — both optional. */
export function credentialsFromHash(hash) {
  const params = new URLSearchParams((hash || '').replace(/^#/, ''));
  return {
    token: params.get('t') || '',
    userId: params.get('u') || DEFAULT_USER_ID,
  };
}

const BridgeLogin = () => {
  const { t } = useTranslation();
  // checking → the instance has not answered yet; unavailable → it has no
  // bridge route; prompt → waiting for a token; signing → exchange in
  // flight; failed → the exchange was refused.
  const [phase, setPhase] = useState('checking');
  const [message, setMessage] = useState('');
  const [token, setToken] = useState('');
  const [userId, setUserId] = useState(DEFAULT_USER_ID);

  const signIn = useCallback(
    async (suppliedToken, suppliedUserId) => {
      const trimmed = (suppliedToken || '').trim();
      if (!trimmed) {
        setPhase('prompt');
        return;
      }
      setPhase('signing');
      setMessage('');
      try {
        const res = await API.post(
          `/api/v2/bridge/exchange?token=${encodeURIComponent(trimmed)}&user_id=${encodeURIComponent(suppliedUserId || DEFAULT_USER_ID)}`,
          {},
          { skipErrorHandler: true },
        );
        const user = res?.data?.data;
        if (!res?.data?.success || !user?.id) {
          setPhase('failed');
          setMessage(t('登录失败，请重试'));
          return;
        }
        localStorage.setItem('user', JSON.stringify(user));
        // Only a slug the server resolved: an empty one means it could not
        // name the tenant, and overwriting a working value with a guess is
        // what produced 404s on every console panel before.
        if (user.tenant_slug) {
          setTenantSlug(user.tenant_slug);
        }
        // Drop the credential from the address bar (and from what a
        // screenshot of this tab would show) before leaving.
        window.history.replaceState(null, '', window.location.pathname);
        window.location.replace(
          window.location.origin + '/console/v2/dashboard',
        );
      } catch (err) {
        setPhase('failed');
        setMessage(
          err?.response?.status === 403
            ? t('登录令牌无效')
            : t('登录失败，请重试'),
        );
      }
    },
    [t],
  );

  useEffect(() => {
    let cancelled = false;
    const start = async () => {
      const { token: hashToken, userId: hashUserId } = credentialsFromHash(
        window.location.hash,
      );
      if (cancelled) return;
      setToken(hashToken);
      setUserId(hashUserId);

      let bridgeEnabled = true;
      try {
        const res = await API.get('/api/status', { skipErrorHandler: true });
        bridgeEnabled =
          res?.data?.data?.login_methods?.bridge?.enabled === true;
      } catch (_) {
        // Status unreachable. Let the exchange itself answer rather than
        // refusing on a guess.
        bridgeEnabled = true;
      }
      if (cancelled) return;
      if (!bridgeEnabled) {
        setPhase('unavailable');
        return;
      }
      if (hashToken) {
        signIn(hashToken, hashUserId);
        return;
      }
      setPhase('prompt');
    };
    start();
    return () => {
      cancelled = true;
    };
  }, [signIn]);

  return (
    <div className='flex flex-col items-center justify-center min-h-screen bg-gray-50'>
      <Card className='p-6 shadow-lg w-full max-w-md'>
        <Typography.Title heading={5}>{t('验收登录')}</Typography.Title>
        {phase === 'checking' && (
          <Typography.Text>{t('正在登录，请稍候...')}</Typography.Text>
        )}
        {phase === 'signing' && (
          <Typography.Text>{t('正在登录，请稍候...')}</Typography.Text>
        )}
        {phase === 'unavailable' && (
          <Typography.Text type='warning'>
            {t('此实例未开放验收登录')}
          </Typography.Text>
        )}
        {(phase === 'prompt' || phase === 'failed') && (
          <div className='flex flex-col gap-3 mt-3'>
            {message && (
              <Typography.Text type='danger' data-testid='bridge-login-error'>
                {message}
              </Typography.Text>
            )}
            <Input
              value={token}
              onChange={setToken}
              placeholder={t('粘贴验收令牌')}
              data-testid='bridge-login-token'
            />
            <Button theme='solid' onClick={() => signIn(token, userId)}>
              {t('进入控制台')}
            </Button>
          </div>
        )}
      </Card>
    </div>
  );
};

export default BridgeLogin;
