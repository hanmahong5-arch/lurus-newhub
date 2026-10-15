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

import {
  OPENROUTER_CHANNEL_TYPE,
  type JobBody,
  type OpenRouterChannel,
  type PoolChannel,
  type PreviewModel,
  type RunResult,
  type SyncCategory,
  type SyncJob,
} from './types'

/*
 * v1 handlers (/api/openrouter-sync/*): HTTP 200 with {success:false,message}
 * on a business failure. The shared `api` helper opens the envelope and throws
 * an ApiError carrying that message, so a failed read is an error everywhere,
 * never an empty list.
 */
const BASE = '/api/openrouter-sync'

export const syncQueryKey = ['openrouter-sync'] as const

export const jobsQueryOptions = queryOptions({
  queryKey: [...syncQueryKey, 'jobs'],
  queryFn: async () => (await api.get<SyncJob[] | null>(`${BASE}/jobs`)) ?? [],
})

export const categoriesQueryOptions = queryOptions({
  queryKey: [...syncQueryKey, 'categories'],
  queryFn: async () =>
    (await api.get<SyncCategory[] | null>(`${BASE}/categories`)) ?? [],
  staleTime: 5 * 60_000,
})

/**
 * OpenRouter channels, the only valid sync targets. The list endpoint answers
 * `{items,...}` (or a bare array on older builds); the type filter is applied
 * on the server and again here so a stale server cannot widen the picker.
 */
export const channelsQueryOptions = queryOptions({
  queryKey: [...syncQueryKey, 'channels'],
  queryFn: async (): Promise<OpenRouterChannel[]> => {
    const data = await api.get<
      { items?: OpenRouterChannel[] | null } | OpenRouterChannel[] | null
    >('/api/channel/', {
      params: { p: 1, page_size: 200, type: OPENROUTER_CHANNEL_TYPE },
    })
    const list = Array.isArray(data) ? data : (data?.items ?? [])
    return list.filter((c) => c.type === OPENROUTER_CHANNEL_TYPE)
  },
  staleTime: 60_000,
})

export const POOL_REFRESH_MS = 10_000

export const poolQueryOptions = queryOptions({
  queryKey: [...syncQueryKey, 'api-pool'],
  queryFn: async () =>
    (await api.get<PoolChannel[] | null>(`${BASE}/api-pool`)) ?? [],
})

export function previewQueryOptions(jobId: number) {
  return queryOptions({
    queryKey: [...syncQueryKey, 'preview', jobId],
    queryFn: async () =>
      (await api.get<PreviewModel[] | null>(`${BASE}/jobs/${jobId}/preview`)) ??
      [],
    // A dry run hits the upstream catalogue: never serve a stale one.
    gcTime: 0,
    staleTime: 0,
  })
}

export function createJob(body: JobBody) {
  return api.post<SyncJob>(`${BASE}/jobs`, body)
}

export function updateJob(id: number, body: JobBody) {
  return api.put<SyncJob>(`${BASE}/jobs/${id}`, body)
}

export function deleteJob(id: number) {
  return api.delete<unknown>(`${BASE}/jobs/${id}`)
}

/** `force` re-baselines the circuit breaker and ignores the schedule. */
export function runJob(id: number, force: boolean) {
  return api.post<RunResult | null>(
    `${BASE}/jobs/${id}/run${force ? '?force=true' : ''}`
  )
}

export function runAllJobs() {
  return api.post<RunResult | null>(`${BASE}/run-all`)
}
