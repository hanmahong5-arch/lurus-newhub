import { queryOptions } from '@tanstack/react-query'

import { tenantApi } from '@/lib/api'

/** Subset of GET /api/v2/~/user/me (handler.GetSelfV2) the shell needs. */
export interface CurrentUser {
  id: number
  username: string
  display_name?: string
  email?: string
  role: number
  status?: number
  group?: string
  quota: number
  used_quota: number
  remaining_quota: number
  request_count?: number
  tenant_slug?: string
  token_count?: number
}

export const currentUserQueryKey = ['current-user'] as const

/**
 * The signed-in user. A 401 here sends the browser to the sign-in page (see
 * lib/api.ts), so a resolved value always means a live session.
 */
export const currentUserQueryOptions = queryOptions({
  queryKey: currentUserQueryKey,
  queryFn: () => tenantApi.get<CurrentUser>('/user/me'),
  staleTime: 60_000,
  retry: false,
})
