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

import { QUOTA_COLUMN_MAX } from '../constants'
import { applyQuotaAdjust, parseAmount } from './quota'

describe('applyQuotaAdjust', () => {
  it('adds, subtracts and overrides', () => {
    expect(applyQuotaAdjust(100, 'add', 50)).toEqual({ ok: true, quota: 150 })
    expect(applyQuotaAdjust(100, 'subtract', 40)).toEqual({ ok: true, quota: 60 })
    expect(applyQuotaAdjust(100, 'override', 0)).toEqual({ ok: true, quota: 0 })
  })

  it('refuses a balance below zero', () => {
    expect(applyQuotaAdjust(100, 'subtract', 101)).toEqual({
      ok: false,
      reason: 'negative',
    })
    expect(applyQuotaAdjust(100, 'override', -1)).toEqual({
      ok: false,
      reason: 'negative',
    })
  })

  it('refuses a zero or non-finite delta and an overflowing balance', () => {
    expect(applyQuotaAdjust(100, 'add', 0)).toEqual({ ok: false, reason: 'invalid' })
    expect(applyQuotaAdjust(100, 'add', Number.NaN)).toEqual({
      ok: false,
      reason: 'invalid',
    })
    expect(applyQuotaAdjust(QUOTA_COLUMN_MAX, 'add', 1)).toEqual({
      ok: false,
      reason: 'too-large',
    })
  })
})

describe('parseAmount', () => {
  it('reads plain decimals and rejects everything else', () => {
    expect(parseAmount(' 12.5 ')).toBe(12.5)
    expect(parseAmount('-3')).toBe(-3)
    expect(parseAmount('')).toBeNull()
    expect(parseAmount('1e3')).toBeNull()
    expect(parseAmount('abc')).toBeNull()
  })
})
