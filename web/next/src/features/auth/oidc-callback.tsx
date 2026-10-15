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
import { useQueryClient } from '@tanstack/react-query'
import { TriangleAlert } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { http, type ApiEnvelope } from '@/lib/api'
import { currentUserQueryKey } from '@/lib/user'

import { AuthStatusCard } from './components/auth-card'
import { AuthLayout } from './components/auth-layout'
import { postLoginUrl } from './lib/login-redirect'
import { navigation } from './lib/navigate'
import { clearTenantSlug, storeUser } from './lib/session-hint'

/** How long the failure stays on screen before the person is sent back. */
const RETURN_DELAY_MS = 3000

/**
 * Landing page of the IdP redirect (/oauth/oidc). The server has already
 * established the session cookie; this page asks who it belongs to, keeps the
 * `user` hint and moves on to the console.
 */
export function OidcCallbackPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined

    const loadSession = async () => {
      try {
        const res = await http.get<ApiEnvelope<unknown>>(
          '/api/v2/auth/session-info',
          { skipAuthRedirect: true }
        )
        if (cancelled) return

        const { success, message, data } = res.data
        if (!success) {
          throw new Error(message || t('Login failed'))
        }

        // The console routes everything through `/api/v2/~`; a slug left over
        // from an earlier account must not survive into this session.
        clearTenantSlug()
        storeUser(data)
        // Whatever the signed-out state cached about the current user is
        // stale now.
        queryClient.removeQueries({ queryKey: currentUserQueryKey })
        toast.success(t('Login successful!'))
        navigation.replace(postLoginUrl(window.location.origin))
      } catch (err) {
        if (cancelled) return
        const msg = (err instanceof Error && err.message) || t('Login failed')
        setError(msg)
        toast.error(msg)
        timer = setTimeout(
          () => navigation.replace('/login'),
          RETURN_DELAY_MS
        )
      }
    }

    void loadSession()
    return () => {
      cancelled = true
      if (timer) clearTimeout(timer)
    }
    // t and queryClient are stable for the life of the page; the exchange
    // must run exactly once per visit.
    // oxlint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  if (error) {
    return (
      <AuthLayout>
        <AuthStatusCard
          testId='oidc-callback-error'
          icon={TriangleAlert}
          tone='danger'
          title={error}
          actions={
            <Button variant='outline' onClick={() => navigation.replace('/login')}>
              {t('Back to sign in')}
            </Button>
          }
        >
          {t('Returning to the sign-in page...')}
        </AuthStatusCard>
      </AuthLayout>
    )
  }

  return (
    <AuthLayout>
      <AuthStatusCard
        busy
        testId='oidc-callback-progress'
        title={t('Signing in, please wait...')}
      />
    </AuthLayout>
  )
}
