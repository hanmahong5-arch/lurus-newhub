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
import { afterEach, describe, expect, it, vi } from 'vitest'

import {
  formatCompactNumber,
  formatNumber,
  formatPercent,
  formatTimestampForInput,
  formatTimestampRelative,
  formatTimestampToDate,
  formatTokens,
  formatUseTime,
  parseTimestampFromInput,
} from './format'

describe('format', () => {
  afterEach(() => vi.useRealTimers())

  it('renders a dash for missing numbers', () => {
    expect(formatNumber(null)).toBe('-')
    expect(formatCompactNumber(undefined)).toBe('-')
    expect(formatPercent(Number.NaN)).toBe('-')
  })

  it('formats numbers and percentages', () => {
    expect(formatNumber(1234.567, 'en-US')).toBe('1,234.57')
    expect(formatCompactNumber(1500, 'en-US')).toBe('1.5K')
    expect(formatPercent(12.5)).toMatch(/12\.5/)
  })

  it('treats 0, -1 and missing timestamps as "never"', () => {
    expect(formatTimestampToDate(undefined)).toBe('-')
    expect(formatTimestampToDate(0)).toBe('-')
    expect(formatTimestampToDate(-1)).toBe('-')
    expect(formatTimestampRelative(-1)).toBe('-')
  })

  it('formats a timestamp in seconds and in milliseconds alike', () => {
    const sec = 1_700_000_000
    expect(formatTimestampToDate(sec)).toBe(
      formatTimestampToDate(sec * 1000, 'milliseconds')
    )
    expect(formatTimestampToDate(sec)).toMatch(/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/)
  })

  it('formats relative time against now', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-01-01T00:10:00Z'))
    const now = Math.floor(Date.now() / 1000)
    expect(formatTimestampRelative(now - 30, 'seconds', 'en-US')).toBe(
      '30 seconds ago'
    )
    expect(formatTimestampRelative(now - 3600, 'seconds', 'en-US')).toBe(
      '1 hour ago'
    )
  })

  it('formats tokens and durations', () => {
    expect(formatTokens(0)).toBe('-')
    expect(formatTokens(999)).toBe('999')
    expect(formatTokens(1500)).toBe('1.5K')
    expect(formatTokens(2_500_000)).toBe('2.50M')
    expect(formatUseTime(1.234)).toBe('1.2s')
    expect(formatUseTime(125)).toBe('2m 5s')
  })

  it('round-trips datetime-local input values', () => {
    expect(formatTimestampForInput(-1)).toBe('')
    expect(parseTimestampFromInput('')).toBe(-1)
    const ts = parseTimestampFromInput('2026-03-04T05:06')
    expect(formatTimestampForInput(ts)).toBe('2026-03-04T05:06')
  })
})
