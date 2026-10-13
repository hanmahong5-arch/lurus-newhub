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
import { formatChartTime, type TimeGranularity } from '@/lib/time'

import { MAX_RANK_ROWS, MAX_SERIES } from './constants'
import type { QuotaRow } from './types'

export interface Metrics {
  quota: number
  count: number
  tokens: number
}

export interface Bucket extends Metrics {
  /** Bucket start, unix seconds (local calendar boundary). */
  ts: number
  byModel: Map<string, Metrics>
}

export interface ModelTotal extends Metrics {
  model: string
}

export interface Usage {
  buckets: Bucket[]
  /** Largest spend first. */
  models: ModelTotal[]
  totals: Metrics
}

function num(v: unknown): number {
  return typeof v === 'number' && Number.isFinite(v) ? v : 0
}

const zero = (): Metrics => ({ quota: 0, count: 0, tokens: 0 })

/**
 * Start of the local-calendar bucket holding `ts`. /api/data/self/ answers
 * hour-sized rows regardless of `default_time`, so day and week buckets are
 * built here. Weeks start on Monday.
 */
export function bucketStart(ts: number, granularity: TimeGranularity): number {
  const d = new Date(ts * 1000)
  d.setMinutes(0, 0, 0)
  if (granularity !== 'hour') {
    d.setHours(0)
    if (granularity === 'week') {
      d.setDate(d.getDate() - ((d.getDay() + 6) % 7))
    }
  }
  return Math.floor(d.getTime() / 1000)
}

/** Next bucket boundary, DST-safe (calendar arithmetic, not +86400). */
export function nextBucket(ts: number, granularity: TimeGranularity): number {
  const d = new Date(ts * 1000)
  if (granularity === 'hour') d.setHours(d.getHours() + 1)
  else d.setDate(d.getDate() + (granularity === 'week' ? 7 : 1))
  return Math.floor(d.getTime() / 1000)
}

/** Safety valve: a chart never gets more than this many buckets. */
const MAX_BUCKETS = 800

/**
 * Fold per (model, hour) rows into zero-filled buckets over [start, end] and
 * per-model totals. Rows outside the range still count (they add a bucket)
 * so nothing the server returned is silently dropped.
 */
export function aggregateUsage(
  rows: QuotaRow[],
  granularity: TimeGranularity,
  start?: number,
  end?: number
): Usage {
  const map = new Map<number, Bucket>()
  const getBucket = (ts: number): Bucket => {
    let b = map.get(ts)
    if (!b) {
      b = { ...zero(), ts, byModel: new Map() }
      map.set(ts, b)
    }
    return b
  }

  if (start != null && end != null && end >= start) {
    let step = bucketStart(start, granularity)
    for (let i = 0; step <= end && i < MAX_BUCKETS; i++) {
      getBucket(step)
      step = nextBucket(step, granularity)
    }
  }

  const modelTotals = new Map<string, Metrics>()
  const totals = zero()
  for (const row of rows) {
    const ts = num(row.created_at)
    if (!ts) continue
    const model = row.model_name || 'Unknown'
    const m: Metrics = {
      quota: num(row.quota),
      count: num(row.count),
      tokens: num(row.token_used),
    }
    const bucket = getBucket(bucketStart(ts, granularity))
    const cell = bucket.byModel.get(model) ?? zero()
    cell.quota += m.quota
    cell.count += m.count
    cell.tokens += m.tokens
    bucket.byModel.set(model, cell)
    bucket.quota += m.quota
    bucket.count += m.count
    bucket.tokens += m.tokens

    const mt = modelTotals.get(model) ?? zero()
    mt.quota += m.quota
    mt.count += m.count
    mt.tokens += m.tokens
    modelTotals.set(model, mt)

    totals.quota += m.quota
    totals.count += m.count
    totals.tokens += m.tokens
  }

  return {
    buckets: [...map.values()].sort((a, b) => a.ts - b.ts),
    models: [...modelTotals.entries()]
      .map(([model, v]) => ({ model, ...v }))
      .sort((a, b) => b.quota - a.quota || a.model.localeCompare(b.model)),
    totals,
  }
}

/** Requests / tokens per minute over the span; 0 for an empty span. */
export function perMinute(total: number, startSec: number, endSec: number) {
  const minutes = (endSec - startSec) / 60
  if (!Number.isFinite(minutes) || minutes <= 0) return 0
  return total / minutes
}

export interface SeriesDef {
  /** Chart data key: m0, m1, ... (model names are not safe dataKeys). */
  key: string
  /** Model name, or null for the folded remainder. */
  model: string | null
}

export const OTHER_KEY = 'other'

export interface ChartPoint {
  ts: number
  label: string
  [seriesKey: string]: number | string
}

export interface ChartSeries {
  series: SeriesDef[]
  points: ChartPoint[]
}

/**
 * Wide rows for recharts: one point per bucket, one numeric column per kept
 * model plus a folded "other". `rank` picks which metric orders the models.
 */
export function buildChartSeries(
  usage: Usage,
  granularity: TimeGranularity,
  metric: 'quota' | 'count',
  limit = MAX_SERIES
): ChartSeries {
  const ranked = [...usage.models].sort(
    (a, b) => b[metric] - a[metric] || a.model.localeCompare(b.model)
  )
  const kept = ranked.slice(0, limit)
  const hasOther = ranked.length > kept.length

  const series: SeriesDef[] = kept.map((m, i) => ({
    key: `m${i}`,
    model: m.model,
  }))
  const keyOf = new Map(series.map((s) => [s.model as string, s.key]))
  if (hasOther) series.push({ key: OTHER_KEY, model: null })

  const points = usage.buckets.map((b) => {
    const point: ChartPoint = {
      ts: b.ts,
      label: formatChartTime(b.ts, granularity),
    }
    for (const s of series) point[s.key] = 0
    for (const [model, v] of b.byModel) {
      const key = keyOf.get(model) ?? OTHER_KEY
      point[key] = (point[key] as number) + v[metric]
    }
    return point
  })
  return { series, points }
}

export interface RankRow {
  model: string | null
  count: number
}

/** Call-count ranking: top rows, the rest folded into one "other" row. */
export function rankByCount(
  usage: Usage,
  limit = MAX_RANK_ROWS
): RankRow[] {
  const ranked = usage.models
    .filter((m) => m.count > 0)
    .sort((a, b) => b.count - a.count || a.model.localeCompare(b.model))
  const top: RankRow[] = ranked
    .slice(0, limit)
    .map((m) => ({ model: m.model, count: m.count }))
  const rest = ranked.slice(limit).reduce((s, m) => s + m.count, 0)
  if (rest > 0) top.push({ model: null, count: rest })
  return top
}
