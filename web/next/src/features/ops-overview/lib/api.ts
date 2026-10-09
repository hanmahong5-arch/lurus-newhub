import { queryOptions } from '@tanstack/react-query'

import { api, tenantApi } from '@/lib/api'

import {
  buildProbe,
  manualDisabledProbe,
  STATUS_MANUALLY_DISABLED,
} from './derive'
import type {
  ChannelDetail,
  ChannelHealth,
  ModelState,
  ProbeFailure,
  ProbeResult,
  PublicModelStatus,
  SummaryChannel,
  UsageSummary,
} from './types'

/** At most this many channels are read one by one (2 requests each). */
export const PROBE_CAP = 300
/** Parallel per-channel reads in flight. */
export const PROBE_CONCURRENCY = 6

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
        typeof m?.model === 'string' &&
        MODEL_STATES.has(m.status as ModelState)
    ),
    updated_at: body?.updated_at ?? 0,
  }
}

/** Run `fn` over `items` with at most `limit` in flight; keeps input order. */
export async function mapLimited<T, R>(
  items: T[],
  limit: number,
  fn: (item: T) => Promise<R>
): Promise<R[]> {
  const out = Array.from<R>({ length: items.length })
  let next = 0
  const worker = async () => {
    while (next < items.length) {
      const i = next++
      out[i] = await fn(items[i])
    }
  }
  await Promise.all(
    Array.from({ length: Math.min(limit, items.length) }, worker)
  )
  return out
}

/**
 * No server-side health roll-up exists, so each channel is read
 * (`/channels/:id/health` + `/channels/:id`) with bounded concurrency. A
 * failed channel read is reported, never dropped.
 */
export async function fetchProbes(
  channels: SummaryChannel[]
): Promise<ProbeResult> {
  const probes: ProbeResult['probes'] = []
  const failures: ProbeFailure[] = []
  const live: SummaryChannel[] = []
  for (const c of channels) {
    if (c.status === STATUS_MANUALLY_DISABLED) probes.push(manualDisabledProbe(c))
    else live.push(c)
  }
  const target = live.slice(0, PROBE_CAP)
  const results = await mapLimited(target, PROBE_CONCURRENCY, async (c) => {
    try {
      const [health, detail] = await Promise.all([
        tenantApi.get<ChannelHealth>(`/channels/${c.channel_id}/health`),
        tenantApi.get<ChannelDetail>(`/channels/${c.channel_id}`),
      ])
      return buildProbe(c, health, detail)
    } catch (e) {
      const failure: ProbeFailure = {
        channelId: c.channel_id,
        name: c.name,
        message: e instanceof Error ? e.message : 'Request failed',
      }
      return failure
    }
  })
  for (const r of results) {
    if ('message' in r) failures.push(r)
    else probes.push(r)
  }
  return { probes, failures, skipped: live.length - target.length }
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

export const probesQuery = (channels: SummaryChannel[]) =>
  queryOptions({
    queryKey: ['ops-overview', 'probes', channels.map((c) => c.channel_id)],
    queryFn: () => fetchProbes(channels),
    retry: false,
    staleTime: 60_000,
  })
