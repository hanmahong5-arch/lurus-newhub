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

type V2UserListData = {
  users?: AdminUser[] | null
  total: number
  page: number
  page_size: number
}

/**
 * GET /api/v2/admin/users — keyword/group filter + pagination.
 * Platform root only (same gate as the handler).
 */
export async function listUsers(params: UserListParams): Promise<UserListPage> {
  const query: Record<string, string | number> = {
    page: params.page,
    page_size: params.pageSize,
  }
  const keyword = params.keyword.trim()
  if (keyword) query.keyword = keyword
  if (params.group) query.group = params.group
  const data = await api.get<V2UserListData>('/api/v2/admin/users', {
    params: query,
  })
  return {
    items: data.users ?? [],
    total: data.total,
    page: data.page,
    page_size: data.page_size,
  }
}

export function getUser(id: number): Promise<AdminUser> {
  return api.get<AdminUser>(`/api/v2/admin/users/${id}`)
}

/** Group names that exist in the group ratio table. */
export const groupsQueryOptions = queryOptions({
  queryKey: groupsQueryKey,
  queryFn: async () => (await api.get<string[]>('/api/v2/admin/groups')) ?? [],
  staleTime: 5 * 60_000,
})

/**
 * PUT /api/v2/admin/users/:id — role/status/quota/group/display_name/remark.
 * Reads current row first so quota edits are not racing a stale screen value.
 */
export async function updateUser(
  id: number,
  build: (current: AdminUser) => UserEditPatch
): Promise<AdminUser> {
  const current = await getUser(id)
  const patch = build(current)
  const next: AdminUser = { ...current, ...patch }
  const data = await api.put<AdminUser>(`/api/v2/admin/users/${id}`, {
    display_name: next.display_name,
    group: next.group,
    quota: next.quota,
    remark: next.remark ?? '',
    role: next.role,
    status: next.status,
  })
  return data ?? next
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
