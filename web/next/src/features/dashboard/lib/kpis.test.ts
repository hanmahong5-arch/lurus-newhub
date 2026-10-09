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
  costByModel,
  dailySeries,
  errorRate,
  formatErrorRate,
  formatLatencyMs,
  formatQps,
  latencyPercentile,
  modelDistribution,
  percentDelta,
  periodDelta,
  qps,
} from './kpis'
import type { LogRow, QuotaRow } from './types'

const consume = (ms: number, quota = 100, model = 'm1'): LogRow => ({
  type: 2,
  total_latency_ms: ms,
  quota,
  model_name: model,
})

describe('latency / error KPIs', () => {
  it('median of even count averages the middle pair', () => {
    expect(
      latencyPercentile(
        [consume(100), consume(200), consume(300), consume(500)],
        50
      )
    ).toBe(250)
  })
  it('nearest-rank p95 ignores non-consume and zero latency rows', () => {
    const logs: LogRow[] = [
      ...Array.from({ length: 20 }, (_, i) => consume((i + 1) * 10)),
      { type: 5, total_latency_ms: 99999 },
      consume(0),
    ]
    expect(latencyPercentile(logs, 95)).toBe(190)
  })
  it('no samples -> null, shown as placeholder', () => {
    expect(latencyPercentile([], 50)).toBeNull()
    expect(formatLatencyMs(null)).toBe('--')
    expect(formatErrorRate(errorRate([]))).toBe('--')
  })
  it('error rate = errors / (consume + errors)', () => {
    const logs: LogRow[] = [consume(1), consume(1), consume(1), { type: 5 }]
    expect(errorRate(logs)).toBe(0.25)
    expect(formatErrorRate(0.25)).toBe('25.0%')
    expect(formatErrorRate(0.0001)).toBe('<0.1%')
    expect(formatErrorRate(0)).toBe('0%')
  })
  it('formats latency and qps', () => {
    expect(formatLatencyMs(850)).toBe('850ms')
    expect(formatLatencyMs(1500)).toBe('1.50s')
    expect(formatQps(0)).toBe('0')
    expect(formatQps(0.001)).toBe('<0.01')
    expect(formatQps(0.5)).toBe('0.50')
    expect(formatQps(12.4)).toBe('12')
  })
  it('qps prefers the server total over the capped page', () => {
    expect(qps(600, 100, 300)).toBe(2)
    expect(qps(null, 30, 300)).toBeCloseTo(0.1)
    expect(qps(0, 0, 300)).toBe(0)
  })
})

describe('costByModel', () => {
  it('groups consume rows, drops zero quota, sorts by spend', () => {
    const out = costByModel([
      consume(1, 100, 'a'),
      consume(1, 300, 'b'),
      consume(1, 50, 'a'),
      consume(1, 0, 'c'),
      { type: 5, quota: 999, model_name: 'e' },
    ])
    expect(out).toEqual([
      { model: 'b', totalQuota: 300, requestCount: 1 },
      { model: 'a', totalQuota: 150, requestCount: 2 },
    ])
  })
})

describe('trend series', () => {
  const noon = (y: number, m: number, d: number) =>
    Math.floor(new Date(y, m - 1, d, 12).getTime() / 1000)

  it('zero-fills a fixed-length series, oldest first', () => {
    const s = dailySeries(
      [
        { created_at: noon(2026, 10, 8), quota: 10, count: 1 },
        { created_at: noon(2026, 10, 8) + 60, quota: 5, count: 2 },
        { created_at: noon(2026, 10, 6), quota: 7, count: 3 },
        { created_at: noon(2026, 9, 1), quota: 999, count: 9 },
      ],
      noon(2026, 10, 8),
      3
    )
    expect(s.map((p) => p.quota)).toEqual([7, 0, 15])
    expect(s.map((p) => p.count)).toEqual([3, 0, 3])
  })
  it('percentDelta never fabricates from a zero baseline', () => {
    expect(percentDelta(10, 0)).toBeNull()
    expect(percentDelta(15, 10)).toBe(50)
    expect(percentDelta(5, 10)).toBe(-50)
  })
  it('periodDelta compares the last 7 days with the prior 7', () => {
    const rows: QuotaRow[] = []
    for (let d = 1; d <= 14; d++) {
      rows.push({ created_at: noon(2026, 10, d), quota: d <= 7 ? 10 : 20, count: 1 })
    }
    const s = dailySeries(rows, noon(2026, 10, 14), 14)
    expect(periodDelta(s, 'quota')).toBe(100)
    expect(periodDelta(s.slice(-10), 'quota')).toBeNull()
  })
  it('modelDistribution sums per model and caps', () => {
    const out = modelDistribution(
      [
        { model_name: 'a', quota: 1 },
        { model_name: 'b', quota: 5 },
        { model_name: 'a', quota: 6 },
        { quota: 2 },
      ],
      2
    )
    expect(out).toEqual([
      { model: 'a', quota: 7 },
      { model: 'b', quota: 5 },
    ])
  })
})
