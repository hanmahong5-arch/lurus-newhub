import { describe, expect, it } from 'vitest'

import {
  MONEY_UNAVAILABLE,
  formatQuota,
  parseMoneyConfig,
  quotaToUsd,
  usdToQuota,
} from './money'

const usd = parseMoneyConfig({
  quota_per_unit: 500000,
  quota_display_type: 'USD',
})

describe('parseMoneyConfig', () => {
  it('reads the unit price from the status payload, not from a constant', () => {
    const a = parseMoneyConfig({ quota_per_unit: 500000 })
    const b = parseMoneyConfig({ quota_per_unit: 1000000 })
    expect(a?.quotaPerUnit).toBe(500000)
    expect(b?.quotaPerUnit).toBe(1000000)
    // Same balance, different operator setting -> different figure.
    expect(formatQuota(1_000_000, a)).toBe('$2.00')
    expect(formatQuota(1_000_000, b)).toBe('$1.00')
  })

  it.each([
    undefined,
    null,
    {},
    { quota_per_unit: 0 },
    { quota_per_unit: -1 },
    { quota_per_unit: 'abc' },
  ])('is null (never a guessed default) when the status is %j', (status) => {
    expect(parseMoneyConfig(status)).toBeNull()
  })

  it('accepts a numeric string unit price', () => {
    expect(parseMoneyConfig({ quota_per_unit: '500000' })?.quotaPerUnit).toBe(
      500000
    )
  })

  it('maps CNY to the yuan sign and the server exchange rate', () => {
    const cfg = parseMoneyConfig({
      quota_per_unit: 500000,
      quota_display_type: 'CNY',
      usd_exchange_rate: 7.3,
    })
    expect(cfg).toMatchObject({ symbol: '¥', rate: 7.3, displayType: 'CNY' })
    expect(formatQuota(500000, cfg)).toBe('¥7.30')
  })

  it('refuses CNY without a rate rather than showing yuan at rate 1', () => {
    expect(
      parseMoneyConfig({ quota_per_unit: 500000, quota_display_type: 'CNY' })
    ).toBeNull()
  })

  it('maps CUSTOM to its symbol and rate', () => {
    const cfg = parseMoneyConfig({
      quota_per_unit: 500000,
      quota_display_type: 'CUSTOM',
      custom_currency_symbol: 'C$',
      custom_currency_exchange_rate: 2,
    })
    expect(formatQuota(500000, cfg)).toBe('C$2.00')
  })

  it('treats LUTE like USD, as the legacy console does', () => {
    const cfg = parseMoneyConfig({
      quota_per_unit: 500000,
      quota_display_type: 'LUTE',
    })
    expect(formatQuota(500000, cfg)).toBe('$1.00')
  })

  it('shows raw quota for TOKENS', () => {
    const cfg = parseMoneyConfig({
      quota_per_unit: 500000,
      quota_display_type: 'TOKENS',
    })
    expect(formatQuota(1234567, cfg)).toBe('1,234,567')
  })
})

describe('formatQuota', () => {
  it('uses the placeholder when there is no config or no number', () => {
    expect(formatQuota(100, null)).toBe(MONEY_UNAVAILABLE)
    expect(formatQuota(undefined, usd)).toBe(MONEY_UNAVAILABLE)
    expect(formatQuota(Number.NaN, usd)).toBe(MONEY_UNAVAILABLE)
  })

  it('formats and rounds to two decimals', () => {
    expect(formatQuota(250000, usd)).toBe('$0.50')
    expect(formatQuota(0, usd)).toBe('$0.00')
  })

  it('shows the smallest step for a tiny positive amount, not zero', () => {
    expect(formatQuota(1, usd)).toBe('$0.01')
    expect(formatQuota(1, usd, 4)).toBe('$0.0001')
  })
})

describe('quota conversion', () => {
  it('round-trips through the configured unit price', () => {
    expect(usd).not.toBeNull()
    if (!usd) return
    expect(quotaToUsd(750000, usd)).toBe(1.5)
    expect(usdToQuota(1.5, usd)).toBe(750000)
  })
})
