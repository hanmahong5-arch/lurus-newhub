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

import { blankLast, isBlankModel, normalizeModel } from './model-name'

describe('isBlankModel / normalizeModel', () => {
  it('treats missing, empty and whitespace-only names as blank', () => {
    expect(isBlankModel(undefined)).toBe(true)
    expect(isBlankModel(null)).toBe(true)
    expect(isBlankModel('')).toBe(true)
    expect(isBlankModel('  \t')).toBe(true)
    expect(isBlankModel('model-a')).toBe(false)
    // A real model that is merely called "Unknown" is a real model.
    expect(isBlankModel('Unknown')).toBe(false)
  })

  it('collapses every blank to one value and leaves real names untouched', () => {
    expect(normalizeModel(undefined)).toBe('')
    expect(normalizeModel('   ')).toBe('')
    expect(normalizeModel(' model-a ')).toBe(' model-a ')
  })
})

describe('blankLast', () => {
  it('moves blank names after named ones and keeps both orders', () => {
    const rows = [
      { n: '' },
      { n: 'b' },
      { n: ' ' },
      { n: 'a' },
      { n: undefined as string | undefined },
    ]
    expect(blankLast(rows, (r) => r.n).map((r) => r.n)).toEqual([
      'b',
      'a',
      '',
      ' ',
      undefined,
    ])
  })

  it('does not mutate its input', () => {
    const rows = [{ n: '' }, { n: 'a' }]
    blankLast(rows, (r) => r.n)
    expect(rows.map((r) => r.n)).toEqual(['', 'a'])
  })
})
