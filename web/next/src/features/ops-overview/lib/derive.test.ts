import { describe, expect, it } from 'vitest'

import {
  expiringSoon,
  formatCostCny4,
  probeFromSummary,
  reasonTotals,
  totalsOf,
  unroutable,
  windowNearLimit,
} from './derive'
import type { ChannelProbe, SummaryChannel } from './types'

const NOW = 1_000_000

function probe(over: Partial<ChannelProbe>): ChannelProbe {
  return {
    channelId: 1,
    name: 'ch',
    expiresAt: null,
    windowUsedPct: null,
    routable: true,
    reasons: [],
    cooldownUntil: null,
    lastError: '',
    ...over,
  }
}

describe('probeFromSummary', () => {
  it('maps a roll-up row; unknown window stays null, never 0', () => {
    expect(
      probeFromSummary({
        id: 7,
        name: 'zhipu',
        routable: false,
        reasons: ['cooling_429'],
        expires_at: 300,
        window_max_used_pct: 93.5,
        cooldown_until: 900,
        last_error: 'rate limited',
      })
    ).toEqual({
      channelId: 7,
      name: 'zhipu',
      expiresAt: 300,
      windowUsedPct: 93.5,
      routable: false,
      reasons: ['cooling_429'],
      cooldownUntil: 900,
      lastError: 'rate limited',
    })
    expect(
      probeFromSummary({
        id: 8,
        name: 'plain',
        routable: true,
        reasons: [],
        expires_at: 0,
        window_max_used_pct: null,
      })
    ).toMatchObject({
      expiresAt: null,
      windowUsedPct: null,
      cooldownUntil: null,
    })
  })
  it('keeps a real 0% window as 0', () => {
    expect(
      probeFromSummary({
        id: 1,
        name: 'x',
        routable: true,
        reasons: [],
        expires_at: 0,
        window_max_used_pct: 0,
      }).windowUsedPct
    ).toBe(0)
  })
})

describe('lists', () => {
  it('expiringSoon keeps <72h incl. expired, sorted soonest first', () => {
    const rows = [
      probe({ channelId: 1, expiresAt: NOW + 71 * 3600 }),
      probe({ channelId: 2, expiresAt: NOW + 73 * 3600 }),
      probe({ channelId: 3, expiresAt: NOW - 10 }),
      probe({ channelId: 4, expiresAt: null }),
    ]
    expect(expiringSoon(rows, NOW).map((p) => p.channelId)).toEqual([3, 1])
  })
  it('windowNearLimit is strictly above 90, fullest first', () => {
    const rows = [
      probe({ channelId: 1, windowUsedPct: 90 }),
      probe({ channelId: 2, windowUsedPct: 91 }),
      probe({ channelId: 3, windowUsedPct: 99 }),
      probe({ channelId: 4, windowUsedPct: null }),
    ]
    expect(windowNearLimit(rows).map((p) => p.channelId)).toEqual([3, 2])
  })
  it('unroutable puts manual switch-offs last and totals reasons', () => {
    const rows = [
      probe({ channelId: 1, routable: false, reasons: ['disabled_manual'] }),
      probe({
        channelId: 2,
        routable: false,
        reasons: ['cooling_429', 'auth_failed'],
      }),
      probe({ channelId: 3, routable: true }),
      probe({ channelId: 4, routable: false, reasons: ['cooling_429'] }),
    ]
    expect(unroutable(rows).map((p) => p.channelId)).toEqual([2, 4, 1])
    expect(reasonTotals(rows)).toEqual([
      ['cooling_429', 2],
      ['auth_failed', 1],
      ['disabled_manual', 1],
    ])
  })
})

describe('formatting and totals', () => {
  it('formats 0.0001 CNY units', () => {
    expect(formatCostCny4(123456)).toBe('¥12.35')
    expect(formatCostCny4(undefined)).toBe('--')
  })
  it('sums cost, requests and quota', () => {
    const rows = [
      { cost_cny4: 10, requests: 2, quota: 5 },
      { cost_cny4: 1, requests: 3, quota: 7 },
    ] as SummaryChannel[]
    expect(totalsOf(rows)).toEqual({ cost: 11, requests: 5, quota: 12 })
  })
})
