import type { UserAccess } from '@/lib/user'

/**
 * Who may see an entry or open a route.
 * - everyone: any signed-in user
 * - orgLead: tenant admin (or staff) or department lead
 * - tenantAdmin: tenant admin or platform staff
 * - platformStaff: platform staff only
 * - platformRoot: platform root only (the server's root-only routes)
 */
export type Requires =
  | 'everyone'
  | 'orgLead'
  | 'tenantAdmin'
  | 'platformStaff'
  | 'platformRoot'

export function canAccess(requires: Requires, access: UserAccess): boolean {
  switch (requires) {
    case 'everyone':
      return true
    case 'orgLead':
      return access.isTenantAdmin || access.isDeptLead
    case 'tenantAdmin':
      return access.isTenantAdmin
    case 'platformStaff':
      return access.isPlatformStaff
    case 'platformRoot':
      return access.isPlatformRoot
  }
}
