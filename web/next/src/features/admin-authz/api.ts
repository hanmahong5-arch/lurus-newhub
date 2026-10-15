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
import { api } from '@/lib/api'

import type { AuthzCatalog, CreateGrantInput, Grant } from './types'

export const authzKeys = {
  catalog: ['admin-authz', 'catalog'] as const,
  grants: ['admin-authz', 'grants'] as const,
}

const BASE = '/api/v2/admin/authz'

export async function fetchCatalog(): Promise<AuthzCatalog> {
  const data = await api.get<Partial<AuthzCatalog> | null>(`${BASE}/catalog`)
  return {
    resources: data?.resources ?? [],
    roles: data?.roles ?? [],
  }
}

export async function fetchGrants(): Promise<Grant[]> {
  const rows = await api.get<Grant[] | null>(`${BASE}/grants`)
  return rows ?? []
}

/**
 * Grants are GLOBAL this cycle: `tenant_id` is always null (the server answers
 * a non-null one with 400 GRANT_INVALID). `ttl_seconds` is omitted entirely
 * for a permanent grant, never sent as null; a parsed 0 IS sent so the server
 * refuses it instead of silently minting a permanent grant.
 */
export function createGrant(input: CreateGrantInput): Promise<unknown> {
  return api.post(`${BASE}/grants`, {
    user_id: input.userId,
    resource: input.resource,
    action: input.action,
    tenant_id: null,
    ...(input.ttlSeconds === undefined
      ? {}
      : { ttl_seconds: input.ttlSeconds }),
  })
}

export function revokeGrant(id: number): Promise<unknown> {
  return api.delete(`${BASE}/grants/${id}`)
}
