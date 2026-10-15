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

import { rangeSeconds } from './lib/derive'
import type { PerfReport, RangeId, TenantOption } from './types'

export const PERF_PATH = '/api/v2/admin/analytics/model-performance'
export const EXPORT_PATH = '/api/v2/admin/logs/export'
const TENANTS_PATH = '/api/v2/admin/tenants?page_size=100'
/** Bound on tenant paging so a runaway total cannot loop forever. */
const MAX_TENANT_PAGES = 5

export async function fetchPerformance(
  range: RangeId,
  tenantId: string
): Promise<PerfReport> {
  const end = Math.floor(Date.now() / 1000)
  const start = end - rangeSeconds(range)
  const params = new URLSearchParams({
    start: String(start),
    end: String(end),
  })
  if (tenantId) params.set('tenant_id', tenantId)
  const body = await api.get<Partial<PerfReport> | null>(
    `${PERF_PATH}?${params}`,
    { skipAuthRedirect: true }
  )
  return {
    models: Array.isArray(body?.models) ? body.models : [],
    start_time: body?.start_time ?? start,
    end_time: body?.end_time ?? end,
  }
}

export const performanceQuery = (range: RangeId, tenantId: string) =>
  queryOptions({
    queryKey: ['admin-model-performance', 'report', range, tenantId],
    queryFn: () => fetchPerformance(range, tenantId),
    retry: false,
    meta: { errorToast: false },
  })

interface TenantPage {
  tenants?: TenantOption[]
  total?: number
}

/**
 * Tenant filter options. A failure is non-fatal: the filter then offers only
 * "all tenants", and whatever was read before the failure is kept.
 */
export async function fetchTenantOptions(): Promise<TenantOption[]> {
  const out: TenantOption[] = []
  for (let page = 1; page <= MAX_TENANT_PAGES; page++) {
    let body: TenantPage | null
    try {
      body = await api.get<TenantPage | null>(
        page === 1 ? TENANTS_PATH : `${TENANTS_PATH}&page=${page}`,
        { skipAuthRedirect: true }
      )
    } catch {
      break
    }
    const rows = Array.isArray(body?.tenants) ? body.tenants : []
    out.push(...rows)
    if (rows.length === 0 || out.length >= (body?.total ?? 0)) break
  }
  return out
}

export const tenantOptionsQuery = queryOptions({
  queryKey: ['admin-model-performance', 'tenants'],
  queryFn: fetchTenantOptions,
  retry: false,
  staleTime: 60_000,
  meta: { errorToast: false },
})

/**
 * Same-origin, cookie-authenticated CSV of the usage logs behind the table, for
 * the exact window the server applied and the same tenant filter.
 */
export function exportUrl(
  window_: { start_time: number; end_time: number },
  tenantId: string
): string {
  const params = new URLSearchParams({
    format: 'csv',
    start_time: String(window_.start_time),
    end_time: String(window_.end_time),
  })
  if (tenantId) params.set('tenant_id', tenantId)
  return `${EXPORT_PATH}?${params}`
}
