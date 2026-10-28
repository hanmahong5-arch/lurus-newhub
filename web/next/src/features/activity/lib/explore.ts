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
import { isBlankModel } from '@/features/dashboard/lib/model-name'
import {
  buildChartSeries,
  type Usage,
  type UsageMetric,
} from '@/features/dashboard/lib/series'

export interface ExploreRow {
  model: string
  quota: number
  count: number
  tokens: number
  sharePct: number
}

/** Ranked model table for Explore; blank models stay last. */
export function rankModels(
  usage: Usage,
  metric: UsageMetric,
  limit = 20
): ExploreRow[] {
  const total = usage.totals[metric]
  const ranked = [...usage.models].sort(
    (a, b) => b[metric] - a[metric] || a.model.localeCompare(b.model)
  )
  const named = ranked.filter((m) => !isBlankModel(m.model))
  const blank = ranked.filter((m) => isBlankModel(m.model))
  const ordered = [...named, ...blank].slice(0, limit)
  return ordered.map((m) => ({
    model: m.model,
    quota: m.quota,
    count: m.count,
    tokens: m.tokens,
    sharePct: total > 0 ? (m[metric] / total) * 100 : 0,
  }))
}

/** Chart series for Explore (quota / count / tokens). */
export function exploreChart(
  usage: Usage,
  granularity: 'hour' | 'day' | 'week',
  metric: UsageMetric
) {
  return buildChartSeries(usage, granularity, metric)
}
