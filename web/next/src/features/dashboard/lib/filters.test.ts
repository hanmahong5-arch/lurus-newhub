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
import { beforeEach, describe, expect, it } from 'vitest'

import {
  CHART_PREFERENCES_STORAGE_KEY,
  DEFAULT_CHART_PREFERENCES,
  MAX_RANGE_SECONDS,
} from './constants'
import {
  buildDefaultFilters,
  detectPresetDays,
  filtersKey,
  getSavedChartPreferences,
  granularityForRangeDays,
  rangeProblem,
  resolveRange,
  saveChartPreferences,
} from './filters'
import { hasVolume, sortPerformance } from './performance'
import type { PerformanceRow } from './types'

beforeEach(() => window.localStorage.clear())

describe('resolveRange', () => {
  it('a preset is a rolling window ending now, re-anchored every call', () => {
    const f = buildDefaultFilters({ ...DEFAULT_CHART_PREFERENCES, defaultTimeRangeDays: 7 })
    expect(resolveRange(f, 1_000_000)).toEqual({
      start: 1_000_000 - 7 * 86_400,
      end: 1_000_000,
    })
    expect(resolveRange(f, 2_000_000).end).toBe(2_000_000)
  })
  it('a custom range is fixed', () => {
    const start = new Date(2024, 0, 1)
    const end = new Date(2024, 0, 3)
    const r = resolveRange({ days: null, start, end, granularity: 'day' }, 5)
    expect(r).toEqual({
      start: Math.floor(start.getTime() / 1000),
      end: Math.floor(end.getTime() / 1000),
    })
  })
  it('every preset stays under the server span ceiling', () => {
    for (const days of [1, 7, 14, 29]) {
      const r = resolveRange({ days, granularity: 'day' }, 10_000_000)
      expect(r.end - r.start).toBeLessThanOrEqual(MAX_RANGE_SECONDS)
    }
  })
})

describe('rangeProblem', () => {
  const d = (day: number) => new Date(2024, 0, day)
  it('names why a range cannot be queried', () => {
    expect(rangeProblem(undefined, d(2))).toBe('incomplete')
    expect(rangeProblem(d(2), undefined)).toBe('incomplete')
    expect(rangeProblem(d(3), d(2))).toBe('order')
    expect(rangeProblem(d(2), d(2))).toBe('order')
    expect(rangeProblem(d(1), new Date(2024, 0, 31, 1))).toBe('too-long')
    expect(rangeProblem(d(1), d(31))).toBeNull()
  })
})

describe('presets', () => {
  it('pair a bucket size with the range', () => {
    expect(granularityForRangeDays(1)).toBe('hour')
    expect(granularityForRangeDays(7)).toBe('day')
    expect(granularityForRangeDays(14)).toBe('day')
    expect(granularityForRangeDays(29)).toBe('week')
  })
  it('detectPresetDays matches a custom range that spans a preset', () => {
    const start = new Date(2024, 0, 1)
    const end = new Date(2024, 0, 8)
    expect(detectPresetDays({ days: null, start, end, granularity: 'day' })).toBe(7)
    expect(
      detectPresetDays({ days: null, start, end: new Date(2024, 0, 6), granularity: 'day' })
    ).toBeNull()
    expect(detectPresetDays({ days: 14, granularity: 'day' })).toBe(14)
  })
  it('filtersKey differs by span and bucket size', () => {
    const a = filtersKey({ days: 1, granularity: 'hour' })
    expect(a).not.toBe(filtersKey({ days: 7, granularity: 'hour' }))
    expect(a).not.toBe(filtersKey({ days: 1, granularity: 'day' }))
  })
})

describe('chart preferences', () => {
  it('fall back when nothing or garbage is stored', () => {
    expect(getSavedChartPreferences()).toEqual(DEFAULT_CHART_PREFERENCES)
    window.localStorage.setItem(CHART_PREFERENCES_STORAGE_KEY, '{nope')
    expect(getSavedChartPreferences()).toEqual(DEFAULT_CHART_PREFERENCES)
  })
  it('round-trip, and bad fields fall back one by one', () => {
    saveChartPreferences({
      consumptionDistributionChart: 'area',
      modelAnalyticsChart: 'top',
      defaultTimeRangeDays: 14,
      defaultTimeGranularity: 'day',
    })
    expect(getSavedChartPreferences()).toMatchObject({
      consumptionDistributionChart: 'area',
      modelAnalyticsChart: 'top',
      defaultTimeRangeDays: 14,
      defaultTimeGranularity: 'day',
    })
    window.localStorage.setItem(
      CHART_PREFERENCES_STORAGE_KEY,
      JSON.stringify({ defaultTimeRangeDays: 3, modelAnalyticsChart: 'top' })
    )
    const p = getSavedChartPreferences()
    expect(p.defaultTimeRangeDays).toBe(DEFAULT_CHART_PREFERENCES.defaultTimeRangeDays)
    expect(p.modelAnalyticsChart).toBe('top')
  })
})

describe('performance rows', () => {
  const base = { p50_latency_ms: 1, p95_latency_ms: 2, error_rate: 0, enough_samples: true }
  const member: PerformanceRow[] = [
    { ...base, model_name: 'b' },
    { ...base, model_name: 'a' },
  ]
  it('members have no volume and sort by name', () => {
    expect(hasVolume(member)).toBe(false)
    expect(sortPerformance(member).map((r) => r.model_name)).toEqual(['a', 'b'])
  })
  it('admins sort by request volume', () => {
    const admin = [
      { ...base, model_name: 'a', requests: 1 },
      { ...base, model_name: 'b', requests: 9 },
    ]
    expect(hasVolume(admin)).toBe(true)
    expect(sortPerformance(admin).map((r) => r.model_name)).toEqual(['b', 'a'])
  })
})
