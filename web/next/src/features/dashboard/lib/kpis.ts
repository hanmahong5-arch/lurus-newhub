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
import type { LogRow, QuotaRow } from './types'

export const LOG_TYPE_CONSUME = 2
export const LOG_TYPE_ERROR = 5

/** Realtime KPI window, same as the legacy console (5 minutes). */
export const REALTIME_WINDOW_SECONDS = 300

/**
 * GET /api/v2/~/logs falls back to 20 rows when page_size > 100, so asking
 * for more returns fewer rows, not more.
 */
export const LOG_PAGE_SIZE = 100

/**
 * GET /api/data/self/ answers 200 + success:false when the span exceeds
 * 2,592,000s (usedata.go GetUserQuotaDates); 29 days keeps a day of margin.
 */
export const TREND_WINDOW_SECONDS = 29 * 24 * 60 * 60

export const TREND_DAYS = 14
export const DELTA_DAYS = 7

function num(v: unknown): number {
  return typeof v === 'number' && Number.isFinite(v) ? v : 0
}

/** Nearest-rank percentile over consume logs with a positive latency. */
export function latencyPercentile(
  logs: LogRow[],
  percentile: number
): number | null {
  if (percentile < 0 || percentile > 100) return null
  const samples = logs
    .filter(
      (l) => num(l.type) === LOG_TYPE_CONSUME && num(l.total_latency_ms) > 0
    )
    .map((l) => num(l.total_latency_ms))
    .sort((a, b) => a - b)
  if (samples.length === 0) return null
  if (percentile === 50) {
    const mid = Math.floor(samples.length / 2)
    return samples.length % 2 === 0
      ? Math.round((samples[mid - 1] + samples[mid]) / 2)
      : samples[mid]
  }
  const idx = Math.min(
    samples.length - 1,
    Math.max(0, Math.ceil((percentile / 100) * samples.length) - 1)
  )
  return samples[idx]
}

/** Errors / (consume + errors); null when there is no sample to divide. */
export function errorRate(logs: LogRow[]): number | null {
  let consume = 0
  let error = 0
  for (const l of logs) {
    const t = num(l.type)
    if (t === LOG_TYPE_CONSUME) consume++
    else if (t === LOG_TYPE_ERROR) error++
  }
  const total = consume + error
  return total === 0 ? null : error / total
}

export interface ModelCost {
  model: string
  totalQuota: number
  requestCount: number
}

/** Consume logs grouped by model, largest spend first. */
export function costByModel(logs: LogRow[]): ModelCost[] {
  const by = new Map<string, ModelCost>()
  for (const l of logs) {
    if (num(l.type) !== LOG_TYPE_CONSUME) continue
    const model = l.model_name || ''
    const q = num(l.quota)
    if (!model || q <= 0) continue
    const prev = by.get(model) ?? { model, totalQuota: 0, requestCount: 0 }
    prev.totalQuota += q
    prev.requestCount += 1
    by.set(model, prev)
  }
  return [...by.values()].sort((a, b) => b.totalQuota - a.totalQuota)
}

/**
 * Requests per second over the window. The server's `total` is the true row
 * count (the fetched page is capped at LOG_PAGE_SIZE), so prefer it.
 */
export function qps(
  total: number | null,
  fetched: number,
  windowSeconds = REALTIME_WINDOW_SECONDS
): number {
  const n = total ?? fetched
  return n > 0 ? n / windowSeconds : 0
}

export function formatLatencyMs(ms: number | null): string {
  if (ms == null) return '--'
  return ms < 1000 ? `${ms}ms` : `${(ms / 1000).toFixed(2)}s`
}

export function formatErrorRate(rate: number | null): string {
  if (rate == null || !Number.isFinite(rate)) return '--'
  if (rate === 0) return '0%'
  const pct = rate * 100
  return pct < 0.1 ? '<0.1%' : `${pct.toFixed(1)}%`
}

export function formatQps(v: number): string {
  if (!Number.isFinite(v)) return '--'
  if (v === 0) return '0'
  if (v < 0.01) return '<0.01'
  if (v < 1) return v.toFixed(2)
  if (v < 10) return v.toFixed(1)
  return Math.round(v).toString()
}

/** Local-calendar-day start (unix seconds) of a unix timestamp. */
export function localDayKey(tsSec: number): number {
  const d = new Date(tsSec * 1000)
  d.setHours(0, 0, 0, 0)
  return Math.floor(d.getTime() / 1000)
}

export interface DailyPoint {
  /** Local day start, unix seconds. */
  day: number
  quota: number
  count: number
}

/**
 * Per local day sums for the `days` days ending on `end`'s day, oldest
 * first, zero-filled so the chart never reflows.
 */
export function dailySeries(
  rows: QuotaRow[],
  end: number,
  days: number
): DailyPoint[] {
  if (!end || days < 1) return []
  const cursor = new Date(localDayKey(end) * 1000)
  cursor.setDate(cursor.getDate() - (days - 1))
  const keys: number[] = []
  const byDay = new Map<number, DailyPoint>()
  for (let i = 0; i < days; i++) {
    const key = Math.floor(cursor.getTime() / 1000)
    keys.push(key)
    byDay.set(key, { day: key, quota: 0, count: 0 })
    cursor.setDate(cursor.getDate() + 1)
  }
  for (const row of rows) {
    const ts = num(row.created_at)
    if (!ts) continue
    const p = byDay.get(localDayKey(ts))
    if (!p) continue
    p.quota += num(row.quota)
    p.count += num(row.count)
  }
  return keys.map((k) => byDay.get(k) as DailyPoint)
}

/** Signed % change; null when there is nothing honest to compare against. */
export function percentDelta(
  current: number,
  previous: number
): number | null {
  if (!Number.isFinite(current)) return null
  if (!Number.isFinite(previous) || previous === 0) return null
  return ((current - previous) / previous) * 100
}

/** Last DELTA_DAYS vs the DELTA_DAYS before, from a daily series. */
export function periodDelta(
  series: DailyPoint[],
  field: 'quota' | 'count'
): number | null {
  const sum = (arr: DailyPoint[]) => arr.reduce((s, p) => s + p[field], 0)
  const prev = series.slice(-DELTA_DAYS * 2, -DELTA_DAYS)
  if (prev.length < DELTA_DAYS) return null
  return percentDelta(sum(series.slice(-DELTA_DAYS)), sum(prev))
}

export interface ModelShare {
  model: string
  quota: number
}

/** /api/data/self/ rows grouped by model, largest first, top `limit`. */
export function modelDistribution(rows: QuotaRow[], limit = 8): ModelShare[] {
  const totals = new Map<string, number>()
  for (const row of rows) {
    const model = row.model_name || '--'
    totals.set(model, (totals.get(model) ?? 0) + num(row.quota))
  }
  return [...totals.entries()]
    .map(([model, quota]) => ({ model, quota }))
    .sort((a, b) => b.quota - a.quota)
    .slice(0, limit)
}
