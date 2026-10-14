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
import { useQuery } from '@tanstack/react-query'

import { ROLE } from '@/lib/auth'
import { currentUserQueryOptions } from '@/lib/user'

export interface Viewer {
  id: number | null
  role: number
  isRoot: boolean
}

/**
 * Who is looking at the table. Platform operators (role 100) hold the actions
 * behind /api/v2/admin/* (delete a user, force-disable two-factor). UI
 * hygiene only; the server enforces the same gates.
 */
export function useViewer(): Viewer {
  const { data } = useQuery(currentUserQueryOptions)
  const role = data?.role ?? 0
  return { id: data?.id ?? null, role, isRoot: role >= ROLE.root }
}

/** Server rule (CheckPermission): only root may act on an equal or higher role. */
export function canManageRole(viewer: Viewer, targetRole: number): boolean {
  return viewer.isRoot || targetRole < viewer.role
}
