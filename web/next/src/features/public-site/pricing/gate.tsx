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
import { Link } from '@tanstack/react-router'
import { Ban, LogIn } from 'lucide-react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { buildLoginUrl } from '@/lib/auth'

import { PageShell } from '../components/page-shell'
import { useSiteInfo } from '../lib/use-site-info'
import { usePublicSession } from '../queries'
import { LinkButton } from '../components/link-button'

/**
 * The operator's switches for the price list (HeaderNavModules in
 * /api/status): off hides it, "requires sign-in" asks for a session first.
 * UI gate only: the price list API itself is open by design.
 */
export function PricingGate(props: {
  /** Where sign-in should return to. */
  returnTo: string
  fallback: ReactNode
  children: ReactNode
}): ReactNode {
  const { t } = useTranslation()
  const info = useSiteInfo()
  const { signedIn } = usePublicSession()

  // Until the switches are known, show the loading shape rather than flash
  // the list for a page the operator may have turned off.
  if (!info.loaded) return props.fallback

  if (!info.nav.pricing) {
    return (
      <PageShell narrow>
        <EmptyState
          bordered
          icon={Ban}
          title={t('The price list is not available')}
          description={t('The administrator has turned this page off.')}
          action={
            <LinkButton variant='outline' render={<Link to='/' />}>
              {t('Back to Home')}
            </LinkButton>
          }
        />
      </PageShell>
    )
  }

  if (info.nav.pricingRequiresAuth) {
    if (signedIn === undefined) return props.fallback
    if (!signedIn) {
      return (
        <PageShell narrow>
          <EmptyState
            bordered
            icon={LogIn}
            title={t('Sign in to see the price list')}
            description={t('The administrator requires an account for this page.')}
            action={
              <LinkButton render={<a href={buildLoginUrl(props.returnTo)} />}>
                {t('Sign in')}
              </LinkButton>
            }
          />
        </PageShell>
      )
    }
  }

  return props.children
}
