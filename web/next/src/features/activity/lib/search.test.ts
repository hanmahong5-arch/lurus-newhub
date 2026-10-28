import { describe, expect, it } from 'vitest'

import {
  sparseActivitySearch,
  usageLogsSearchFromDrilldown,
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

  it('rejects invalid days and metrics', () => {
    expect(validateActivitySearch({ days: 14, metric: 'latency' })).toEqual({})
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

describe('usageLogsSearchFromDrilldown', () => {
  it('builds start/end and omits blank model', () => {
    const s = usageLogsSearchFromDrilldown({
      model: '',
      startSec: 1_700_000_000,
      endSec: 1_700_086_400,
    })
    expect(s.model).toBeUndefined()
    expect(s.start).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/)
    expect(s.end).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/)
  })

  it('includes a named model for the Activity → logs href', () => {
    expect(
      usageLogsSearchFromDrilldown({
        model: 'claude-sonnet',
        startSec: 1_700_000_000,
        endSec: 1_700_003_600,
      }).model
    ).toBe('claude-sonnet')
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
        days: 1,
        metric: 'tokens',
        granularity: 'hour',
      })
    ).toEqual({
      tab: 'explore',
      days: 1,
      metric: 'tokens',
      granularity: 'hour',
    })
  })
})
