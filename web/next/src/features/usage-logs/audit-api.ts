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

import { api, http, tenantApi, tenantUrl, toApiError } from '@/lib/api'

import {
  AUDIT_PAGE_SIZE,
  auditParams,
  joinCsvPages,
  mapChainReport,
  mapPlatformAuditPage,
  mapTenantAuditPage,
  type AuditFilters,
  type AuditPage,
  type AuditScope,
  type ChainReport,
} from './lib/audit'

const ADMIN_AUDIT = '/api/v2/admin/audit'

export function auditQueryOptions(
  scope: AuditScope,
  filters: AuditFilters,
  page: number,
  pageSize: number = AUDIT_PAGE_SIZE
) {
  return queryOptions<AuditPage>({
    queryKey: ['usage-logs', 'audit', scope, filters, page, pageSize],
    queryFn: async () => {
      const params = auditParams(filters, scope)
      if (scope === 'tenant') {
        return mapTenantAuditPage(
          await tenantApi.get('/audit', {
            params: { ...params, page, page_size: pageSize },
          })
        )
      }
      return mapPlatformAuditPage(
        await api.get(`${ADMIN_AUDIT}/events`, {
          params: { ...params, page, per_page: pageSize },
        })
      )
    },
    retry: false,
  })
}

/** The canonical action taxonomy (platform feed's filter menu). */
export const auditActionsQueryOptions = queryOptions<string[]>({
  queryKey: ['usage-logs', 'audit', 'actions'],
  queryFn: async () => {
    const raw = await api.get<{ actions?: unknown }>(`${ADMIN_AUDIT}/actions`)
    return Array.isArray(raw?.actions)
      ? raw.actions.filter((a): a is string => typeof a === 'string')
      : []
  },
  staleTime: 5 * 60_000,
  retry: false,
})

/** Pages fetched per export before stopping (1000 rows each). */
export const EXPORT_MAX_PAGES = 100

export interface CsvExport {
  csv: string
  /** More rows exist than the page cap allowed. */
  truncated: boolean
}

/**
 * Both export routes return one cursor page per call (`X-Next-Cursor`);
 * follow the cursor and stitch the pages into one file.
 */
export async function exportAuditCsv(
  scope: AuditScope,
  filters: AuditFilters,
  maxPages = EXPORT_MAX_PAGES
): Promise<CsvExport> {
  const url =
    scope === 'tenant'
      ? tenantUrl('/audit/export.csv')
      : `${ADMIN_AUDIT}/export`
  const base: Record<string, string | number> = {
    ...auditParams(filters, scope),
    ...(scope === 'platform' ? { format: 'csv' } : {}),
  }
  const pages: string[] = []
  let cursor = ''
  for (let i = 0; i < maxPages; i++) {
    let res
    try {
      res = await http.get<string>(url, {
        params: { ...base, ...(cursor ? { cursor } : {}) },
        responseType: 'text',
        transformResponse: (d: unknown) => d,
      })
    } catch (e) {
      throw toApiError(e)
    }
    pages.push(typeof res.data === 'string' ? res.data : '')
    const next = res.headers?.['x-next-cursor']
    cursor = typeof next === 'string' ? next : ''
    if (cursor === '') return { csv: joinCsvPages(pages), truncated: false }
  }
  return { csv: joinCsvPages(pages), truncated: true }
}

export interface ChainVerifyRequest {
  tenantId: string
  afterId: number
  start: number | undefined
  end: number | undefined
}

/** Recompute the tamper-evidence hash chain over one id-ordered window. */
export async function verifyAuditChain(
  req: ChainVerifyRequest
): Promise<ChainReport> {
  const params: Record<string, string | number> = {}
  if (req.tenantId.trim()) params.tenant_id = req.tenantId.trim()
  if (req.afterId > 0) params.after_id = req.afterId
  if (req.start !== undefined) params.start_time = req.start
  if (req.end !== undefined) params.end_time = req.end
  return mapChainReport(
    await api.get(`${ADMIN_AUDIT}/chain-verify`, { params })
  )
}
