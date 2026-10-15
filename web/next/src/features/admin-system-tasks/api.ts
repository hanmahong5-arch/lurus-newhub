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
import { queryOptions } from '@tanstack/react-query'

import { api } from '@/lib/api'

export type SystemTaskState = 'ok' | 'overdue' | 'standby'

export interface SystemTask {
  name: string
  interval_seconds: number
  leader_only: boolean
  /** Unix seconds; 0 = this replica has never stamped a success. */
  last_success_at: number
  state: SystemTaskState
  /** "follower" | "disabled" when state is standby, otherwise empty. */
  standby_reason?: string
}

export interface SystemTasksSnapshot {
  is_leader: boolean
  pod: string
  tasks: SystemTask[] | null
}

export const systemTasksQueryKey = ['admin-system-tasks'] as const

/**
 * GET /api/v2/admin/system/tasks (root only). A per-pod view: it lists the
 * periodic jobs THIS replica registered, not a cluster-wide aggregate.
 * `skipAuthRedirect` lets the page explain an expired session itself instead
 * of bouncing away mid-read.
 */
export const systemTasksQueryOptions = queryOptions({
  queryKey: systemTasksQueryKey,
  queryFn: () =>
    api.get<SystemTasksSnapshot>('/api/v2/admin/system/tasks', {
      skipAuthRedirect: true,
    }),
  retry: false,
})
