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

import type { PublicPricingPayload, PublicPricingRow } from '../../api'
import { applyFilters, buildFacets } from './filters'
import {
  buildPublicModels,
  formatAmount,
  groupMultiplier,
  pricingTypeOf,
  priceView,
} from './model'
import { buildSample, endpointRows, sampleBody } from './samples'

// 1 ratio unit = $2 per 1M tokens in these fixtures.
const USD_PER_RATIO = 2

function row(over: Partial<PublicPricingRow>): PublicPricingRow {
  return {
    model_name: 'model-a',
    quota_type: 0,
    model_ratio: 1,
    completion_ratio: 3,
    enable_groups: ['default', 'vip'],
    supported_endpoint_types: ['openai'],
    price_configured: true,
    ...over,
  }
}

function payload(rows: PublicPricingRow[]): PublicPricingPayload {
  return {
    rows,
    vendors: [
      { id: 1, name: 'Vendor A', icon: 'VendorA' },
      { id: 2, name: 'Vendor B' },
    ],
    groupRatio: { default: 1, vip: 0.5 },
    usableGroup: { default: 'Default', vip: 'VIP' },
    endpoints: {},
  }
}

const find = (models: ReturnType<typeof buildPublicModels>, id: string) => {
  const m = models.find((x) => x.id === id)
  if (!m) throw new Error(`model ${id} missing`)
  return m
}

describe('buildPublicModels', () => {
  it('prices tokens from the unit price and the lowest group ratio', () => {
    const models = buildPublicModels(
      payload([row({ model_name: 'model-a', vendor_id: 1 })]),
      USD_PER_RATIO
    )
    const m = find(models, 'model-a')
    expect(m.vendor).toBe('Vendor A')
    expect(m.vendorIcon).toBe('VendorA')
    expect(m.groups).toEqual(['default', 'vip'])
    // best price = vip (x0.5): input $1 / 1M, output = x3 completion ratio.
    const best = priceView(m, groupMultiplier(m), 'M')
    expect(best).toMatchObject({ kind: 'token', input: 1, output: 3 })
    // A named group shows that group's price.
    expect(priceView(m, groupMultiplier(m, 'default'), 'M')).toMatchObject({
      kind: 'token',
      input: 2,
      output: 6,
    })
    // Per 1K is a thousandth.
    expect(priceView(m, 1, 'K')).toMatchObject({ input: 0.002 })
  })

  it('never turns an unpriced model into a free one', () => {
    const models = buildPublicModels(
      payload([
        row({
          model_name: 'model-unpriced',
          price_configured: false,
          model_ratio: 0,
        }),
      ]),
      USD_PER_RATIO
    )
    expect(priceView(find(models, 'model-unpriced'), 1)).toEqual({
      kind: 'unpriced',
    })
  })

  it('marks token prices unavailable, not zero, when the unit price is unknown', () => {
    const models = buildPublicModels(
      payload([
        row({ model_name: 'model-a' }),
        row({ model_name: 'model-call', quota_type: 1, model_price: 0.25 }),
      ]),
      null
    )
    expect(priceView(find(models, 'model-a'), 1)).toEqual({
      kind: 'unavailable',
    })
    // A per-call price is already dollars and needs no unit price.
    expect(priceView(find(models, 'model-call'), 1)).toEqual({
      kind: 'call',
      price: 0.25,
    })
  })

  it('keeps a configured zero search-unit price distinct from none', () => {
    const models = buildPublicModels(
      payload([
        row({
          model_name: 'search-free',
          usage_unit: 'search_unit',
          search_unit_price: 0,
        }),
        row({
          model_name: 'search-unset',
          usage_unit: 'search_unit',
          search_unit_price: null,
        }),
      ]),
      USD_PER_RATIO
    )
    expect(priceView(find(models, 'search-free'), 1)).toEqual({
      kind: 'search',
      price: 0,
    })
    expect(priceView(find(models, 'search-unset'), 1)).toEqual({
      kind: 'search',
      price: null,
    })
  })

  it('hides the cache column data when the server sends no cache ratio', () => {
    const models = buildPublicModels(payload([row({})]), USD_PER_RATIO)
    expect(priceView(models[0], 1)).toMatchObject({ cache: null })
    const withCache = buildPublicModels(
      payload([row({ cache_ratio: 0.1 })]),
      USD_PER_RATIO
    )
    expect(priceView(withCache[0], 1)).toMatchObject({ cache: 0.2 })
  })

  it('drops groups that have no public ratio', () => {
    const models = buildPublicModels(
      payload([row({ enable_groups: ['vip', 'private', 'auto', ''] })]),
      USD_PER_RATIO
    )
    expect(models[0].groups).toEqual(['vip'])
  })

  it('classifies the pricing type', () => {
    const models = buildPublicModels(
      payload([
        row({ model_name: 'm-token' }),
        row({ model_name: 'm-call', quota_type: 1, model_price: 1 }),
        row({ model_name: 'm-search', usage_unit: 'search_unit' }),
      ]),
      USD_PER_RATIO
    )
    expect(models.map((m) => pricingTypeOf(m.entry))).toEqual([
      'token',
      'call',
      'search',
    ])
  })
})

