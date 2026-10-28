import { describe, expect, it } from 'vitest'

import {
  sparseActivitySearch,
  validateActivitySearch,
  validateUsageLogsSearch,
} from './search'

describe('validateActivitySearch', () => {
  it('keeps known values and drops garbage', () => {
    expect(
      validateActivitySearch({
        tab: 'explore',
        days: '7',
        metric: 'tokens',
        granularity: 'hour',
        extra: 1,
      })
    ).toEqual({
      tab: 'explore',
      days: 7,
      metric: 'tokens',
      granularity: 'hour',
    })
  })

  it('accepts 14-day preset and rejects unknown metrics', () => {
    expect(validateActivitySearch({ days: 14 })).toEqual({ days: 14 })
    expect(validateActivitySearch({ days: 1, metric: 'latency' })).toEqual({})
  })
})

describe('validateUsageLogsSearch', () => {
  it('accepts model/token/type and datetime-local bounds', () => {
    expect(
      validateUsageLogsSearch({
        model: ' gpt-4o ',
        token: 'e2e',
        start: '2026-10-01T00:00',
        end: '2026-10-07T23:59',
        type: '5',
      })
    ).toEqual({
      model: 'gpt-4o',
      token: 'e2e',
      start: '2026-10-01T00:00',
      end: '2026-10-07T23:59',
      type: 5,
    })
  })

  it('drops bad dates and non-positive types', () => {
    expect(
      validateUsageLogsSearch({ start: 'nope', type: 0, model: '' })
    ).toEqual({})
  })
})

describe('sparseActivitySearch', () => {
  it('omits default overview/7d/quota/day', () => {
    expect(
      sparseActivitySearch({
        tab: 'overview',
        days: 7,
        metric: 'quota',
        granularity: 'day',
      })
    ).toEqual({})
  })

  it('keeps non-defaults', () => {
    expect(
      sparseActivitySearch({
        tab: 'explore',
        days: 14,
        metric: 'tokens',
        granularity: 'week',
      })
    ).toEqual({
      tab: 'explore',
      days: 14,
      metric: 'tokens',
      granularity: 'week',
    })
  })
})
