import { queryOptions } from '@tanstack/react-query'

import { api, tenantApi } from '@/lib/api'

import { probeFromSummary } from './derive'
import type {
  HealthSummaryBody,
  ModelState,
  ProbeResult,
  PublicModelStatus,
  UsageSummary,
} from './types'

export async function fetchUsageSummary(): Promise<UsageSummary> {
  const body = await tenantApi.get<Partial<UsageSummary> | null>(
    '/channels/usage-summary'
  )
  return {
    window: body?.window ?? '30d',
    since: body?.since ?? 0,
    channels: Array.isArray(body?.channels) ? body.channels : [],
    truncated: body?.truncated === true,
  }
}

const MODEL_STATES = new Set<string>(['operational', 'degraded', 'down'])

export async function fetchPublicModelStatus(): Promise<PublicModelStatus> {
  const body = await api.get<Partial<PublicModelStatus> | null>(
    '/api/v2/public/model-status',
    { skipAuthRedirect: true }
  )
  const models = Array.isArray(body?.models) ? body.models : []
  return {
    models: models.filter(
      (m) =>
        typeof m?.model === 'string' && MODEL_STATES.has(m.status as ModelState)
    ),
    updated_at: body?.updated_at ?? 0,
  }
}

/**
 * One request: the server rolls up every channel's routable verdict, plan end
 * and fullest plan window (`GET /channels/health-summary`). A failed read
 * rejects as a whole; it is never reported as "no problems".
 */
export async function fetchProbes(): Promise<ProbeResult> {
  const body = await tenantApi.get<HealthSummaryBody | null>(
    '/channels/health-summary'
  )
  const rows = Array.isArray(body?.channels) ? body.channels : []
  return {
    probes: rows.map(probeFromSummary),
    truncated: body?.truncated === true,
  }
}

export const usageSummaryQuery = queryOptions({
  queryKey: ['ops-overview', 'usage-summary'],
  queryFn: fetchUsageSummary,
  retry: false,
})

export const modelStatusQuery = queryOptions({
  queryKey: ['ops-overview', 'model-status'],
  queryFn: fetchPublicModelStatus,
  retry: false,
})

export const probesQuery = queryOptions({
  queryKey: ['ops-overview', 'probes'],
  queryFn: fetchProbes,
  retry: false,
  staleTime: 30_000,
})
