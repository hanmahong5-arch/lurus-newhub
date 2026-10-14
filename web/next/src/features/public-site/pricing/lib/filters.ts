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
import { FILTER_ALL, type PricingSearch, type TokenUnit } from '../constants'
import {
  groupMultiplier,
  pricingTypeOf,
  priceView,
  sortPrice,
  type PublicModel,
} from './model'

export interface Facet {
  value: string
  count: number
}

function countBy(values: Iterable<string>): Facet[] {
  const counts = new Map<string, number>()
  for (const v of values) counts.set(v, (counts.get(v) ?? 0) + 1)
  return [...counts]
    .map(([value, count]) => ({ value, count }))
    .sort((a, b) => b.count - a.count || a.value.localeCompare(b.value))
}

/** Option lists for the filter sidebar, counted over the whole catalogue. */
export function buildFacets(models: PublicModel[]) {
  const unique = (list: string[]) => [...new Set(list)]
  return {
    vendors: countBy(models.flatMap((m) => (m.vendor ? [m.vendor] : []))),
    tags: countBy(
      models.flatMap((m) => unique(m.tags.map((t) => t.toLowerCase())))
    ),
    endpoints: countBy(models.flatMap((m) => unique(m.endpoints))),
    modalities: countBy(models.flatMap((m) => (m.modality ? [m.modality] : []))),
    types: countBy(models.map((m) => pricingTypeOf(m.entry))),
    groups: countBy(models.flatMap((m) => m.groups)),
  }
}

export function matchesSearch(model: PublicModel, query: string): boolean {
  const q = query.trim().toLowerCase()
  if (!q) return true
  const hay = [
    model.id,
    model.vendor,
    model.description,
    model.modality,
    ...model.tags,
    ...model.endpoints,
  ]
    .join(' ')
    .toLowerCase()
  return hay.includes(q)
}

/** Apply the URL filters, then the sort. Pure; the input is not reordered. */
export function applyFilters(
  models: PublicModel[],
  search: PricingSearch,
  unit: TokenUnit = 'M'
): PublicModel[] {
  const group = search.group ?? FILTER_ALL
  const tag = search.tag?.toLowerCase()
  const filtered = models.filter((m) => {
    if (!matchesSearch(m, search.search ?? '')) return false
    if (search.vendor && m.vendor !== search.vendor) return false
    if (group !== FILTER_ALL && !m.groups.includes(group)) return false
    if (search.type && pricingTypeOf(m.entry) !== search.type) return false
    if (search.endpoint && !m.endpoints.includes(search.endpoint)) return false
    if (search.modality && m.modality !== search.modality) return false
    if (tag && !m.tags.some((t) => t.toLowerCase() === tag)) return false
    return true
  })

  const sort = search.sort ?? 'name'
  const byName = (a: PublicModel, b: PublicModel) => a.id.localeCompare(b.id)
  if (sort === 'name') return filtered.sort(byName)

  const keyed = filtered.map((m) => ({
    m,
    price: sortPrice(priceView(m, groupMultiplier(m, group), unit)),
  }))
  keyed.sort((a, b) => {
    // Models without a price go last whichever way the order runs.
    if (a.price === null && b.price === null) return byName(a.m, b.m)
    if (a.price === null) return 1
    if (b.price === null) return -1
    const diff = sort === 'price-low' ? a.price - b.price : b.price - a.price
    return diff || byName(a.m, b.m)
  })
  return keyed.map((k) => k.m)
}

export const FILTER_KEYS = [
  'vendor',
  'group',
  'type',
  'endpoint',
  'modality',
  'tag',
] as const

/** How many of the sidebar filters are set (the search box is not one). */
export function activeFilterCount(search: PricingSearch): number {
  return FILTER_KEYS.filter((k) => search[k] !== undefined).length
}
