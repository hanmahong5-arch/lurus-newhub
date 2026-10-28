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
import dayjs from '@/lib/dayjs'

import type { UsageMetric } from '@/features/dashboard/lib/series'

/** `<input type="datetime-local">` value format used by usage-logs filters. */
export const LOG_DATETIME_FORMAT = 'YYYY-MM-DDTHH:mm'

export type ActivityTab = 'overview' | 'explore'
export type ActivityGranularity = 'hour' | 'day' | 'week'

/** Rolling windows (capped under data-export max span, same as dashboard). */
export const ACTIVITY_DAY_PRESETS = [7, 14, 29] as const
export type ActivityDays = (typeof ACTIVITY_DAY_PRESETS)[number]

export interface ActivitySearch {
  tab?: ActivityTab
  days?: ActivityDays
  metric?: UsageMetric
  granularity?: ActivityGranularity
}

export interface UsageLogsSearch {
  model?: string
  token?: string
  start?: string
  end?: string
  type?: number
}

function asString(v: unknown): string {
  return typeof v === 'string' ? v.trim() : ''
}

function isDays(v: unknown): v is ActivityDays {
  return ACTIVITY_DAY_PRESETS.some((d) => d === Number(v))
}

function isMetric(v: unknown): v is UsageMetric {
  return v === 'quota' || v === 'count' || v === 'tokens'
}

function isGranularity(v: unknown): v is ActivityGranularity {
  return v === 'hour' || v === 'day' || v === 'week'
}

function isDatetimeLocal(v: string): boolean {
  if (!v) return false
  // Accept the compact picker format and anything Date/dayjs can parse.
  return /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}/.test(v) && dayjs(v).isValid()
}

/** Route `validateSearch` for `/activity`. Unknown values fall back to absent. */
export function validateActivitySearch(
  search: Record<string, unknown>
): ActivitySearch {
  const out: ActivitySearch = {}
  if (search.tab === 'overview' || search.tab === 'explore') out.tab = search.tab
  if (isDays(Number(search.days))) out.days = Number(search.days) as ActivityDays
  if (isMetric(search.metric)) out.metric = search.metric
  if (isGranularity(search.granularity)) out.granularity = search.granularity
  return out
}

/** Route `validateSearch` for `/usage-logs`. */
export function validateUsageLogsSearch(
  search: Record<string, unknown>
): UsageLogsSearch {
  const out: UsageLogsSearch = {}
  const model = asString(search.model)
  const token = asString(search.token)
  const start = asString(search.start)
  const end = asString(search.end)
  if (model) out.model = model
  if (token) out.token = token
  if (start && isDatetimeLocal(start)) out.start = start
  if (end && isDatetimeLocal(end)) out.end = end
  const type = Number(search.type)
  if (Number.isInteger(type) && type > 0) out.type = type
  return out
}

/** Unix seconds → usage-logs datetime-local string. */
export function toLogDatetime(sec: number): string {
  return dayjs.unix(sec).format(LOG_DATETIME_FORMAT)
}

/** Drop undefined keys so TanStack search stays sparse. */
export function sparseActivitySearch(s: ActivitySearch): ActivitySearch {
  const out: ActivitySearch = {}
  if (s.tab && s.tab !== 'overview') out.tab = s.tab
  if (s.days != null && s.days !== 7) out.days = s.days
  if (s.metric && s.metric !== 'quota') out.metric = s.metric
  if (s.granularity && s.granularity !== 'day') out.granularity = s.granularity
  return out
}
