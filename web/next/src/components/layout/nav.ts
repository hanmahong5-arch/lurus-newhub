import {
  Boxes,
  KeyRound,
  LayoutDashboard,
  ScrollText,
  type LucideIcon,
} from 'lucide-react'

import { hasMinRole } from '@/lib/auth'

export interface NavItem {
  id: string
  /** Route path relative to the router basepath (/next). */
  to: '/dashboard' | '/keys' | '/usage-logs' | '/models'
  /** English source string; translated at render time. */
  label: string
  icon: LucideIcon
  /**
   * Minimum user role to show the entry (same scale and meaning as `minRole`
   * in the legacy console's HFShell NAV_SECTIONS: 10 = admin, 100 = root).
   * Absent = every signed-in user. UI hygiene only; the server enforces.
   */
  minRole?: number
}

export const NAV_ITEMS: readonly NavItem[] = [
  {
    id: 'dashboard',
    to: '/dashboard',
    label: 'Dashboard',
    icon: LayoutDashboard,
  },
  { id: 'keys', to: '/keys', label: 'API Keys', icon: KeyRound },
  {
    id: 'usage-logs',
    to: '/usage-logs',
    label: 'Usage Logs',
    icon: ScrollText,
  },
  // Visible to every signed-in user (the legacy console puts it behind
  // minRole 10, but the tenant model list is a member-level read).
  { id: 'models', to: '/models', label: 'Models', icon: Boxes },
]

/** Entries the given role may see. */
export function visibleNavItems(
  role: number | undefined,
  items: readonly NavItem[] = NAV_ITEMS
): NavItem[] {
  return items.filter((item) => !item.minRole || hasMinRole(role, item.minRole))
}
