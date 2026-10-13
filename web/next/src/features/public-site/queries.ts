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
import { queryOptions, useQuery, useQueryClient } from '@tanstack/react-query'

import { tenantApi } from '@/lib/api'
import { currentUserQueryKey, type CurrentUser } from '@/lib/user'

import {
  fetchAboutContent,
  fetchHomePageContent,
  fetchLegalDocument,
  fetchModelStatus,
  fetchPublicPricing,
  type LegalKind,
} from './api'

const TEN_MINUTES = 10 * 60_000

export const homeContentQuery = queryOptions({
  queryKey: ['public-site', 'home-content'],
  queryFn: fetchHomePageContent,
  staleTime: 60_000,
  retry: false,
})

export const aboutContentQuery = queryOptions({
  queryKey: ['public-site', 'about'],
  queryFn: fetchAboutContent,
  staleTime: TEN_MINUTES,
  retry: false,
})

export const legalQuery = (kind: LegalKind) =>
  queryOptions({
    queryKey: ['public-site', 'legal', kind],
    queryFn: () => fetchLegalDocument(kind),
    staleTime: TEN_MINUTES,
    retry: false,
  })

export const publicPricingQuery = queryOptions({
  queryKey: ['public-site', 'pricing'],
  queryFn: fetchPublicPricing,
  staleTime: 5 * 60_000,
  retry: false,
})

export const modelStatusQuery = queryOptions({
  queryKey: ['public-site', 'model-status'],
  queryFn: fetchModelStatus,
  staleTime: 30_000,
  retry: false,
})

export const usePublicPricing = () => useQuery(publicPricingQuery)

/**
 * Whether a session exists, found without ever redirecting: the probe opts out
 * of the sign-in bounce in lib/api.ts, and any failure (401 included) simply
 * means "not signed in" on a page that is public either way.
 *
 * Returns `undefined` while unknown, so callers can avoid flashing the wrong
 * call to action.
 */
export function usePublicSession(): { signedIn: boolean | undefined } {
  const client = useQueryClient()
  const { data } = useQuery({
    queryKey: ['public-site', 'session'],
    queryFn: async (): Promise<boolean> => {
      // The console already resolved the user: no second request.
      if (client.getQueryData<CurrentUser>(currentUserQueryKey)) return true
      try {
        await tenantApi.get<CurrentUser>('/user/me', { skipAuthRedirect: true })
        return true
      } catch {
        // 401/403: no session. Other failures read as signed out as well; the
        // console button then routes through sign-in, which sends a live
        // session straight back.
        return false
      }
    },
    staleTime: 60_000,
    retry: false,
  })
  return { signedIn: data }
}
