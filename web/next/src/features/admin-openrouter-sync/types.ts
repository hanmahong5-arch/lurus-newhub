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

/** Channel type id of an OpenRouter channel. */
export const OPENROUTER_CHANNEL_TYPE = 20

export const SCHEDULES = ['daily', 'weekly', 'manual'] as const
export type Schedule = (typeof SCHEDULES)[number]

export const TOP_N_MAX = 1000

/** One row of GET /api/openrouter-sync/jobs. */
export interface SyncJob {
  id: number
  name: string
  target_channel_id: number
  /** A JSON-encoded string array (the column is text), or already an array. */
  categories: string | string[] | null
  top_n: number
  schedule: string
  enabled: boolean
  /** ISO timestamp; null before the first run. */
  last_run_at: string | null
  last_error?: string
}

export interface SyncCategory {
  key: string
  /** Server-side label (not localised); the page maps known keys itself. */
  label: string
}

export interface OpenRouterChannel {
  id: number
  name: string
  type?: number
}

/** Result of running one job, or every enabled job. */
export interface RunResult {
  fetched_total?: number
  free_total?: number
  managed_new_count?: number
  added?: string[] | null
  removed?: string[] | null
  circuit_breaker_on?: boolean
  skipped?: boolean
  skip_reason?: string
}

export interface PreviewModel {
  id: string
  name: string
  /** Unix seconds. */
  created: number
}

/** Body of POST /jobs and PUT /jobs/:id. */
export interface JobBody {
  name: string
  target_channel_id: number
  categories: string[]
  top_n: number
  schedule: string
  enabled: boolean
}

export type PoolKeyStatus = 'enabled' | 'cooling' | 'permanent_disabled'
export type PoolChannelStatus =
  | 'enabled'
  | 'auto_disabled'
  | 'manually_disabled'
  | 'unknown'

export interface PoolKey {
  index: number
  /** Masked: the server never returns more than a short prefix. */
  key_prefix: string
  status: string
  cooldown_until?: number
  cooldown_seconds_remaining?: number
  disable_reason?: string
  last_disabled_at?: number
}

export interface PoolChannel {
  channel_id: number
  channel_name?: string
  status: string
  key_count: number
  enabled_count: number
  cooling_count: number
  permanent_disabled_count: number
  keys: PoolKey[] | null
}
