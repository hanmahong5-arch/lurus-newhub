import {
  Coins,
  GitBranch,
  Layers,
  ListTodo,
  Lock,
  RefreshCw,
  Stethoscope,
  Tags,
  Building2,
  Gauge,
  type LucideIcon,
} from 'lucide-react'

import { canAccess, type Requires } from '@/lib/access'
import { hasMinRole, ROLE } from '@/lib/auth'
import { userAccess, type CurrentUser } from '@/lib/user'

export interface ClassicNavItem {
  id: string
  /** Legacy console page. A plain <a href>: following it leaves the SPA. */
  href: string
  /** English source string; translated at render time. */
  label: string
  icon: LucideIcon
  requires: Requires
  /** The legacy route is wrapped in RootRoute (integer role >= 100). */
  rootOnly?: boolean
}

export interface ClassicNavGroup {
  id: 'classic'
  /** English source string; translated at render time. */
  label: string
  items: readonly ClassicNavItem[]
}

/**
 * Legacy pages that have no equivalent in this console yet. Kept apart from
 * NAV_GROUPS on purpose: these entries are links out, not routes. The gates
 * mirror the legacy console's own route guards (App.jsx AdminRoute/RootRoute).
 */
export const CLASSIC_NAV_GROUP: ClassicNavGroup = {
  id: 'classic',
  label: 'Classic console',
  items: [
    {
      id: 'classic-flows',
      href: '/console/v2/flows',
      label: 'Flows',
      icon: GitBranch,
      requires: 'tenantAdmin',
    },
    {
      id: 'classic-pricing',
      href: '/console/v2/pricing',
      label: 'Pricing',
      icon: Tags,
      requires: 'tenantAdmin',
    },
    {
      id: 'classic-tenants',
      href: '/console/v2/tenants',
      label: 'Tenants',
      icon: Building2,
      requires: 'platformStaff',
      rootOnly: true,
    },
    {
      id: 'classic-model-limits',
      href: '/console/v2/admin/model-limits',
      label: 'Model Rate Limits',
      icon: Gauge,
      requires: 'platformStaff',
      rootOnly: true,
    },
    {
      id: 'classic-authz',
      href: '/console/v2/admin/authz',
      label: 'Authorization',
      icon: Lock,
      requires: 'platformStaff',
      rootOnly: true,
    },
    {
      id: 'classic-model-pools',
      href: '/console/v2/admin/model-pools',
      label: 'Model Pools',
      icon: Layers,
      requires: 'platformStaff',
      rootOnly: true,
    },
    {
      id: 'classic-cost-intelligence',
      href: '/console/v2/admin/cost-intelligence',
      label: 'Cost Intelligence',
      icon: Coins,
      requires: 'platformStaff',
      rootOnly: true,
    },
    {
      id: 'classic-diagnostics',
      href: '/console/v2/admin/diagnostics',
      label: 'Diagnostics',
      icon: Stethoscope,
      requires: 'platformStaff',
      rootOnly: true,
    },
    {
      id: 'classic-system-tasks',
      href: '/console/v2/admin/system-tasks',
      label: 'System Tasks',
      icon: ListTodo,
      requires: 'platformStaff',
      rootOnly: true,
    },
    {
      id: 'classic-openrouter-sync',
      href: '/console/openrouter-sync',
      label: 'OpenRouter Sync',
      icon: RefreshCw,
      requires: 'platformStaff',
      rootOnly: true,
    },
  ],
}

/**
 * The classic group filtered for the caller, or null when nothing is left.
 * UI hygiene only: the legacy console enforces the same gates itself.
 */
export function visibleClassicGroup(
  user: Pick<CurrentUser, 'role' | 'tenant_role' | 'is_payer'> | undefined
): ClassicNavGroup | null {
  const access = userAccess(user)
  const items = CLASSIC_NAV_GROUP.items.filter(
    (item) =>
      canAccess(item.requires, access) &&
      (!item.rootOnly || hasMinRole(user?.role, ROLE.root))
  )
  return items.length > 0 ? { ...CLASSIC_NAV_GROUP, items } : null
}
