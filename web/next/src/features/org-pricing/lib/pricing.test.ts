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
import { beforeEach, describe, expect, it } from 'vitest'

import {
  DRAFT_TTL_MS,
  draftKey,
  loadDraft,
  sanitizeEdits,
  saveDraft,
} from './draft'
import {
  MAX_TIERS,
  baselineValue,
  buildBatch,
  clearModel,
  emptyTierDraft,
  fieldValue,
  normalizeDiffs,
  normalizeSheet,
  setScalar,
  setTiers,
  tierErrorKey,
  tiersFromDiff,
  validateAll,
  validateEdit,
  type Edits,
  type PricingRow,
} from './pricing'

const row = (over: Partial<PricingRow> = {}): PricingRow => ({
  model_name: 'model-a',
  vendor: 'vendor-a',
  quota_type: 0,
  model_ratio: 2.5,
  completion_ratio: 1,
  model_price: 0,
  price_configured: true,
  context_tiers: [],
  enable_groups: [],
  ...over,
})

describe('normalizeSheet', () => {
  it('tolerates absent members instead of throwing', () => {
    expect(normalizeSheet(undefined)).toEqual({
      pricing: [],
      vendors: [],
      groupRatio: {},
      version: 0,
    })
    expect(normalizeSheet({ pricing: 'x', vendors: 3, group_ratio: [1] }).pricing).toEqual([])
  })

  it('only an explicit price_configured=false marks a model unpriced', () => {
    const s = normalizeSheet({
      pricing: [
        { model_name: 'a', price_configured: false },
        { model_name: 'b' },
        { model_name: 'c', price_configured: true },
      ],
    })
    expect(s.pricing.map((r) => r.price_configured)).toEqual([false, true, true])
  })

  it('drops rows without a model name and keeps optional fields optional', () => {
    const s = normalizeSheet({
      pricing: [
        { vendor: 'x' },
        { model_name: 'a', cache_ratio: 0.5, search_unit_price: 0, usage_unit: 'search_unit' },
        { model_name: 'b' },
      ],
      version: 4,
      group_ratio: { default: 1, bad: 'x' },
    })
    expect(s.pricing).toHaveLength(2)
    expect(s.pricing[0].cache_ratio).toBe(0.5)
    expect(s.pricing[0].search_unit_price).toBe(0)
    expect(s.pricing[1].cache_ratio).toBeUndefined()
    expect(s.pricing[1].search_unit_price).toBeUndefined()
    expect(s.version).toBe(4)
    expect(s.groupRatio).toEqual({ default: 1 })
  })
})

describe('baselines', () => {
  it('an unpriced model starts blank, never at the 0 the server sends', () => {
    const r = row({ model_ratio: 0, price_configured: false })
    expect(baselineValue(r, 'model_ratio')).toBe('')
    expect(baselineValue(row({ model_price: 0, price_configured: false }), 'model_price')).toBe('')
    expect(baselineValue(row(), 'model_ratio')).toBe('2.5')
  })

  it('a missing cache ratio is blank, a present one is shown', () => {
    expect(baselineValue(row(), 'cache_ratio')).toBe('')
    expect(baselineValue(row({ cache_ratio: 0.42 }), 'cache_ratio')).toBe('0.42')
  })

  it('an edit wins over the baseline', () => {
    expect(fieldValue(row(), { model_ratio: '9' }, 'model_ratio')).toBe('9')
    expect(fieldValue(row(), undefined, 'model_ratio')).toBe('2.5')
  })
})

describe('edit buffer', () => {
  it('discarding the last field of a model removes the model', () => {
    let e: Edits = {}
    e = setScalar(e, 'model-a', 'model_ratio', '3')
    expect(Object.keys(e)).toEqual(['model-a'])
    e = clearModel(e, 'model-a')
    expect(e).toEqual({})
  })

  it('a tier list identical to the server one is not an edit', () => {
    const r = row({ context_tiers: [{ threshold_tokens: 0, model_ratio: 1 }] })
    let e = setTiers({}, r, [])
    expect(e['model-a'].context_tiers).toEqual([])
    e = setTiers(e, r, [
      { ...emptyTierDraft(), threshold_tokens: '0', model_ratio: '1' },
    ])
    expect(e).toEqual({})
  })
})

describe('buildBatch', () => {
  it('sends numbers, and nothing for blank or untouched fields', () => {
    const batch = buildBatch({
      'model-a': { model_ratio: '3.5', completion_ratio: '' },
      'model-b': { cache_ratio: ' ' },
      'model-c': { model_price: '0.25' },
    })
    expect(batch).toEqual([
      { model_name: 'model-a', model_ratio: 3.5 },
      { model_name: 'model-c', model_price: 0.25 },
    ])
  })

  it('threshold defaults to 0 on blank and blank tier ratios are omitted', () => {
    const batch = buildBatch({
      'model-a': {
        context_tiers: [
          { ...emptyTierDraft(), model_ratio: '2' },
          { ...emptyTierDraft(), threshold_tokens: '5000', cache_ratio: '0.1' },
        ],
      },
    })
    expect(batch).toEqual([
      {
        model_name: 'model-a',
        context_tiers: [
          { threshold_tokens: 0, model_ratio: 2 },
          { threshold_tokens: 5000, cache_ratio: 0.1 },
        ],
      },
    ])
  })

  it('an explicit empty tier list is kept: it clears the model tiers', () => {
    expect(buildBatch({ 'model-a': { context_tiers: [] } })).toEqual([
      { model_name: 'model-a', context_tiers: [] },
    ])
  })

  it('an invalid number is never sent as NaN', () => {
    const batch = buildBatch({ 'model-a': { model_ratio: 'abc' } })
    expect(batch).toEqual([])
    expect(JSON.stringify(batch)).not.toContain('null')
  })
})

