import {
  BarChart3,
  Boxes,
  FolderTree,
  KeyRound,
  KeySquare,
  LayoutDashboard,
  Radio,
  Receipt,
  ScrollText,
  ShieldCheck,
  SlidersHorizontal,
  Users,
  type LucideIcon,
} from 'lucide-react'

import { canAccess, type Requires } from '@/lib/access'
import type { UserAccess } from '@/lib/user'

export interface NavItem {
  id: string
  /** Route path relative to the router basepath (/next). */
  to:
    | '/dashboard'
    | '/keys'
    | '/usage-logs'
    | '/models'
    | '/org/members'
    | '/org/departments'
    | '/org/keys-batch'
    | '/org/billing'
    | '/org/policy'
    | '/ops/channels'
    | '/ops/overview'
    | '/ops/rules'
  /** English source string; translated at render time. */
  label: string
  icon: LucideIcon
  /** Who sees the entry. UI hygiene only; the server enforces. */
  requires: Requires
}

export interface NavGroup {
  id: 'workspace' | 'org' | 'ops'
  /** English source string; translated at render time. */
  label: string
  items: readonly NavItem[]
}

export const NAV_GROUPS: readonly NavGroup[] = [
  {
    id: 'workspace',
    label: 'Workbench',
    items: [
      {
        id: 'dashboard',
        to: '/dashboard',
        label: 'Dashboard',
        icon: LayoutDashboard,
        requires: 'everyone',
      },
      {
        id: 'keys',
        to: '/keys',
        label: 'API Keys',
        icon: KeyRound,
        requires: 'everyone',
      },
      {
        id: 'usage-logs',
        to: '/usage-logs',
        label: 'Usage Logs',
        icon: ScrollText,
        requires: 'everyone',
      },
      {
        id: 'models',
        to: '/models',
        label: 'Models',
        icon: Boxes,
        requires: 'everyone',
      },
    ],
  },
  {
    id: 'org',
    label: 'Enterprise Management',
    items: [
      {
        id: 'org-members',
        to: '/org/members',
        label: 'Members & Invitations',
        icon: Users,
        requires: 'tenantAdmin',
      },
      {
        id: 'org-departments',
        to: '/org/departments',
        label: 'Departments',
        icon: FolderTree,
        requires: 'orgLead',
      },
      {
        id: 'org-keys-batch',
        to: '/org/keys-batch',
        label: 'Bulk Key Issuing',
        icon: KeySquare,
        requires: 'tenantAdmin',
      },
      {
        id: 'org-billing',
        to: '/org/billing',
        label: 'Billing & Reconciliation',
        icon: Receipt,
        requires: 'orgLead',
      },
      {
        id: 'org-policy',
        to: '/org/policy',
        label: 'Data & Security',
        icon: ShieldCheck,
        requires: 'tenantAdmin',
      },
    ],
  },
  {
    id: 'ops',
    label: 'Platform Operations',
    items: [
      {
        id: 'ops-channels',
        to: '/ops/channels',
        label: 'Channels & Account Pools',
        icon: Radio,
        requires: 'platformStaff',
      },
      {
        id: 'ops-overview',
        to: '/ops/overview',
        label: 'Operations Overview',
        icon: BarChart3,
        requires: 'platformStaff',
      },
      {
        id: 'ops-rules',
        to: '/ops/rules',
        label: 'Platform Rules & Templates',
        icon: SlidersHorizontal,
        requires: 'platformStaff',
      },
    ],
  },
]

/** Groups with at least one entry the caller may see, entries filtered. */
export function visibleNavGroups(
  access: UserAccess,
  groups: readonly NavGroup[] = NAV_GROUPS
): NavGroup[] {
  return groups
    .map((g) => ({
      ...g,
      items: g.items.filter((i) => canAccess(i.requires, access)),
    }))
    .filter((g) => g.items.length > 0)
}
