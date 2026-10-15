/*
Copyright (C) 2023-2026 QuantumNous

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
import { KeyRound, ShieldAlert } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { buttonVariants } from '@/components/ui/button'
import { api, isApiError } from '@/lib/api'

import { AuthStatusCard } from './components/auth-card'
import { AuthLayout } from './components/auth-layout'
import { inviteLink, readInviteCode } from './lib/invite'
import { loginCapability } from './lib/login-capability'
import { postLoginUrl, readRedirectParam } from './lib/login-redirect'
import { navigation } from './lib/navigate'
import { hasStoredUser, storeTenantSlug, storeUser } from './lib/session-hint'

/**
 * Terminal refusals of the bootstrap call: 403s that signing in again cannot
 * clear. TENANT_SEAT_LIMIT is the tenants.max_users ceiling
 * (handler/zita_bootstrap.go); TENANT_ORG_MISMATCH is the same wall seen from
 * the IdP callback (handler/oauth.go). Anything else behind a 403 is a
 * disabled account.
 */
type DeniedCode = 'TENANT_SEAT_LIMIT' | 'TENANT_ORG_MISMATCH'

function isDeniedCode(code: string | undefined): code is DeniedCode {
  return code === 'TENANT_SEAT_LIMIT' || code === 'TENANT_ORG_MISMATCH'
}

type View =
  | { kind: 'checking' }
  | { kind: 'redirecting'; url: string }
  | { kind: 'denied'; code: DeniedCode }
  | { kind: 'no-sso'; bridge: boolean }

interface BootstrapUser {
  id?: number
  tenant_slug?: string
  [key: string]: unknown
}

/** How long the redirect may take before the page offers a manual link. */
const FALLBACK_DELAY_MS = 3000

/**
 * The sign-in flow for /login, /login/:slug and /register.
 *
 * Accounts, registration and passwords live on the identity platform, so
 * these routes share one flow and the same redirect target serves both
 * "sign in" and "register":
 *
 *  1. A visitor that already holds a session hint goes straight on.
 *  2. POST /api/v2/auth/zita-bootstrap turns the platform SDK cookie into a
 *     newhub session and the canonical user row. On success the person lands
 *     on ?redirect= (or the dashboard).
 *  3. 401 / network failure means no platform session yet: send the browser
 *     to the identity login, unless this instance reports single sign-on off
 *     (the isolated acceptance instance), where the redirect would end on a
 *     raw 503; that case stops and offers the bridge sign-in instead.
 *  4. A 403 is terminal: bouncing back to the IdP would loop because the
 *     bootstrap rejects on every retry.
 *
 * The tenant slug in /login/:slug is not used: the identity platform resolves
 * the account globally rather than per tenant (ADR-0011 layer C).
 */
function SsoSignIn() {
  const { t } = useTranslation()
  const [view, setView] = useState<View>({ kind: 'checking' })
  const [showFallback, setShowFallback] = useState(false)

  useEffect(() => {
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined

    // A session hint means someone is (or was recently) signed in: do not
    // run the whole exchange again. A stale hint is cleared by the first
    // 401 the console gets (lib/auth.ts), which comes back here.
    if (hasStoredUser()) {
      navigation.replace(postLoginUrl(window.location.origin))
      return () => {
        cancelled = true
      }
    }

    const bootstrap = async () => {
      const inviteCode = readInviteCode()
      // Forwarded so a session that is ALREADY platform-authenticated (the
      // SSO round trip landed back here) consumes the invite on this call;
      // also folded into return_to so a visitor who still has to go through
      // the identity platform comes back to this same invite link.
      const inviteQuery = inviteCode
        ? `?invite=${encodeURIComponent(inviteCode)}`
        : ''
      try {
        const user = await api.post<BootstrapUser>(
          `/api/v2/auth/zita-bootstrap${inviteQuery}`,
          {},
          { skipAuthRedirect: true }
        )
        if (cancelled) return
        if (user?.id) {
          storeUser(user)
          storeTenantSlug(user.tenant_slug)
          // ?redirect= wins over the default landing page; only same-site
          // relative paths are accepted (lib/login-redirect.ts).
          navigation.replace(postLoginUrl(window.location.origin))
          return
        }
      } catch (err) {
        if (cancelled) return
        if (isApiError(err) && err.status === 403) {
          if (isDeniedCode(err.code)) {
            setView({ kind: 'denied', code: err.code })
            return
          }
          navigation.replace(`${window.location.origin}/account-disabled`)
          return
        }
        // 401 / network: no platform session yet, fall through to the IdP.
      }
      if (cancelled) return

      const capability = await loginCapability()
      if (cancelled) return
      if (!capability.sso) {
        setView({ kind: 'no-sso', bridge: capability.bridge })
        return
      }

      const returnTo = inviteCode
        ? inviteLink(window.location.origin, inviteCode)
        : postLoginUrl(window.location.origin)
      const url = `/api/v2/auth/zita-login?return_to=${encodeURIComponent(returnTo)}`
      setView({ kind: 'redirecting', url })
      timer = setTimeout(() => setShowFallback(true), FALLBACK_DELAY_MS)
      navigation.assign(url)
    }

    void bootstrap()
    return () => {
      cancelled = true
      if (timer) clearTimeout(timer)
    }
  }, [])

  if (view.kind === 'denied') {
    return (
      <AuthLayout>
        <AuthStatusCard
          testId='sso-denied'
          icon={ShieldAlert}
          tone='danger'
          title={t('Sign-in is not possible')}
        >
          {view.code === 'TENANT_SEAT_LIMIT'
            ? t(
                'This tenant has reached its user limit. Ask an administrator to free a seat or raise the limit, then sign in again.'
              )
            : t(
                "This account belongs to a different organisation than the tenant you are signing in to. Use your own organisation's sign-in link, or ask an administrator."
              )}
        </AuthStatusCard>
      </AuthLayout>
    )
  }

  if (view.kind === 'no-sso') {
    const redirect = readRedirectParam()
    const bridgeHref = redirect
      ? `/bridge-login?redirect=${encodeURIComponent(redirect)}`
      : '/bridge-login'
    return (
      <AuthLayout>
        <AuthStatusCard
          testId='sso-disabled'
          icon={KeyRound}
          tone='warning'
          title={t('Single sign-on is not enabled on this instance')}
          actions={
            view.bridge ? (
              <a href={bridgeHref} className={buttonVariants()}>
                {t('Go to acceptance sign-in')}
              </a>
            ) : undefined
          }
        />
      </AuthLayout>
    )
  }

  const fallbackUrl =
    showFallback && view.kind === 'redirecting' ? view.url : null
  return (
    <AuthLayout>
      <AuthStatusCard
        busy
        testId='sso-progress'
        title={t('Checking your session...')}
        actions={
          fallbackUrl ? (
            <a
              href={fallbackUrl}
              className={buttonVariants({ variant: 'outline' })}
            >
              {t('Continue to sign-in')}
            </a>
          ) : undefined
        }
      >
        {fallbackUrl
          ? t('Redirecting to single sign-on, please wait...')
          : undefined}
      </AuthStatusCard>
    </AuthLayout>
  )
}

/** /login and /login/:tenantSlug. The slug is accepted but not used (see above). */
export function LoginPage(_props: { tenantSlug?: string }) {
  return <SsoSignIn />
}

/** /register: registration is delegated to the identity platform, same flow. */
export function RegisterPage() {
  return <SsoSignIn />
}
