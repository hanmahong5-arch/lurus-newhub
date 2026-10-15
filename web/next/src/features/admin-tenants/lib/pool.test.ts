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

import type { CreditPool } from '../types'
import {
  isTopupDisabled,
  isUnlimited,
  parseAlertPct,
  parseCeiling,
  parseMoneyText,
  parseTopup,
  poolFillRatio,
  poolHealth,
  validateMaxBalance,
  validateTopupAmount,
} from './pool'

const pool = (over: Partial<CreditPool> = {}): CreditPool => ({
  id: 1,
  tenant_id: 't-acme',
  current_balance: 800,
  max_balance: 1000,
  reset_period: 'monthly',
  ...over,
})

// 1 USD = 500,000 units, as the console's money config would say.
const fromDisplay = (amount: number) => Math.round(amount * 500_000)

describe('credit pool helpers', () => {
  it('isUnlimited recognises the -1 sentinel only', () => {
    expect(isUnlimited(null)).toBe(false)
    expect(isUnlimited(pool({ max_balance: 0 }))).toBe(false)
    expect(isUnlimited(pool({ max_balance: -1 }))).toBe(true)
    expect(isUnlimited(pool({ max_balance: 1000 }))).toBe(false)
  })

  it('poolHealth classifies the tenant state', () => {
    expect(poolHealth(null)).toBe('unconfigured')
    expect(poolHealth(pool({ max_balance: -1, current_balance: 99 }))).toBe(
      'unlimited'
    )
    expect(poolHealth(pool({ current_balance: 0 }))).toBe('exhausted')
    expect(poolHealth(pool({ current_balance: -5 }))).toBe('exhausted')
    // 10% of the ceiling is under the 20% line.
    expect(poolHealth(pool({ current_balance: 100 }))).toBe('warning')
    expect(poolHealth(pool({ current_balance: 800 }))).toBe('ok')
  })

  it('poolHealth does not divide by a zero ceiling', () => {
    expect(poolHealth(pool({ max_balance: 0, current_balance: 5 }))).toBe('ok')
  })

  it('isTopupDisabled when there is no pool to top up', () => {
    expect(isTopupDisabled(null)).toBe(true)
    expect(isTopupDisabled(pool())).toBe(false)
  })

  it('poolFillRatio is the share of the ceiling left, null without one', () => {
    expect(poolFillRatio(pool({ current_balance: 250, max_balance: 1000 }))).toBe(
      0.25
    )
    expect(poolFillRatio(pool({ max_balance: -1 }))).toBeNull()
    expect(poolFillRatio(pool({ max_balance: 0 }))).toBeNull()
    expect(poolFillRatio(null)).toBeNull()
    expect(poolFillRatio(pool({ current_balance: 5000 }))).toBe(1)
  })

  it('validateMaxBalance accepts -1, 0 and positives; rejects other negatives and NaN', () => {
    expect(validateMaxBalance(-1)).toBeNull()
    expect(validateMaxBalance(0)).toBeNull()
    expect(validateMaxBalance(1_000_000)).toBeNull()
    expect(validateMaxBalance(-5)).toBe('negative')
    expect(validateMaxBalance(Number.NaN)).toBe('not-a-number')
  })

  it('validateTopupAmount requires a positive integer', () => {
    expect(validateTopupAmount(100)).toBeNull()
    expect(validateTopupAmount(0)).toBe('not-positive')
    expect(validateTopupAmount(-5)).toBe('not-positive')
    expect(validateTopupAmount(1.5)).toBe('not-integer')
    expect(validateTopupAmount(Number.NaN)).toBe('not-a-number')
  })
})

describe('money inputs', () => {
  it('parseMoneyText accepts plain decimals only', () => {
    expect(parseMoneyText('5')).toBe(5)
    expect(parseMoneyText(' 12.50 ')).toBe(12.5)
    expect(parseMoneyText('')).toBeNull()
    expect(parseMoneyText('-1')).toBeNull()
    expect(parseMoneyText('1e3')).toBeNull()
    expect(parseMoneyText('abc')).toBeNull()
  })

  it('parseTopup converts display money to whole quota units', () => {
    expect(parseTopup('2', fromDisplay)).toEqual({ ok: true, amount: 1_000_000 })
    expect(parseTopup('0.5', fromDisplay)).toEqual({ ok: true, amount: 250_000 })
  })

  it('parseTopup refuses zero, junk and a missing currency config', () => {
    expect(parseTopup('0', fromDisplay)).toEqual({
      ok: false,
      error: 'not-positive',
    })
    expect(parseTopup('abc', fromDisplay)).toEqual({
      ok: false,
      error: 'not-a-number',
    })
    expect(parseTopup('5', () => null)).toEqual({
      ok: false,
      error: 'currency',
    })
  })

  it('parseCeiling: unlimited is -1 whatever the amount field holds', () => {
    expect(parseCeiling({ unlimited: true, amount: '' }, fromDisplay)).toEqual({
      ok: true,
      value: -1,
    })
    expect(
      parseCeiling({ unlimited: false, amount: '10' }, fromDisplay)
    ).toEqual({ ok: true, value: 5_000_000 })
    expect(
      parseCeiling({ unlimited: false, amount: '0' }, fromDisplay)
    ).toEqual({ ok: true, value: 0 })
  })

  it('parseCeiling refuses a blank, negative or unconvertible amount', () => {
    expect(
      parseCeiling({ unlimited: false, amount: '' }, fromDisplay)
    ).toMatchObject({ ok: false })
    expect(
      parseCeiling({ unlimited: false, amount: '-5' }, fromDisplay)
    ).toMatchObject({ ok: false })
    expect(
      parseCeiling({ unlimited: false, amount: '5' }, () => null)
    ).toEqual({ ok: false, error: 'currency' })
  })

  it('parseAlertPct takes a whole percent from 1 to 100', () => {
    expect(parseAlertPct('80')).toBe(80)
    expect(parseAlertPct('1')).toBe(1)
    expect(parseAlertPct('100')).toBe(100)
    expect(parseAlertPct('0')).toBeNull()
    expect(parseAlertPct('101')).toBeNull()
    expect(parseAlertPct('7.5')).toBeNull()
    expect(parseAlertPct('')).toBeNull()
  })
})
