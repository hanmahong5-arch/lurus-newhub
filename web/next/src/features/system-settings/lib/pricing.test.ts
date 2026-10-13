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

import {
  applyRow,
  buildRows,
  changedKeys,
  parseNumberMap,
  parsePricingMaps,
  removeModel,
  rowSignature,
  validateNumberMap,
  type PricingKey,
  type PricingMaps,
} from './pricing'
import {
  validateGroupGroupRatios,
  validateGroupRatios,
  validateRateLimitGroups,
  validateUsableGroups,
} from './validators'

function maps(raw: Partial<Record<PricingKey, string>>): PricingMaps {
  return parsePricingMaps((key) => raw[key] ?? '')
}

describe('parseNumberMap', () => {
  it('keeps finite numbers and drops everything else', () => {
    expect(parseNumberMap('{"a":1,"b":"x","c":null,"d":2.5}')).toEqual({
      a: 1,
      d: 2.5,
    })
    expect(parseNumberMap('')).toEqual({})
    expect(parseNumberMap('[1]')).toEqual({})
    expect(parseNumberMap('{')).toEqual({})
  })
})

describe('buildRows', () => {
  it('merges the option rows into one row per model', () => {
    const rows = buildRows(
      maps({
        ModelRatio: '{"model-b":1,"model-a":2}',
        CompletionRatio: '{"model-a":4}',
        ModelPrice: '{"model-c":0.5}',
      })
    )
    expect(rows.map((r) => r.name)).toEqual(['model-a', 'model-b', 'model-c'])
    expect(rows[0]).toEqual({
      name: 'model-a',
      mode: 'per-token',
      ratio: 2,
      completion: 4,
    })
    expect(rows[2]).toEqual({ name: 'model-c', mode: 'per-request', price: 0.5 })
  })

  it('a model with no entry is absent, never a zero-priced row', () => {
    expect(buildRows(maps({}))).toEqual([])
  })
})

describe('applyRow / removeModel', () => {
  const base = maps({
    ModelRatio: '{"model-a":1}',
    CompletionRatio: '{"model-a":2}',
  })

  it('switching to per-request clears the ratios', () => {
    const next = applyRow(base, { name: 'model-a', mode: 'per-request', price: 1 })
    expect(next.ModelPrice).toEqual({ 'model-a': 1 })
    expect(next.ModelRatio).toEqual({})
    expect(next.CompletionRatio).toEqual({})
  })

  it('switching to per-token clears the fixed price and drops blank fields', () => {
    const start = maps({ ModelPrice: '{"model-a":1}' })
    const next = applyRow(start, { name: 'model-a', mode: 'per-token', ratio: 3 })
    expect(next.ModelPrice).toEqual({})
    expect(next.ModelRatio).toEqual({ 'model-a': 3 })
    const cleared = applyRow(base, { name: 'model-a', mode: 'per-token', ratio: 1 })
    expect(cleared.CompletionRatio).toEqual({})
  })

  it('renames by moving every entry and does not mutate the input', () => {
    const next = applyRow(
      base,
      { name: 'model-z', mode: 'per-token', ratio: 1, completion: 2 },
      'model-a'
    )
    expect(Object.keys(next.ModelRatio)).toEqual(['model-z'])
    expect(base.ModelRatio).toEqual({ 'model-a': 1 })
  })

  it('removes a model from every map', () => {
    const next = removeModel(base, 'model-a')
    expect(buildRows(next)).toEqual([])
  })
})

describe('change tracking', () => {
  it('reports only the maps that differ', () => {
    const before = maps({ ModelRatio: '{"a":1}', ModelPrice: '{"b":1}' })
    const after = applyRow(before, { name: 'a', mode: 'per-token', ratio: 2 })
    expect(changedKeys(before, after)).toEqual(['ModelRatio'])
  })

  it('has a stable signature per row', () => {
    const [row] = buildRows(maps({ ModelRatio: '{"a":1}' }))
    expect(rowSignature(row)).toBe(rowSignature({ ...row }))
    expect(rowSignature(row)).not.toBe(rowSignature({ ...row, ratio: 2 }))
    expect(rowSignature(undefined)).toBe('')
  })
})

describe('validators', () => {
  it('validateNumberMap refuses non-numbers and negatives', () => {
    expect(validateNumberMap({ a: 1 })).toBeNull()
    expect(validateNumberMap({ a: '1' })).toBe('Every value must be a number')
    expect(validateNumberMap({ a: -1 })).toBe('Values cannot be negative')
    expect(validateNumberMap([])).toBe('Expected a JSON object')
  })

  it('validateRateLimitGroups mirrors the server rule', () => {
    expect(validateRateLimitGroups({ default: [0, 1] })).toBeNull()
    expect(validateRateLimitGroups({ default: [0, 0] })).toMatch(/maxSuccess/)
    expect(validateRateLimitGroups({ default: [1] })).toMatch(/whole numbers/)
    expect(validateRateLimitGroups({ default: [1.5, 1] })).toMatch(
      /whole numbers/
    )
    expect(validateRateLimitGroups({ default: [3000000000, 1] })).toMatch(
      /2,147,483,647/
    )
  })

  it('validates group ratio shapes', () => {
    expect(validateGroupRatios({ vip: 0.5 })).toBeNull()
    expect(validateGroupRatios({ vip: -1 })).toBe('Ratios cannot be negative')
    expect(validateGroupGroupRatios({ vip: { default: 0.9 } })).toBeNull()
    expect(validateGroupGroupRatios({ vip: 1 })).toMatch(/object/)
    expect(validateUsableGroups({ vip: 'VIP' })).toBeNull()
    expect(validateUsableGroups({ vip: 1 })).toBe('Every description must be text')
  })
})
