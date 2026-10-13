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
/**
 * Per-model pricing lives in several option rows, each a JSON object of
 * model name -> number. This module turns them into one row per model for the
 * table editor and back, without ever touching a key it does not own.
 *
 *   ModelRatio            input multiplier (quota per token)
 *   CompletionRatio       output multiplier relative to the input ratio
 *   CacheRatio            cached-input multiplier relative to the input ratio
 *   ImageRatio            image multiplier
 *   AudioRatio            audio-input multiplier
 *   AudioCompletionRatio  audio-output multiplier
 *   ModelPrice            fixed price per request, in USD
 *
 * A model with a ModelPrice is billed per request and the ratios are ignored;
 * a model with none of them is "not priced" - never "free".
 */
export const PRICING_KEYS = [
  'ModelRatio',
  'CompletionRatio',
  'CacheRatio',
  'ModelPrice',
  'ImageRatio',
  'AudioRatio',
  'AudioCompletionRatio',
] as const

export type PricingKey = (typeof PRICING_KEYS)[number]
export type NumberMap = Record<string, number>
export type PricingMaps = Record<PricingKey, NumberMap>

export type PricingMode = 'per-token' | 'per-request'

export type ModelPriceRow = {
  name: string
  mode: PricingMode
  ratio?: number
  completion?: number
  cache?: number
  price?: number
  image?: number
  audio?: number
  audioCompletion?: number
}

type RowField = Exclude<keyof ModelPriceRow, 'name' | 'mode'>

/** Which option row stores which row field. */
export const ROW_FIELD_KEY: Record<RowField, PricingKey> = {
  ratio: 'ModelRatio',
  completion: 'CompletionRatio',
  cache: 'CacheRatio',
  price: 'ModelPrice',
  image: 'ImageRatio',
  audio: 'AudioRatio',
  audioCompletion: 'AudioCompletionRatio',
}

const ROW_FIELDS = Object.keys(ROW_FIELD_KEY) as RowField[]
const TOKEN_FIELDS: RowField[] = [
  'ratio',
  'completion',
  'cache',
  'image',
  'audio',
  'audioCompletion',
]

/** Parse one option row; anything that is not a finite number is dropped. */
export function parseNumberMap(raw: string): NumberMap {
  const text = raw.trim()
  if (text === '') return {}
  try {
    const parsed: unknown = JSON.parse(text)
    if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
      return {}
    }
    const out: NumberMap = {}
    for (const [name, value] of Object.entries(parsed)) {
      if (typeof value === 'number' && Number.isFinite(value)) out[name] = value
    }
    return out
  } catch {
    return {}
  }
}

export function parsePricingMaps(
  read: (key: PricingKey) => string
): PricingMaps {
  const maps = {} as PricingMaps
  for (const key of PRICING_KEYS) maps[key] = parseNumberMap(read(key))
  return maps
}

export function serializeNumberMap(map: NumberMap): string {
  return JSON.stringify(map, null, 2)
}

/** One row per model that has at least one pricing entry, sorted by name. */
export function buildRows(maps: PricingMaps): ModelPriceRow[] {
  const names = new Set<string>()
  for (const key of PRICING_KEYS) {
    for (const name of Object.keys(maps[key])) names.add(name)
  }
  return [...names]
    .sort((a, b) => a.localeCompare(b))
    .map((name) => {
      const row: ModelPriceRow = {
        name,
        mode: name in maps.ModelPrice ? 'per-request' : 'per-token',
      }
      for (const field of ROW_FIELDS) {
        const key = ROW_FIELD_KEY[field]
        if (name in maps[key]) row[field] = maps[key][name]
      }
      return row
    })
}

/** Stable text for "did this model's pricing change". */
export function rowSignature(row: ModelPriceRow | undefined): string {
  if (!row) return ''
  return JSON.stringify([
    row.mode,
    ...ROW_FIELDS.map((field) => row[field] ?? null),
  ])
}

function cloneMaps(maps: PricingMaps): PricingMaps {
  const out = {} as PricingMaps
  for (const key of PRICING_KEYS) out[key] = { ...maps[key] }
  return out
}

/**
 * Store a row. Switching mode clears the other mode's entries so a model never
 * carries both a fixed price and ratios. A rename moves the entries.
 */
export function applyRow(
  maps: PricingMaps,
  row: ModelPriceRow,
  previousName?: string
): PricingMaps {
  const next = cloneMaps(maps)
  if (previousName !== undefined && previousName !== row.name) {
    for (const key of PRICING_KEYS) delete next[key][previousName]
  }
  const fields = row.mode === 'per-request' ? (['price'] as RowField[]) : TOKEN_FIELDS
  for (const field of ROW_FIELDS) {
    const key = ROW_FIELD_KEY[field]
    const value = fields.includes(field) ? row[field] : undefined
    if (value === undefined) delete next[key][row.name]
    else next[key][row.name] = value
  }
  return next
}

export function removeModel(maps: PricingMaps, name: string): PricingMaps {
  const next = cloneMaps(maps)
  for (const key of PRICING_KEYS) delete next[key][name]
  return next
}

/** Keys whose map differs between two sets, so only those are written. */
export function changedKeys(before: PricingMaps, after: PricingMaps): PricingKey[] {
  return PRICING_KEYS.filter(
    (key) => JSON.stringify(before[key]) !== JSON.stringify(after[key])
  )
}

/** Every value of a ratio / price map must be a non-negative number. */
export function validateNumberMap(parsed: unknown): string | null {
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
    return 'Expected a JSON object'
  }
  for (const value of Object.values(parsed)) {
    if (typeof value !== 'number' || !Number.isFinite(value)) {
      return 'Every value must be a number'
    }
    if (value < 0) return 'Values cannot be negative'
  }
  return null
}
