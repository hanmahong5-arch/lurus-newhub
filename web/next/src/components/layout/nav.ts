import {
  Activity,
  BadgeDollarSign,
  Building2,
  Gauge,
  Layers,
  LockKeyhole,
  MessagesSquare,
  PiggyBank,
  RefreshCw,
  Stethoscope,
  Timer,
  Workflow,
  BarChart3,
  Boxes,
  CircleUserRound,
  FlaskConical,
  ListChecks,
  Settings,
  Ticket,
  Trophy,
  UserCog,
  Wallet,
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
  /** Route path (the console owns the site root; assets stay under /next/). */
  to:
    | '/dashboard'
    | '/playground'
    | '/keys'
    | '/usage-logs'
    | '/usage-logs/audit'
    | '/tasks'
    | '/models'
    | '/rankings'
    | '/wallet'
    | '/account'
    | '/admin/redemption-codes'
    | '/admin/users'
    | '/admin/settings'
    | '/chat'
    | '/org/flows'
    | '/org/pricing'
    | '/admin/tenants'
    | '/admin/authz'
    | '/admin/model-limits'
    | '/admin/model-pools'
    | '/admin/model-performance'
    | '/admin/cost-intelligence'
    | '/admin/system-tasks'
    | '/admin/diagnostics'
    | '/admin/openrouter-sync'
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
  id: 'workspace' | 'personal' | 'org' | 'ops' | 'admin'
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
        id: 'playground',
        to: '/playground',
        label: 'Playground',
        icon: FlaskConical,
        requires: 'everyone',
      },
      {
        id: 'chat',
        to: '/chat',
        label: 'Chat',
        icon: MessagesSquare,
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
        id: 'tasks',
        to: '/tasks',
        label: 'Async Tasks',
        icon: ListChecks,
        requires: 'everyone',
      },
      {
        id: 'models',
        to: '/models',
        label: 'Models',
        icon: Boxes,
        requires: 'everyone',
      },
      {
        id: 'rankings',
        to: '/rankings',
        label: 'Rankings',
        icon: Trophy,
        requires: 'tenantAdmin',
      },
    ],
  },
  {
    id: 'personal',
    label: 'Personal',
    items: [
      {
        id: 'wallet',
        to: '/wallet',
        label: 'Wallet',
        icon: Wallet,
        requires: 'everyone',
      },
      {
        id: 'account',
        to: '/account',
        label: 'Account & Security',
        icon: CircleUserRound,
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
      {
        id: 'org-audit',
        to: '/usage-logs/audit',
        label: 'Audit Log',
        icon: ScrollText,
        requires: 'tenantAdmin',
      },
      {
        id: 'redemption-codes',
        to: '/admin/redemption-codes',
        label: 'Redemption Codes',
        icon: Ticket,
        requires: 'tenantAdmin',
      },
      {
        id: 'org-flows',
        to: '/org/flows',
        label: 'Setup Wizard',
        icon: Workflow,
        requires: 'tenantAdmin',
      },
      {
        id: 'org-pricing',
        to: '/org/pricing',
        label: 'Pricing Versions',
        icon: BadgeDollarSign,
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
      {
        id: 'admin-model-performance',
        to: '/admin/model-performance',
        label: 'Model Performance',
        icon: Activity,
        requires: 'platformRoot',
      },
      {
        id: 'admin-cost',
        to: '/admin/cost-intelligence',
        label: 'Cost Intelligence',
        icon: PiggyBank,
        requires: 'platformRoot',
      },
      {
        id: 'admin-system-tasks',
        to: '/admin/system-tasks',
        label: 'System Tasks',
        icon: Timer,
        requires: 'platformRoot',
      },
      {
        id: 'admin-diagnostics',
        to: '/admin/diagnostics',
        label: 'Diagnostics',
        icon: Stethoscope,
        requires: 'platformRoot',
      },
      {
        id: 'admin-openrouter-sync',
        to: '/admin/openrouter-sync',
        label: 'OpenRouter Sync',
        icon: RefreshCw,
        requires: 'platformRoot',
      },
    ],
  },
  {
    id: 'admin',
    label: 'Platform Administration',
    items: [
      {
        id: 'admin-tenants',
        to: '/admin/tenants',
        label: 'Tenants',
        icon: Building2,
        requires: 'platformRoot',
      },
      {
        id: 'admin-users',
        to: '/admin/users',
        label: 'Users',
        icon: UserCog,
        requires: 'platformStaff',
      },
      {
        id: 'admin-authz',
        to: '/admin/authz',
        label: 'Authorization',
        icon: LockKeyhole,
        requires: 'platformRoot',
      },
      {
        id: 'admin-model-limits',
        to: '/admin/model-limits',
        label: 'Model Rate Limits',
        icon: Gauge,
        requires: 'platformRoot',
      },
      {
        id: 'admin-model-pools',
        to: '/admin/model-pools',
        label: 'Model Pools',
        icon: Layers,
        requires: 'platformRoot',
      },
      {
        id: 'admin-settings',
        to: '/admin/settings',
        label: 'System Settings',
        icon: Settings,
        requires: 'platformRoot',
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
