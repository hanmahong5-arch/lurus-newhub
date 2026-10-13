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
import type { TimeGranularity } from '@/lib/time'

import {
  CHART_PREFERENCES_STORAGE_KEY,
  DEFAULT_CHART_PREFERENCES,
  MAX_RANGE_SECONDS,
  TIME_RANGE_PRESETS,
} from './constants'
import type {
  ConsumptionDistributionChartType,
  DashboardChartPreferences,
  DashboardFilters,
  ModelAnalyticsChartTab,
} from './types'

function isGranularity(value: unknown): value is TimeGranularity {
  return value === 'hour' || value === 'day' || value === 'week'
}

function isConsumptionChart(
  value: unknown
): value is ConsumptionDistributionChartType {
  return value === 'bar' || value === 'area'
}

function isModelChart(value: unknown): value is ModelAnalyticsChartTab {
  return value === 'trend' || value === 'proportion' || value === 'top'
}

function isPresetDays(value: unknown): value is number {
  return TIME_RANGE_PRESETS.some((p) => p.days === value)
}

/** Saved chart preferences; anything missing or malformed falls back. */
export function getSavedChartPreferences(): DashboardChartPreferences {
  if (typeof window === 'undefined') return DEFAULT_CHART_PREFERENCES
  try {
    const raw = window.localStorage.getItem(CHART_PREFERENCES_STORAGE_KEY)
    if (!raw) return DEFAULT_CHART_PREFERENCES
    const parsed = JSON.parse(raw) as Partial<DashboardChartPreferences>
    return {
      consumptionDistributionChart: isConsumptionChart(
        parsed.consumptionDistributionChart
      )
        ? parsed.consumptionDistributionChart
        : DEFAULT_CHART_PREFERENCES.consumptionDistributionChart,
      modelAnalyticsChart: isModelChart(parsed.modelAnalyticsChart)
        ? parsed.modelAnalyticsChart
        : DEFAULT_CHART_PREFERENCES.modelAnalyticsChart,
      defaultTimeRangeDays: isPresetDays(parsed.defaultTimeRangeDays)
        ? parsed.defaultTimeRangeDays
        : DEFAULT_CHART_PREFERENCES.defaultTimeRangeDays,
      defaultTimeGranularity: isGranularity(parsed.defaultTimeGranularity)
        ? parsed.defaultTimeGranularity
        : DEFAULT_CHART_PREFERENCES.defaultTimeGranularity,
    }
  } catch {
    return DEFAULT_CHART_PREFERENCES
  }
}

export function saveChartPreferences(
  preferences: DashboardChartPreferences
): void {
  if (typeof window === 'undefined') return
  try {
    window.localStorage.setItem(
      CHART_PREFERENCES_STORAGE_KEY,
      JSON.stringify(preferences)
    )
  } catch {
    // Storage may be unavailable (private mode); preferences are a nicety.
  }
}

export function buildDefaultFilters(
  preferences: DashboardChartPreferences = getSavedChartPreferences()
): DashboardFilters {
  return {
    days: preferences.defaultTimeRangeDays,
    granularity: preferences.defaultTimeGranularity,
  }
}

/** A quick range implies a sensible bucket size (1d hourly, 29d weekly). */
export function granularityForRangeDays(days: number): TimeGranularity {
  if (days <= 1) return 'hour'
  if (days >= 29) return 'week'
  return 'day'
}

/** Resolve the filters to a concrete [start, end] in unix seconds. */
export function resolveRange(
  filters: DashboardFilters,
  nowSec = Math.floor(Date.now() / 1000)
): { start: number; end: number } {
  if (filters.days == null && filters.start && filters.end) {
    return {
      start: Math.floor(filters.start.getTime() / 1000),
      end: Math.floor(filters.end.getTime() / 1000),
    }
  }
  const days = filters.days ?? DEFAULT_CHART_PREFERENCES.defaultTimeRangeDays
  return { start: nowSec - days * 86_400, end: nowSec }
}

export type RangeProblem = 'incomplete' | 'order' | 'too-long'

/** Why a custom range cannot be queried, or null when it can. */
export function rangeProblem(
  start: Date | undefined,
  end: Date | undefined
): RangeProblem | null {
  if (!start || !end) return 'incomplete'
  const span = Math.floor((end.getTime() - start.getTime()) / 1000)
  if (span <= 0) return 'order'
  if (span > MAX_RANGE_SECONDS) return 'too-long'
  return null
}

/** The preset whose span equals the applied range, for button highlighting. */
export function detectPresetDays(filters: DashboardFilters): number | null {
  if (filters.days != null) return filters.days
  if (!filters.start || !filters.end) return null
  const days = Math.round(
    (filters.end.getTime() - filters.start.getTime()) / 86_400_000
  )
  return isPresetDays(days) ? days : null
}

/** Stable react-query key part for a set of filters. */
export function filtersKey(filters: DashboardFilters): string {
  return filters.days != null
    ? `rolling:${filters.days}:${filters.granularity}`
    : `fixed:${filters.start?.getTime() ?? 0}:${filters.end?.getTime() ?? 0}:${filters.granularity}`
}
