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

import type {
  AdminUser,
  UserEditPatch,
  UserListPage,
  UserListParams,
} from './types'

export const usersQueryKey = ['admin-users'] as const
export const groupsQueryKey = ['admin-user-groups'] as const

/**
 * GET /api/user/ (all) or /api/user/search (keyword and/or group).
 * Platform operators see every tenant, a tenant admin only their own.
 * The server ignores status/role/sort parameters, so none are sent.
 */
export async function listUsers(params: UserListParams): Promise<UserListPage> {
  const searching = params.keyword.trim() !== '' || params.group !== ''
  const page = await api.get<UserListPage>(
    searching ? '/api/user/search' : '/api/user/',
    {
      params: {
        ...(searching
          ? { keyword: params.keyword.trim(), group: params.group }
          : {}),
        p: params.page,
        page_size: params.pageSize,
      },
    }
  )
  return { ...page, items: page.items ?? [] }
}

export function getUser(id: number): Promise<AdminUser> {
  return api.get<AdminUser>(`/api/user/${id}`)
}

/** Group names that exist in the group ratio table. */
export const groupsQueryOptions = queryOptions({
  queryKey: groupsQueryKey,
  queryFn: async () => (await api.get<string[]>('/api/group/')) ?? [],
  staleTime: 5 * 60_000,
})

/**
 * PUT /api/user/ rewrites username, display_name, group, quota and remark from
 * the body on every call (a missing field is saved as empty / zero), and the
 * row on screen may be stale: quota moves with every request the user makes.
 * So every write first reads the user's current row, lets `build` derive the
 * change from it, and sends the complete set back.
 */
export async function updateUser(
  id: number,
  build: (current: AdminUser) => UserEditPatch
): Promise<AdminUser> {
  const current = await getUser(id)
  const patch = build(current)
  const next: AdminUser = { ...current, ...patch }
  await api.put('/api/user/', {
    id: current.id,
    username: current.username,
    display_name: next.display_name,
    group: next.group,
    quota: next.quota,
    remark: next.remark ?? '',
    role: next.role,
    status: next.status,
  })
  return next
}

/** DELETE /api/v2/admin/users/:id - platform operator (root) only. */
export function deleteUser(id: number): Promise<unknown> {
  return api.delete(`/api/v2/admin/users/${id}`)
}

/**
 * POST /api/v2/admin/security/users/:id/totp/force-disable - root only,
 * behind the step-up gate. `notified` says whether the person could be told.
 */
export function forceDisableTotp(
  id: number,
  reason: string
): Promise<{ notified: boolean }> {
  return api.post<{ notified: boolean }>(
    `/api/v2/admin/security/users/${id}/totp/force-disable`,
    { reason }
  )
}
