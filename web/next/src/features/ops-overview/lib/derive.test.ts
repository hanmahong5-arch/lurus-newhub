import { describe, expect, it } from 'vitest'

import {
  earliestExpiry,
  expiringSoon,
  formatCostCny4,
  manualDisabledProbe,
  maxWindowUsedPct,
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

describe('earliestExpiry', () => {
  it('takes the earliest of setting and per-key ends', () => {
    expect(
      earliestExpiry({
        id: 1,
        setting: JSON.stringify({ expires_at: 500 }),
        channel_info: { multi_key_meta: { '0': { expires_at: 300 }, '1': {} } },
      })
    ).toBe(300)
  })
  it('is null when nothing is declared or the setting is not JSON', () => {
    expect(earliestExpiry({ id: 1, setting: 'not json' })).toBeNull()
    expect(earliestExpiry({ id: 1, setting: null })).toBeNull()
    expect(earliestExpiry({ id: 1, setting: '{"expires_at":0}' })).toBeNull()
  })
})

describe('maxWindowUsedPct', () => {
  it('reads direct and list shapes, null when unknown', () => {
    expect(maxWindowUsedPct({ used_pct: 40 })).toBe(40)
    expect(
      maxWindowUsedPct({ windows: [{ used_pct: 10 }, { used_pct: 93 }] })
    ).toBe(93)
    expect(maxWindowUsedPct(null)).toBeNull()
    expect(maxWindowUsedPct({})).toBeNull()
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
  it('manualDisabledProbe is unroutable with the manual reason', () => {
    const c = { channel_id: 9, name: 'x', status: 2 } as SummaryChannel
    expect(manualDisabledProbe(c)).toMatchObject({
      channelId: 9,
      routable: false,
      reasons: ['disabled_manual'],
    })
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
