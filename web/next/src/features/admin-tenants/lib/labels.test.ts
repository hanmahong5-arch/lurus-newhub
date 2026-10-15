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

import { formatIsoDate } from './labels'

describe('formatIsoDate', () => {
  it('shows an unset Go time as missing, not as year 0', () => {
    expect(formatIsoDate('0001-01-01T00:00:00Z')).toBe('-')
  })

  it('shows missing and malformed values as missing', () => {
    expect(formatIsoDate(undefined)).toBe('-')
    expect(formatIsoDate('')).toBe('-')
    expect(formatIsoDate('not a date')).toBe('-')
  })

  it('formats a real timestamp', () => {
    expect(formatIsoDate('2026-08-30T12:48:00Z')).toMatch(/^2026-08-30 \d\d:48$/)
  })
})