describe('formatAmount', () => {
  it('reads a configured zero as Free and none as null', () => {
    expect(formatAmount(0, null, 'Free')).toBe('Free')
    expect(formatAmount(null, null, 'Free')).toBeNull()
  })
})

describe('applyFilters', () => {
  const models = buildPublicModels(
    payload([
      row({
        model_name: 'zeta',
        vendor_id: 1,
        model_ratio: 4,
        tags: 'Fast,cheap',
        modality: 'chat',
      }),
      row({
        model_name: 'alpha',
        vendor_id: 2,
        model_ratio: 1,
        enable_groups: ['vip'],
        supported_endpoint_types: ['embeddings'],
        modality: 'embedding',
      }),
      row({
        model_name: 'mid',
        vendor_id: 1,
        price_configured: false,
        model_ratio: 0,
      }),
    ]),
    USD_PER_RATIO
  )
  const ids = (list: typeof models) => list.map((m) => m.id)

  it('filters by vendor, group, endpoint, modality and tag', () => {
    expect(ids(applyFilters(models, { vendor: 'Vendor B' }))).toEqual(['alpha'])
    expect(ids(applyFilters(models, { group: 'default' }))).toEqual([
      'mid',
      'zeta',
    ])
    expect(ids(applyFilters(models, { endpoint: 'embeddings' }))).toEqual([
      'alpha',
    ])
    expect(ids(applyFilters(models, { modality: 'chat' }))).toEqual(['zeta'])
    expect(ids(applyFilters(models, { tag: 'FAST' }))).toEqual(['zeta'])
  })

  it('searches name, vendor and tags', () => {
    expect(ids(applyFilters(models, { search: 'vendor b' }))).toEqual(['alpha'])
    expect(ids(applyFilters(models, { search: 'cheap' }))).toEqual(['zeta'])
    expect(ids(applyFilters(models, { search: 'nothing-matches' }))).toEqual([])
  })

  it('sorts by price with unpriced models last in both directions', () => {
    expect(ids(applyFilters(models, { sort: 'price-low' }))).toEqual([
      'alpha',
      'zeta',
      'mid',
    ])
    expect(ids(applyFilters(models, { sort: 'price-high' }))).toEqual([
      'zeta',
      'alpha',
      'mid',
    ])
  })

  it('does not reorder its input', () => {
    const before = ids(models)
    applyFilters(models, { sort: 'price-high' })
    expect(ids(models)).toEqual(before)
  })

  it('counts facets over the whole catalogue', () => {
    const facets = buildFacets(models)
    expect(facets.vendors).toEqual([
      { value: 'Vendor A', count: 2 },
      { value: 'Vendor B', count: 1 },
    ])
    expect(facets.tags.map((t) => t.value)).toEqual(['cheap', 'fast'])
    expect(facets.types).toEqual([{ value: 'token', count: 3 }])
  })
})

describe('samples', () => {
  it('prefers the endpoint paths the server reports', () => {
    const rows = endpointRows(['openai', 'gemini', 'unknown-kind'], {
      openai: { path: '/custom/chat', method: 'POST' },
    })
    expect(rows[0]).toEqual({
      type: 'openai',
      method: 'POST',
      path: '/custom/chat',
    })
    // Falls back to the built-in table, and skips what it does not know.
    expect(rows[1].path).toBe('/v1beta/models/{model}:generateContent')
    expect(rows).toHaveLength(2)
  })

  it('fills the model into body and path and reads the key from the environment', () => {
    const [generate] = endpointRows(['gemini'], {})
    const curl = buildSample('curl', {
      baseUrl: 'https://gw.example',
      model: 'model-a',
      endpoint: generate,
    })
    expect(curl).toContain(
      'https://gw.example/v1beta/models/model-a:generateContent'
    )
    expect(curl).toContain('$API_KEY')
    expect(curl).not.toContain('{model}')
    expect(sampleBody('embeddings', 'model-e')).toEqual({
      model: 'model-e',
      input: 'Hello',
    })
  })

  it('builds all three languages', () => {
    const [chat] = endpointRows(['openai'], {})
    for (const lang of ['curl', 'python', 'javascript'] as const) {
      const text = buildSample(lang, {
        baseUrl: 'https://gw.example',
        model: 'model-a',
        endpoint: chat,
      })
      expect(text).toContain('https://gw.example/v1/chat/completions')
      expect(text).toContain('model-a')
    }
  })
})
