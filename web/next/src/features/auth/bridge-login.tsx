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
import { KeyRound, ShieldOff } from 'lucide-react'
import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { api, isApiError } from '@/lib/api'

import { AuthStatusCard } from './components/auth-card'
import { AuthLayout } from './components/auth-layout'
import { credentialsFromHash, DEFAULT_USER_ID } from './lib/bridge-credentials'
import { bridgeOffered } from './lib/login-capability'
import { postLoginUrl } from './lib/login-redirect'
import { navigation } from './lib/navigate'
import { storeTenantSlug, storeUser } from './lib/session-hint'

/**
 * Sign-in for a deployment that has no single sign-on.
 *
 * The isolated acceptance instance runs with the identity SDK off on
 * purpose, and this fork carries no password column, so its only way in is
 * POST /api/v2/bridge/exchange, a route the backend registers only where
 * E2E_BRIDGE_TOKEN is set.
 *
 * The token may ride in the URL fragment (#t=...&u=...) so a single link
 * signs someone in with one click. A fragment is not sent to the server and
 * does not reach access logs; it is still a credential in a URL, so the page
 * strips it from the address bar as soon as it has been exchanged.
 *
 * Which user to become defaults to 1 (the seeded root of an acceptance
 * instance) and can be overridden with u= in the same fragment.
 */

interface BridgeUser {
  id?: number
  tenant_slug?: string
  [key: string]: unknown
}

type Phase =
  // The instance has not answered yet.
  | 'checking'
  // It has no bridge route.
  | 'unavailable'
  // Waiting for a token.
  | 'prompt'
  // Exchange in flight.
  | 'signing'
  // The exchange was refused.
  | 'failed'

export function BridgeLoginPage() {
  const { t } = useTranslation()
  const [phase, setPhase] = useState<Phase>('checking')
  const [message, setMessage] = useState('')
  const [token, setToken] = useState('')
  const [userId, setUserId] = useState(DEFAULT_USER_ID)

  const signIn = useCallback(
    async (suppliedToken: string, suppliedUserId: string) => {
      const trimmed = (suppliedToken || '').trim()
      if (!trimmed) {
        setPhase('prompt')
        return
      }
      setPhase('signing')
      setMessage('')
      try {
        const user = await api.post<BridgeUser>(
          `/api/v2/bridge/exchange?token=${encodeURIComponent(trimmed)}&user_id=${encodeURIComponent(suppliedUserId || DEFAULT_USER_ID)}`,
          {},
          { skipAuthRedirect: true }
        )
        if (!user?.id) {
          setPhase('failed')
          setMessage(t('Login failed, please try again'))
          return
        }
        storeUser(user)
        storeTenantSlug(user.tenant_slug)
        // Drop the credential from the address bar (and from what a
        // screenshot of this tab would show) before leaving. The landing URL
        // is resolved first: replaceState drops ?redirect= from the URL.
        const landing = postLoginUrl(window.location.origin)
        window.history.replaceState(null, '', window.location.pathname)
        navigation.replace(landing)
      } catch (err) {
        setPhase('failed')
        const status = isApiError(err) ? err.status : undefined
        if (status === 403) {
          setMessage(t('That sign-in token was rejected'))
        } else if (status === 429) {
          // The bootstrap rate limiter (5 attempts per minute per address)
          // also guards this route.
          setMessage(
            t('Too many attempts. Wait a minute before trying again.')
          )
        } else {
          setMessage(t('Login failed, please try again'))
        }
      }
    },
    [t]
  )

  useEffect(() => {
    let cancelled = false
    const start = async () => {
      const { token: hashToken, userId: hashUserId } = credentialsFromHash(
        window.location.hash
      )
      setToken(hashToken)
      setUserId(hashUserId)

      const enabled = await bridgeOffered()
      if (cancelled) return
      if (!enabled) {
        setPhase('unavailable')
        return
      }
      if (hashToken) {
        void signIn(hashToken, hashUserId)
        return
      }
      setPhase('prompt')
    }
    void start()
    return () => {
      cancelled = true
    }
  }, [signIn])

  if (phase === 'unavailable') {
    return (
      <AuthLayout>
        <AuthStatusCard
          testId='bridge-login-unavailable'
          icon={ShieldOff}
          tone='warning'
          title={t('Acceptance sign-in is not available on this instance')}
        />
      </AuthLayout>
    )
  }

  if (phase === 'checking' || phase === 'signing') {
    return (
      <AuthLayout>
        <AuthStatusCard
          busy
          testId='bridge-login-progress'
          title={t('Signing in, please wait...')}
        />
      </AuthLayout>
    )
  }

  const onSubmit = (event: FormEvent) => {
    event.preventDefault()
    void signIn(token, userId)
  }

  return (
    <AuthLayout>
      <Card className='shadow-sm'>
        <CardContent className='flex flex-col gap-4 px-6 py-6'>
          <div className='flex flex-col items-center gap-2 text-center'>
            <span className='bg-muted text-muted-foreground flex size-11 items-center justify-center rounded-full'>
              <KeyRound aria-hidden='true' className='size-5' />
            </span>
            <h1 className='text-lg font-semibold'>{t('Acceptance sign-in')}</h1>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Paste the acceptance token for this instance to open the console.'
              )}
            </p>
          </div>
          <form className='flex flex-col gap-3' onSubmit={onSubmit}>
            {message && (
              <p
                role='alert'
                className='text-destructive text-sm'
                data-testid='bridge-login-error'
              >
                {message}
              </p>
            )}
            <div className='flex flex-col gap-1.5'>
              <Label htmlFor='bridge-login-token'>{t('Acceptance token')}</Label>
              <Input
                id='bridge-login-token'
                data-testid='bridge-login-token'
                type='password'
                autoComplete='off'
                spellCheck={false}
                value={token}
                onChange={(event) => setToken(event.target.value)}
                placeholder={t('Paste the acceptance token')}
              />
            </div>
            <Button type='submit' disabled={token.trim() === ''}>
              {t('Open the console')}
            </Button>
          </form>
        </CardContent>
      </Card>
    </AuthLayout>
  )
}
