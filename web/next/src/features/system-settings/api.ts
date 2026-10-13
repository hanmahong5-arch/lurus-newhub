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

/**
 * One stored option as GET /api/option/ returns it. Keys that end in Token,
 * Secret, Key or api_key are withheld by the server (handler/option.go), so a
 * secret can never be read back - only replaced.
 */
export type SystemOption = {
  key: string
  value: string
}

export type OptionValue = string | boolean | number

/** GET /api/option/ - every readable option (root only). */
export async function getSystemOptions(): Promise<SystemOption[]> {
  const data = await api.get<SystemOption[] | null>('/api/option/')
  return data ?? []
}

/**
 * PUT /api/option/ {key,value}. The server answers HTTP 200 with
 * `success:false` for some refusals and 400 for others; `api.put` turns both
 * into an ApiError carrying the server's own message.
 */
export async function updateSystemOption(
  key: string,
  value: OptionValue
): Promise<void> {
  await api.put<unknown>('/api/option/', { key, value })
}

/** POST /api/option/rest_model_ratio - reset every model ratio to the defaults. */
export async function resetModelRatios(): Promise<void> {
  await api.post<unknown>('/api/option/rest_model_ratio')
}

/** DELETE /api/log/?target_timestamp= - delete logs older than the stamp. */
export async function deleteLogsBefore(
  targetTimestamp: number
): Promise<number> {
  const count = await api.delete<number>('/api/log/', {
    params: { target_timestamp: targetTimestamp },
  })
  return typeof count === 'number' ? count : 0
}

export type SyncableChannel = {
  id: number
  name: string
  base_url: string
  status: number
}

export type RatioType =
  | 'model_ratio'
  | 'completion_ratio'
  | 'cache_ratio'
  | 'model_price'

/** One model/ratio-type cell of the diff (dto.DifferenceItem). */
export type DifferenceItem = {
  /** Local value; null when this gateway has none. */
  current: number | null
  /** Upstream value per channel name: a number, "same" or null. */
  upstreams: Record<string, number | 'same' | null>
  /** Whether each upstream's value can be trusted for this model. */
  confidence: Record<string, boolean>
}

export type DifferencesMap = Record<
  string,
  Partial<Record<RatioType, DifferenceItem>>
>

export type UpstreamTestResult = {
  name: string
  status: 'success' | 'error'
  error?: string
}

export type UpstreamFetchResult = {
  differences: DifferencesMap | null
  test_results: UpstreamTestResult[] | null
}

export type FetchUpstreamRequest = {
  channel_ids: number[]
  timeout?: number
}

/** GET /api/ratio_sync/channels. */
export async function getSyncableChannels(): Promise<SyncableChannel[]> {
  const data = await api.get<SyncableChannel[] | null>(
    '/api/ratio_sync/channels'
  )
  return data ?? []
}

/** POST /api/ratio_sync/fetch. */
export async function fetchUpstreamRatios(
  request: FetchUpstreamRequest
): Promise<UpstreamFetchResult> {
  return api.post<UpstreamFetchResult>('/api/ratio_sync/fetch', request)
}
