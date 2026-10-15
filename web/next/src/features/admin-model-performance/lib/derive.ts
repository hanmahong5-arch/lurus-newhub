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
import type { ModelPerf, RangeId, SortDir, SortKey } from '../types'

/** Time-range presets and their window in seconds. */
export const RANGES: readonly { id: RangeId; seconds: number }[] = [
  { id: '1h', seconds: 3600 },
  { id: '24h', seconds: 24 * 3600 },
  { id: '7d', seconds: 7 * 24 * 3600 },
]

export const DEFAULT_RANGE: RangeId = '24h'

export function rangeSeconds(id: RangeId): number {
  return (RANGES.find((r) => r.id === id) ?? RANGES[1]).seconds
}

const SORTERS: Record<SortKey, (m: ModelPerf) => number> = {
  requests: (m) => m.requests,
  error_rate: (m) => m.error_rate,
  avg_latency_ms: (m) => m.avg_latency_ms,
  p50_latency_ms: (m) => m.p50_latency_ms,
  p95_latency_ms: (m) => m.p95_latency_ms,
}

export function sortModels(
  models: readonly ModelPerf[],
  key: SortKey,
  dir: SortDir
): ModelPerf[] {
  const acc = SORTERS[key] ?? SORTERS.requests
  const sign = dir === 'asc' ? 1 : -1
  return [...models].sort((a, b) => (acc(a) - acc(b)) * sign)
}

/** What a header click does: flip the active column, else start descending. */
export function nextSort(
  current: { key: SortKey; dir: SortDir },
  clicked: SortKey
): { key: SortKey; dir: SortDir } {
  if (current.key === clicked) {
    return { key: clicked, dir: current.dir === 'desc' ? 'asc' : 'desc' }
  }
  return { key: clicked, dir: 'desc' }
}

export interface PerfSummary {
  totalRequests: number
  totalErrors: number
  totalSamples: number
  /** Latency average weighted by latency samples (not requests). */
  weightedLatency: number
  totalQuota: number
}

export function summarize(models: readonly ModelPerf[]): PerfSummary {
  let totalRequests = 0
  let totalErrors = 0
  let totalSamples = 0
  let weighted = 0
  let totalQuota = 0
  for (const m of models) {
    totalRequests += m.requests || 0
    totalErrors += m.errors || 0
    totalSamples += m.latency_samples || 0
    // Weight by sample count, NOT requests: error rows carry no latency.
    weighted += (m.avg_latency_ms || 0) * (m.latency_samples || 0)
    totalQuota += m.quota || 0
  }
  return {
    totalRequests,
    totalErrors,
    totalSamples,
    weightedLatency: totalSamples > 0 ? weighted / totalSamples : 0,
    totalQuota,
  }
}

export const fmtInt = (n: number | null | undefined): string =>
  Number(n ?? 0).toLocaleString()

export const fmtPct = (r: number | null | undefined): string =>
  `${((r ?? 0) * 100).toFixed(2)}%`

/**
 * Latency cells are honest about missing samples: rows aggregated purely from
 * error logs (or from before the latency column) carry latency_samples=0, and
 * a dash is shown instead of a misleading 0ms.
 */
export function fmtMs(value: number, samples: number): string {
  return samples > 0 ? `${Math.round(value)}ms` : '—'
}

export type ErrorSeverity = 'ok' | 'warn' | 'bad'

export function errorSeverity(rate: number): ErrorSeverity {
  if (rate > 0.05) return 'bad'
  if (rate > 0.01) return 'warn'
  return 'ok'
}
