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
import { describe, expect, it } from 'vitest'

import {
  aggregateUsage,
  bucketStart,
  buildChartSeries,
  nextBucket,
  OTHER_KEY,
  perMinute,
  rankByCount,
} from './series'
import type { QuotaRow } from './types'

// 2024-03-13 (a Wednesday), local time.
const at = (h: number, m = 0, day = 13) =>
  Math.floor(new Date(2024, 2, day, h, m, 0).getTime() / 1000)

describe('bucketStart / nextBucket', () => {
  it('hour floors to the local hour, day to local midnight', () => {
    expect(bucketStart(at(10, 42), 'hour')).toBe(at(10))
    expect(bucketStart(at(10, 42), 'day')).toBe(at(0))
  })
  it('week floors to Monday 00:00 local', () => {
    // Wed 13th -> Mon 11th; Sun 17th also -> Mon 11th.
    expect(bucketStart(at(10), 'week')).toBe(at(0, 0, 11))
    expect(bucketStart(at(23, 59, 17), 'week')).toBe(at(0, 0, 11))
    expect(bucketStart(at(0, 0, 18), 'week')).toBe(at(0, 0, 18))
  })
  it('nextBucket steps by calendar unit', () => {
    expect(nextBucket(at(10), 'hour')).toBe(at(11))
    expect(nextBucket(at(0), 'day')).toBe(at(0, 0, 14))
    expect(nextBucket(at(0, 0, 11), 'week')).toBe(at(0, 0, 18))
  })
})

const rows: QuotaRow[] = [
  { model_name: 'model-a', created_at: at(10), quota: 100, count: 2, token_used: 50 },
  { model_name: 'model-a', created_at: at(10, 30), quota: 50, count: 1, token_used: 20 },
  { model_name: 'model-b', created_at: at(12), quota: 400, count: 4, token_used: 90 },
]

describe('aggregateUsage', () => {
  it('sums rows into the hour bucket and zero-fills the gaps', () => {
    const u = aggregateUsage(rows, 'hour', at(10), at(13))
    expect(u.buckets.map((b) => b.ts)).toEqual([at(10), at(11), at(12), at(13)])
    expect(u.buckets[0]).toMatchObject({ quota: 150, count: 3, tokens: 70 })
    expect(u.buckets[1]).toMatchObject({ quota: 0, count: 0, tokens: 0 })
    expect(u.totals).toEqual({ quota: 550, count: 7, tokens: 160 })
  })
  it('re-buckets hourly rows into days', () => {
    const u = aggregateUsage(rows, 'day', at(0), at(23))
    expect(u.buckets).toHaveLength(1)
    expect(u.buckets[0].quota).toBe(550)
  })
  it('orders models by spend, largest first', () => {
    const u = aggregateUsage(rows, 'hour')
    expect(u.models.map((m) => m.model)).toEqual(['model-b', 'model-a'])
    expect(u.models[1]).toMatchObject({ quota: 150, count: 3 })
  })
  it('keeps rows outside the requested range instead of dropping them', () => {
    const u = aggregateUsage(rows, 'hour', at(11), at(11))
    expect(u.totals.quota).toBe(550)
    expect(u.buckets.map((b) => b.ts)).toContain(at(10))
  })
  it('no rows is an honest empty usage, not NaN', () => {
    const u = aggregateUsage([], 'day')
    expect(u.buckets).toEqual([])
    expect(u.totals).toEqual({ quota: 0, count: 0, tokens: 0 })
  })
  it('ignores rows without a timestamp and non-finite numbers', () => {
    const u = aggregateUsage(
      [
        { model_name: 'x', quota: 5, count: 1 },
        { model_name: 'y', created_at: at(9), quota: Number.NaN, count: 2 },
      ],
      'hour'
    )
    expect(u.totals).toEqual({ quota: 0, count: 2, tokens: 0 })
  })
})

describe('buildChartSeries', () => {
  const many: QuotaRow[] = Array.from({ length: 5 }, (_, i) => ({
    model_name: `model-${i}`,
    created_at: at(10),
    quota: 100 - i,
    count: 10 - i,
  }))
  it('folds models beyond the limit into one other column', () => {
    const u = aggregateUsage(many, 'hour', at(10), at(10))
    const { series, points } = buildChartSeries(u, 'hour', 'quota', 3)
    expect(series.map((s) => s.key)).toEqual(['m0', 'm1', 'm2', OTHER_KEY])
    expect(series.at(-1)?.model).toBeNull()
    // model-3 (97) + model-4 (96)
    expect(points[0][OTHER_KEY]).toBe(193)
    expect(points[0].m0).toBe(100)
  })
  it('has no other column when everything fits', () => {
    const u = aggregateUsage(many, 'hour', at(10), at(10))
    const { series } = buildChartSeries(u, 'hour', 'count', 10)
    expect(series.some((s) => s.key === OTHER_KEY)).toBe(false)
  })
  it('data keys are positional, so odd model names are safe', () => {
    const u = aggregateUsage(
      [{ model_name: 'vendor/model.v1:free', created_at: at(10), quota: 1, count: 1 }],
      'hour'
    )
    const { series } = buildChartSeries(u, 'hour', 'quota')
    expect(series[0]).toEqual({ key: 'm0', model: 'vendor/model.v1:free' })
  })
})

describe('rankByCount', () => {
  it('ranks by count and folds the tail', () => {
    const u = aggregateUsage(
      [
        { model_name: 'a', created_at: at(1), count: 5 },
        { model_name: 'b', created_at: at(1), count: 3 },
        { model_name: 'c', created_at: at(1), count: 2 },
        { model_name: 'd', created_at: at(1), count: 0, quota: 9 },
      ],
      'hour'
    )
    expect(rankByCount(u, 2)).toEqual([
      { model: 'a', count: 5 },
      { model: 'b', count: 3 },
      { model: null, count: 2 },
    ])
  })
})

describe('perMinute', () => {
  it('divides by the span in minutes and guards bad spans', () => {
    expect(perMinute(120, 0, 3600)).toBe(2)
    expect(perMinute(10, 100, 100)).toBe(0)
    expect(perMinute(10, 200, 100)).toBe(0)
  })
})
