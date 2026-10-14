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
/** A row of GET /api/user/ and GET /api/user/search (repo.User, v1). */
export interface AdminUser {
  id: number
  tenant_id?: string
  username: string
  display_name: string
  email?: string
  /** 1 = member, 5 = subscriber, 10 = admin, 100 = root. */
  role: number
  /** 1 = enabled, 2 = disabled. */
  status: number
  quota: number
  used_quota: number
  request_count: number
  group: string
  remark?: string
  /** Soft-delete marker; the list is unscoped so deleted rows come back. */
  DeletedAt?: string | null
}

/** `pageInfo` of the v1 list handlers. */
export interface UserListPage {
  items: AdminUser[] | null
  total: number
  page: number
  page_size: number
}

export interface UserListParams {
  page: number
  pageSize: number
  keyword: string
  group: string
}

/** Fields an admin can change from the console. */
export interface UserEditPatch {
  display_name?: string
  group?: string
  remark?: string
  quota?: number
  role?: number
  status?: number
}

export type QuotaAdjustMode = 'add' | 'subtract' | 'override'

export type UsersDialog =
  | { kind: 'edit'; user: AdminUser }
  | { kind: 'delete'; user: AdminUser }
  | { kind: 'force-disable-2fa'; user: AdminUser }
  | null
