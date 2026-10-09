import { queryOptions } from '@tanstack/react-query'

import { tenantApi } from '@/lib/api'
import { ROLE, hasMinRole } from '@/lib/auth'

/** Tenant-scoped role on GET /api/v2/~/user/me (users.tenant_role). */
export type TenantRole = 'admin' | 'dept_lead' | ''

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
  tenant_role?: TenantRole
  is_payer?: boolean
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

/** What the signed-in person may see. UI hygiene only; the server enforces. */
export interface UserAccess {
  tenantRole: TenantRole
  isPayer: boolean
  /** Our own people. Backend isPlatformStaff: integer role >= RoleAdminUser (10). */
  isPlatformStaff: boolean
  /** Platform staff, or tenant_role = admin (backend requireTenantAdmin). */
  isTenantAdmin: boolean
  isDeptLead: boolean
}

export function userAccess(
  user: Pick<CurrentUser, 'role' | 'tenant_role' | 'is_payer'> | undefined
): UserAccess {
  const tenantRole: TenantRole =
    user?.tenant_role === 'admin' || user?.tenant_role === 'dept_lead'
      ? user.tenant_role
      : ''
  const isPlatformStaff = hasMinRole(user?.role, ROLE.admin)
  return {
    tenantRole,
    isPayer: user?.is_payer === true,
    isPlatformStaff,
    isTenantAdmin: isPlatformStaff || tenantRole === 'admin',
    isDeptLead: tenantRole === 'dept_lead',
  }
}