describe('validateEdit (mirrors the server)', () => {
  it('ratios and prices must be > 0 when typed, blank is allowed', () => {
    expect(validateEdit({ model_ratio: '0' })).toEqual({ model_ratio: 'positive' })
    expect(validateEdit({ completion_ratio: '-1' })).toEqual({ completion_ratio: 'positive' })
    expect(validateEdit({ model_price: 'abc' })).toEqual({ model_price: 'positive' })
    expect(validateEdit({ cache_ratio: 'Infinity' })).toEqual({ cache_ratio: 'positive' })
    expect(validateEdit({ model_ratio: '', cache_ratio: '0.1' })).toEqual({})
  })

  it('tier thresholds are whole, non-negative and strictly ascending', () => {
    const tiers = (...th: string[]) =>
      th.map((threshold_tokens) => ({ ...emptyTierDraft(), threshold_tokens }))
    expect(validateEdit({ context_tiers: tiers('0', '100') })).toEqual({})
    expect(validateEdit({ context_tiers: tiers('', '100') })).toEqual({})
    expect(validateEdit({ context_tiers: tiers('100', '100') })).toEqual({
      [tierErrorKey(1, 'threshold_tokens')]: 'ascending',
    })
    expect(validateEdit({ context_tiers: tiers('100', '50') })).toEqual({
      [tierErrorKey(1, 'threshold_tokens')]: 'ascending',
    })
    expect(validateEdit({ context_tiers: tiers('-5') })).toEqual({
      [tierErrorKey(0, 'threshold_tokens')]: 'threshold',
    })
    expect(validateEdit({ context_tiers: tiers('1.5') })).toEqual({
      [tierErrorKey(0, 'threshold_tokens')]: 'threshold',
    })
  })

  it('tier ratios must be > 0 and the list has a cap', () => {
    expect(
      validateEdit({ context_tiers: [{ ...emptyTierDraft(), model_ratio: '0' }] })
    ).toEqual({ [tierErrorKey(0, 'model_ratio')]: 'positive' })
    const many = Array.from({ length: MAX_TIERS + 1 }, (_, i) => ({
      ...emptyTierDraft(),
      threshold_tokens: String(i),
    }))
    expect(validateEdit({ context_tiers: many })).toEqual({ tiers: 'too-many-tiers' })
  })

  it('validateAll reports only the broken models', () => {
    expect(
      Object.keys(
        validateAll({ good: { model_ratio: '1' }, bad: { model_ratio: '0' } })
      )
    ).toEqual(['bad'])
  })
})

describe('diffs', () => {
  it('normalizes a preview response and tolerates garbage', () => {
    expect(normalizeDiffs(undefined)).toEqual([])
    expect(normalizeDiffs([null, 3, { model_name: 'a', field: 'model_ratio', old: 1, new: 2, old_explicit: false }])).toEqual([
      { model_name: 'a', field: 'model_ratio', old: 1, new: 2, old_explicit: false },
    ])
  })

  it('reads tier lists out of old/new, null meaning no tiers', () => {
    expect(tiersFromDiff(null)).toEqual([])
    expect(tiersFromDiff([{ threshold_tokens: 7, model_ratio: 2 }])).toEqual([
      { threshold_tokens: 7, model_ratio: 2 },
    ])
  })
})

describe('draft storage', () => {
  beforeEach(() => window.localStorage.clear())

  it('keys are per user and share the legacy layout', () => {
    expect(draftKey(7)).toBe('lurus-hub:draft:7:v2-pricing-edits')
    expect(draftKey(undefined)).toBe('lurus-hub:draft:anon:v2-pricing-edits')
  })

  it('round-trips and removes the slot when empty', () => {
    const key = draftKey(1)
    saveDraft(key, { 'model-a': { model_ratio: '3' } })
    expect(loadDraft(key)).toEqual({ 'model-a': { model_ratio: '3' } })
    saveDraft(key, {})
    expect(window.localStorage.getItem(key)).toBeNull()
  })

  it('ignores a wrong schema, an expired or a corrupt envelope', () => {
    const key = draftKey(1)
    window.localStorage.setItem(key, JSON.stringify({ v: 2, t: Date.now(), d: { a: { model_ratio: '1' } } }))
    expect(loadDraft(key)).toEqual({})
    window.localStorage.setItem(
      key,
      JSON.stringify({ v: 1, t: Date.now() - DRAFT_TTL_MS - 1, d: { a: { model_ratio: '1' } } })
    )
    expect(loadDraft(key)).toEqual({})
    expect(window.localStorage.getItem(key)).toBeNull()
    window.localStorage.setItem(key, '{not json')
    expect(loadDraft(key)).toEqual({})
  })

  it('accepts a legacy draft (numbers) and rebuilds trustworthy text', () => {
    expect(
      sanitizeEdits({
        'model-a': {
          model_ratio: 3,
          junk: 'x',
          context_tiers: [{ threshold_tokens: 0, model_ratio: 1 }],
        },
        'model-b': 'nope',
        'model-c': {},
      })
    ).toEqual({
      'model-a': {
        model_ratio: '3',
        context_tiers: [
          { threshold_tokens: '0', model_ratio: '1', completion_ratio: '', cache_ratio: '' },
        ],
      },
    })
    expect(sanitizeEdits([1, 2])).toEqual({})
  })
})
