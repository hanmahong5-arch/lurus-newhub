import { queryOptions } from '@tanstack/react-query'

import { http, tenantApi, tenantUrl, toApiError } from '@/lib/api'

import {
  AUDIT_PAGE_SIZE,
  auditParams,
  joinCsvPages,
  mapAllowlist,
  mapAuditPage,
  mapRetention,
  mapRules,
  mapTokenOptions,
  mapTokenRetention,
  type AuditFilters,
  type Retention,
} from './lib/policy'

export const policyKey = ['org-policy'] as const

/* model allow-list: GET/PUT /api/v2/~/models/allowlist */

export const allowlistQueryOptions = queryOptions({
  queryKey: [...policyKey, 'allowlist'],
  queryFn: async () => mapAllowlist(await tenantApi.get('/models/allowlist')),
  retry: false,
})

export async function saveAllowlist(selected: string[]) {
  return mapAllowlist(
    await tenantApi.put('/models/allowlist', { selected_models: selected })
  )
}

/** Routable models: candidate pool when the platform sets no ceiling. */
export const routableModelIdsQueryOptions = queryOptions({
  queryKey: [...policyKey, 'routable'],
  queryFn: async () => {
    const raw = await tenantApi.get<{ items?: unknown }>('/models/routable')
    const items = Array.isArray(raw?.items) ? raw.items : []
    return items
      .map((m) =>
        typeof m === 'object' && m !== null
          ? (m as { id?: unknown }).id
          : undefined
      )
      .filter((id): id is string => typeof id === 'string' && id !== '')
  },
  staleTime: 60_000,
  retry: false,
})

/* retention: /api/v2/~/data-policy/retention */

export const retentionQueryOptions = queryOptions({
  queryKey: [...policyKey, 'retention'],
  queryFn: async () =>
    mapRetention(await tenantApi.get('/data-policy/retention')),
  retry: false,
})

export async function saveRetention(mode: Retention) {
  return mapRetention(
    await tenantApi.put('/data-policy/retention', { content_retention: mode })
  )
}

export async function saveTokenRetention(tokenId: number, mode: Retention) {
  return mapTokenRetention(
    await tenantApi.put(`/data-policy/tokens/${tokenId}/retention`, {
      content_retention: mode,
    })
  )
}

/** Token picker source (server caps size at 100). */
export const TOKEN_PICK_SIZE = 100
export const tokenOptionsQueryOptions = queryOptions({
  queryKey: [...policyKey, 'tokens'],
  queryFn: async () =>
    mapTokenOptions(
      await tenantApi.get('/tokens', { params: { p: 1, size: TOKEN_PICK_SIZE } })
    ),
  retry: false,
})

/* content rules: /api/v2/~/data-policy/rules */

export const rulesQueryOptions = queryOptions({
  queryKey: [...policyKey, 'rules'],
  queryFn: async () => mapRules(await tenantApi.get('/data-policy/rules')),
  retry: false,
})

export async function createRule(body: Record<string, unknown>) {
  await tenantApi.post('/data-policy/rules', body)
}

export async function updateRule(id: number, body: Record<string, unknown>) {
  await tenantApi.put(`/data-policy/rules/${id}`, body)
}

export async function deleteRule(id: number) {
  await tenantApi.delete(`/data-policy/rules/${id}`)
}

/* audit: /api/v2/~/audit */

export function auditQueryOptions(filters: AuditFilters, page: number) {
  return queryOptions({
    queryKey: [...policyKey, 'audit', filters, page],
    queryFn: async () =>
      mapAuditPage(
        await tenantApi.get('/audit', {
          params: {
            ...auditParams(filters),
            page,
            page_size: AUDIT_PAGE_SIZE,
          },
        })
      ),
    retry: false,
  })
}

/** Pages fetched per export before stopping (1000 rows each). */
export const EXPORT_MAX_PAGES = 100

export interface CsvExport {
  csv: string
  /** More rows exist than the page cap allowed. */
  truncated: boolean
}

/**
 * The export endpoint returns one cursor page per call (`X-Next-Cursor`);
 * follow the cursor and stitch the pages into one file.
 */
export async function exportAuditCsv(
  filters: AuditFilters,
  maxPages = EXPORT_MAX_PAGES
): Promise<CsvExport> {
  const pages: string[] = []
  let cursor = ''
  for (let i = 0; i < maxPages; i++) {
    let res
    try {
      res = await http.get<string>(tenantUrl('/audit/export.csv'), {
        params: { ...auditParams(filters), ...(cursor ? { cursor } : {}) },
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
