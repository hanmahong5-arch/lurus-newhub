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
import {
  buildCatalog,
  formatPrice,
  type ModelEntry,
  type PricingRow,
} from '@/features/models/lib/catalog'
import type { MoneyConfig } from '@/lib/money'

import type { PublicPricingPayload } from '../../api'
import { EXCLUDED_GROUPS, FILTER_ALL, type TokenUnit } from '../constants'

/**
 * One row of the public price list, ready to show.
 *
 * Money comes only from the shared catalogue helpers (lib/money via
 * `usdPerRatio`); nothing here re-derives a price from status.price or the
 * exchange rate. `entry` is priced at group ratio 1: a group's price is the
 * entry's price times that group's ratio (see `groupMultiplier`).
 */
export interface PublicModel {
  id: string
  /** '' = unknown. */
  vendor: string
  vendorIcon: string
  icon: string
  description: string
  tags: string[]
  endpoints: string[]
  /** '' = unknown. */
  modality: string
  /** Groups this model is offered in AND that have a public ratio. */
  groups: string[]
  ratios: Record<string, number>
  /**
   * Token prices cannot be computed because /api/status gave no unit price.
   * The catalogue still lists the model; its price reads "unavailable".
   */
  priceUnavailable: boolean
  entry: ModelEntry
}

export type PricingType = 'token' | 'call' | 'search'

export function pricingTypeOf(entry: ModelEntry): PricingType {
  if (entry.usageUnit === 'search_unit') return 'search'
  return entry.quotaType === 1 ? 'call' : 'token'
}

function splitTags(raw: string | undefined): string[] {
  if (!raw) return []
  const seen = new Set<string>()
  for (const part of raw.split(/[,;|\s]+/)) {
    const tag = part.trim()
    if (tag) seen.add(tag)
  }
  return [...seen]
}

/** Join the rows with their vendor and the group ratios an anonymous caller sees. */
export function buildPublicModels(
  payload: PublicPricingPayload,
  usdPerRatio: number | null
): PublicModel[] {
  const vendors = new Map(payload.vendors.map((v) => [v.id, v]))
  const rows: PricingRow[] = payload.rows.map((row) => {
    const vendor = row.vendor_id ? vendors.get(row.vendor_id) : undefined
    return {
      ...row,
      vendor: vendor?.name || row.owner_by || undefined,
      model_ratio: row.model_ratio ?? null,
      model_price: row.model_price ?? null,
    }
  })
  // Group ratio 1 on purpose: the per-group multiplier is applied at display.
  const entries = buildCatalog({
    pricing: rows,
    groupRatio: 1,
    usdPerRatio: usdPerRatio ?? 0,
  })
  const byId = new Map(entries.map((e) => [e.id, e]))

  const models: PublicModel[] = []
  for (const row of payload.rows) {
    const entry = byId.get(row.model_name)
    if (!entry) continue
    const vendor = row.vendor_id ? vendors.get(row.vendor_id) : undefined
    const ratios: Record<string, number> = {}
    for (const group of row.enable_groups ?? []) {
      if (EXCLUDED_GROUPS.includes(group)) continue
      const ratio = payload.groupRatio[group]
      if (typeof ratio === 'number' && Number.isFinite(ratio)) {
        ratios[group] = ratio
      }
    }
    models.push({
      id: row.model_name,
      vendor: entry.vendor,
      vendorIcon: vendor?.icon ?? '',
      icon: row.icon ?? '',
      description: entry.description || vendor?.description || '',
      tags: splitTags(row.tags),
      endpoints: entry.capabilities,
      modality: entry.modality,
      groups: Object.keys(ratios),
      ratios,
      priceUnavailable: usdPerRatio === null && pricingTypeOf(entry) === 'token',
      entry,
    })
  }
  return models
}

/**
 * Multiplier for the price shown. A chosen group shows that group's price;
 * otherwise the best (lowest) price any of the model's groups offers.
 */
export function groupMultiplier(model: PublicModel, group = FILTER_ALL): number {
  const chosen = group === FILTER_ALL ? undefined : model.ratios[group]
  if (chosen !== undefined) return chosen
  const all = Object.values(model.ratios)
  return all.length > 0 ? Math.min(...all) : 1
}

const UNIT_DIVISOR: Record<TokenUnit, number> = { M: 1, K: 1000 }

/** USD figures for display; null = not configured, never 0 by default. */
export type PriceView =
  | { kind: 'unpriced' }
  | { kind: 'unavailable' }
  | { kind: 'search'; price: number | null }
  | { kind: 'call'; price: number | null }
  | {
      kind: 'token'
      input: number
      /** null with `outputBilled` false = this model bills no output. */
      output: number | null
      outputBilled: boolean
      /** null = the server sent no cache price: the column stays hidden. */
      cache: number | null
    }

export function priceView(
  model: PublicModel,
  multiplier: number,
  unit: TokenUnit = 'M'
): PriceView {
  const e = model.entry
  if (!e.priced) return { kind: 'unpriced' }
  if (model.priceUnavailable) return { kind: 'unavailable' }
  const type = pricingTypeOf(e)
  if (type === 'search') {
    return {
      kind: 'search',
      price: e.searchUnitPrice === null ? null : e.searchUnitPrice * multiplier,
    }
  }
  if (type === 'call') {
    return {
      kind: 'call',
      price: e.perCall === null ? null : e.perCall * multiplier,
    }
  }
  if (e.inputPerM === null) return { kind: 'unpriced' }
  const scale = multiplier / UNIT_DIVISOR[unit]
  return {
    kind: 'token',
    input: e.inputPerM * scale,
    output: e.outputPerM === null ? null : e.outputPerM * scale,
    outputBilled: !e.inputOnly,
    cache: e.cacheReadPerM === null ? null : e.cacheReadPerM * scale,
  }
}

/** The one number models are ordered by; null sorts last. */
export function sortPrice(view: PriceView): number | null {
  switch (view.kind) {
    case 'token':
      return view.input
    case 'call':
    case 'search':
      return view.price
    default:
      return null
  }
}

/**
 * Amount as text in the operator's display currency. `unpriced` is decided by
 * the caller (null); a configured zero reads "Free", not a dash.
 */
export function formatAmount(
  usd: number | null,
  config: MoneyConfig | null,
  freeLabel: string
): string | null {
  if (usd === null) return null
  if (usd === 0) return freeLabel
  return formatPrice(usd, config)
}
