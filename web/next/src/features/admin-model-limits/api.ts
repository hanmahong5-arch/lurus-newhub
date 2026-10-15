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
  Allowlist,
  ModelLimit,
  ModelLimitBody,
  TenantOption,
} from './types'

/**
 * The bootstrap tenant is always addressable even without a tenants row (the
 * backend carves it out); single-tenant installs set per-model caps here first.
 */
export const DEFAULT_TENANT = 'default'

export const modelLimitsQueryKey = ['admin-model-limits'] as const

const TENANT_PAGE_SIZE = 100
/** Safety stop for the tenant pager: 50 pages of 100 is far past any install. */
const TENANT_MAX_PAGES = 50

function tenantBase(tenantId: string): string {
  return `/api/v2/admin/tenants/${encodeURIComponent(tenantId)}`
}

interface TenantsPayload {
  tenants?: { id: string; name?: string }[]
  items?: { id: string; name?: string }[]
  total?: number
}

/**
 * Every tenant, paged through. `default` is always offered first, whether or
 * not it has a row.
 */
export async function fetchTenantOptions(): Promise<TenantOption[]> {
  const out: TenantOption[] = []
  for (let page = 1; page <= TENANT_MAX_PAGES; page++) {
    const data = await api.get<TenantsPayload>('/api/v2/admin/tenants', {
      params: { page, page_size: TENANT_PAGE_SIZE },
    })
    const rows = data?.tenants ?? data?.items ?? []
    for (const row of rows) out.push({ id: row.id, name: row.name || row.id })
    const total = data?.total ?? 0
    if (rows.length < TENANT_PAGE_SIZE || (total > 0 && out.length >= total)) {
      break
    }
  }
  if (!out.some((t) => t.id === DEFAULT_TENANT)) {
    out.unshift({ id: DEFAULT_TENANT, name: DEFAULT_TENANT })
  }
  return out
}

export const tenantOptionsQueryOptions = queryOptions({
  queryKey: [...modelLimitsQueryKey, 'tenants'],
  queryFn: fetchTenantOptions,
  staleTime: 60_000,
})

export function limitsQueryOptions(tenantId: string) {
  return queryOptions({
    queryKey: [...modelLimitsQueryKey, 'limits', tenantId],
    queryFn: async () =>
      (await api.get<ModelLimit[] | null>(
        `${tenantBase(tenantId)}/model-limits`
      )) ?? [],
  })
}

export function allowlistQueryOptions(tenantId: string) {
  return queryOptions({
    queryKey: [...modelLimitsQueryKey, 'allowlist', tenantId],
    queryFn: async (): Promise<Allowlist> => {
      const data = await api.get<Partial<Allowlist> | null>(
        `${tenantBase(tenantId)}/model-allowlist`
      )
      return {
        configured: data?.configured === true,
        allowed_models: Array.isArray(data?.allowed_models)
          ? data.allowed_models
          : [],
        mode: data?.mode || 'observe',
      }
    },
    // The editor seeds its draft from this read; a background refetch (window
    // focus) must not be able to swap the list under an operator mid-edit.
    staleTime: Infinity,
    refetchOnWindowFocus: false,
  })
}

export async function saveModelLimit(tenantId: string, body: ModelLimitBody) {
  await api.put(`${tenantBase(tenantId)}/model-limits`, body)
}

/** The model rides in `?model=` because names contain `/` (vendor/model). */
export async function deleteModelLimit(tenantId: string, model: string) {
  await api.delete(`${tenantBase(tenantId)}/model-limits`, {
    params: { model },
  })
}

export async function saveAllowlist(tenantId: string, models: string[]) {
  await api.put(`${tenantBase(tenantId)}/model-allowlist`, {
    allowed_models: models,
  })
}

export async function clearAllowlist(tenantId: string) {
  await api.delete(`${tenantBase(tenantId)}/model-allowlist`)
}
