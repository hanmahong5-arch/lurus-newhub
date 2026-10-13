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
import { z } from 'zod'

export const FILTER_ALL = 'all'

/** Groups that are placeholders, not something a person picks. */
export const EXCLUDED_GROUPS = ['', 'auto']

export const SORTS = ['name', 'price-low', 'price-high'] as const
export type SortOption = (typeof SORTS)[number]

export const VIEW_MODES = ['card', 'table'] as const
export type ViewMode = (typeof VIEW_MODES)[number]

export type TokenUnit = 'M' | 'K'

export const PRICING_TYPES = ['token', 'call', 'search'] as const

export const PAGE_SIZE = 20

/**
 * URL state of the price list and of the detail page that links back to it.
 * Every key is optional and absent means the default, so a bare /pricing is
 * the unfiltered list.
 */
export interface PricingSearch {
  search?: string
  sort?: SortOption
  vendor?: string
  group?: string
  type?: (typeof PRICING_TYPES)[number]
  endpoint?: string
  modality?: string
  tag?: string
  unit?: TokenUnit
  view?: ViewMode
}

// A hand-typed `?search=123` reaches the router as a number; read it as text.
const text = z
  .preprocess((v) => (typeof v === 'number' ? String(v) : v), z.string())
  .optional()
  .catch(undefined)

const schema = z.object({
  search: text,
  sort: z.enum(SORTS).optional().catch(undefined),
  vendor: text,
  group: text,
  type: z.enum(PRICING_TYPES).optional().catch(undefined),
  endpoint: text,
  modality: text,
  tag: text,
  unit: z.enum(['M', 'K']).optional().catch(undefined),
  view: z.enum(VIEW_MODES).optional().catch(undefined),
})

/**
 * Route `validateSearch`: unknown or malformed values fall back to "absent"
 * instead of failing the route, so a stale or hand-edited link still opens.
 */
export function validatePricingSearch(
  raw: Record<string, unknown>
): PricingSearch {
  const parsed = schema.parse(raw) as PricingSearch
  // Keep the URL clean: drop keys that came back undefined.
  const out: PricingSearch = {}
  for (const [key, value] of Object.entries(parsed)) {
    if (value !== undefined && value !== '') {
      ;(out as Record<string, unknown>)[key] = value
    }
  }
  return out
}
