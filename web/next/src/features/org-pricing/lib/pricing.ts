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
 * Pure model of the pricing sheet: server shapes, the local edit buffer, the
 * validation that mirrors the server's, and the batch the write routes take.
 * No React, no I/O.
 */

/** Server cap on tiers per model (ratio_setting.maxContextTiersPerModel). */
export const MAX_TIERS = 8

/** Scalar fields an admin can edit per model. */
export type ScalarField =
  | 'model_ratio'
  | 'completion_ratio'
  | 'model_price'
  | 'cache_ratio'

export const SCALAR_FIELDS: readonly ScalarField[] = [
  'model_ratio',
  'completion_ratio',
  'model_price',
  'cache_ratio',
]

/** The ratio overrides a tier may carry. */
export type TierRatioField = 'model_ratio' | 'completion_ratio' | 'cache_ratio'

export const TIER_RATIO_FIELDS: readonly TierRatioField[] = [
  'model_ratio',
  'completion_ratio',
  'cache_ratio',
]

export interface ContextTier {
  threshold_tokens: number
  model_ratio?: number
  completion_ratio?: number
  cache_ratio?: number
}

export interface PricingRow {
  model_name: string
  vendor: string
  /** 0 = ratio based (per token), 1 = per-call price. */
  quota_type: number
  model_ratio: number
  completion_ratio: number
  model_price: number
  /** false = nobody priced this model: its ratio is 0 and means "not priced". */
  price_configured: boolean
  cache_ratio?: number
  context_tiers: ContextTier[]
  enable_groups: string[]
  modality?: string
  usage_unit?: string
  /** undefined = not configured, 0 = explicitly free. */
  search_unit_price?: number
}

export interface PricingSheet {
  pricing: PricingRow[]
  vendors: string[]
  groupRatio: Record<string, number>
  /** Optimistic-lock token; sent back as If-Match-Pricing-Version. */
  version: number
}

/** A tier as typed: every number is kept as the text in its input. */
export interface TierDraft {
  threshold_tokens: string
  model_ratio: string
  completion_ratio: string
  cache_ratio: string
}

/** Everything the admin changed on one model. Absent key = untouched. */
export interface RowEdit {
  model_ratio?: string
  completion_ratio?: string
  model_price?: string
  cache_ratio?: string
  context_tiers?: TierDraft[]
}

export type Edits = Record<string, RowEdit>

/** One item of the write/preview request body. */
export interface BatchItem {
  model_name: string
  model_ratio?: number
  completion_ratio?: number
  model_price?: number
  cache_ratio?: number
  context_tiers?: ContextTier[]
}

export interface DiffEntry {
  model_name: string
  field: string
  old: unknown
  new: unknown
  /** false = the old value came from a fallback, not an explicit entry. */
  old_explicit?: boolean
}

function num(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

function normalizeTier(raw: unknown): ContextTier {
  const r = (raw ?? {}) as Record<string, unknown>
  const tier: ContextTier = { threshold_tokens: num(r.threshold_tokens) ?? 0 }
  for (const f of TIER_RATIO_FIELDS) {
    const v = num(r[f])
    if (v !== undefined) tier[f] = v
  }
  return tier
}

function normalizeRow(raw: unknown): PricingRow | null {
  const r = (raw ?? {}) as Record<string, unknown>
  if (typeof r.model_name !== 'string' || r.model_name === '') return null
  const row: PricingRow = {
    model_name: r.model_name,
    vendor: typeof r.vendor === 'string' ? r.vendor : '',
    quota_type: r.quota_type === 1 ? 1 : 0,
    model_ratio: num(r.model_ratio) ?? 0,
    completion_ratio: num(r.completion_ratio) ?? 0,
    model_price: num(r.model_price) ?? 0,
    // Only an explicit false marks a model unpriced (older servers omit it).
    price_configured: r.price_configured !== false,
    context_tiers: Array.isArray(r.context_tiers)
      ? r.context_tiers.map(normalizeTier)
      : [],
    enable_groups: Array.isArray(r.enable_groups)
      ? r.enable_groups.filter((g): g is string => typeof g === 'string')
      : [],
  }
  const cache = num(r.cache_ratio)
  if (cache !== undefined) row.cache_ratio = cache
  if (typeof r.modality === 'string' && r.modality) row.modality = r.modality
  if (typeof r.usage_unit === 'string' && r.usage_unit) {
    row.usage_unit = r.usage_unit
  }
  const search = num(r.search_unit_price)
  if (search !== undefined) row.search_unit_price = search
  return row
}

/** GET /api/v2/~/pricing `data` -> sheet. Tolerates absent members. */
export function normalizeSheet(raw: unknown): PricingSheet {
  const d = (raw ?? {}) as Record<string, unknown>
  const groupRatio: Record<string, number> = {}
  if (d.group_ratio && typeof d.group_ratio === 'object') {
    for (const [k, v] of Object.entries(d.group_ratio)) {
      const n = num(v)
      if (n !== undefined) groupRatio[k] = n
    }
  }
  return {
    pricing: (Array.isArray(d.pricing) ? d.pricing : [])
      .map(normalizeRow)
      .filter((r): r is PricingRow => r !== null),
    vendors: Array.isArray(d.vendors)
      ? d.vendors.filter((v): v is string => typeof v === 'string' && v !== '')
      : [],
    groupRatio,
    version: typeof d.version === 'number' ? d.version : 0,
  }
}

export function isUnpriced(row: PricingRow): boolean {
  return !row.price_configured
}

/**
 * The text a field starts with when it has no edit. An unpriced model's
 * ratio/price is blank (the server sends 0, which is not a price); a model
 * without a cache ratio starts blank rather than fabricated.
 */
export function baselineValue(row: PricingRow, field: ScalarField): string {
  switch (field) {
    case 'model_ratio':
      return isUnpriced(row) ? '' : String(row.model_ratio)
    case 'completion_ratio':
      return String(row.completion_ratio)
    case 'model_price':
      return isUnpriced(row) ? '' : String(row.model_price)
    case 'cache_ratio':
      return row.cache_ratio === undefined ? '' : String(row.cache_ratio)
  }
}

export function fieldValue(
  row: PricingRow,
  edit: RowEdit | undefined,
  field: ScalarField
): string {
  return edit?.[field] ?? baselineValue(row, field)
}

export function tierToDraft(tier: ContextTier): TierDraft {
  const draft = emptyTierDraft()
  draft.threshold_tokens = String(tier.threshold_tokens)
  for (const f of TIER_RATIO_FIELDS) {
    const v = tier[f]
    if (v !== undefined) draft[f] = String(v)
  }
  return draft
}

export function emptyTierDraft(): TierDraft {
  return {
    threshold_tokens: '',
    model_ratio: '',
    completion_ratio: '',
    cache_ratio: '',
  }
}

export function tiersOf(row: PricingRow, edit?: RowEdit): TierDraft[] {
  return edit?.context_tiers ?? row.context_tiers.map(tierToDraft)
}

function sameTiers(a: TierDraft[], b: TierDraft[]): boolean {
  return JSON.stringify(a) === JSON.stringify(b)
}

function withModel(edits: Edits, model: string, edit: RowEdit): Edits {
  const next = { ...edits }
  if (Object.keys(edit).length === 0) delete next[model]
  else next[model] = edit
  return next
}

export function setScalar(
  edits: Edits,
  model: string,
  field: ScalarField,
  value: string
): Edits {
  return withModel(edits, model, { ...edits[model], [field]: value })
}

/** A tier list identical to the server's is not an edit. */
export function setTiers(
  edits: Edits,
  row: PricingRow,
  tiers: TierDraft[]
): Edits {
  const edit: RowEdit = { ...edits[row.model_name] }
  if (sameTiers(tiers, row.context_tiers.map(tierToDraft))) {
    delete edit.context_tiers
  } else {
    edit.context_tiers = tiers
  }
  return withModel(edits, row.model_name, edit)
}

export function clearModel(edits: Edits, model: string): Edits {
  const next = { ...edits }
  delete next[model]
  return next
}

/** Field-level problems: key is a ScalarField, `tiers`, or `tier:<i>:<field>`. */
export type ValidationCode =
  | 'positive'
  | 'threshold'
  | 'ascending'
  | 'too-many-tiers'

export type EditErrors = Record<string, ValidationCode>

export function tierErrorKey(index: number, field: string): string {
  return `tier:${index}:${field}`
}

/** Blank = "leave to the server's default"; anything typed must be > 0. */
function parseRatio(text: string): number | undefined | 'invalid' {
  const trimmed = text.trim()
  if (trimmed === '') return undefined
  const n = Number(trimmed)
  return Number.isFinite(n) && n > 0 ? n : 'invalid'
}

function parseThreshold(text: string): number | 'invalid' {
  const trimmed = text.trim()
  if (trimmed === '') return 0
  const n = Number(trimmed)
  return Number.isInteger(n) && n >= 0 ? n : 'invalid'
}

/** Mirrors validatePricingBatch / ValidateContextTierList on the server. */
export function validateEdit(edit: RowEdit): EditErrors {
  const errors: EditErrors = {}
  for (const f of SCALAR_FIELDS) {
    const v = edit[f]
    if (v !== undefined && parseRatio(v) === 'invalid') errors[f] = 'positive'
  }
  const tiers = edit.context_tiers
  if (tiers) {
    if (tiers.length > MAX_TIERS) errors['tiers'] = 'too-many-tiers'
    let prev = -1
    tiers.forEach((tier, i) => {
      const threshold = parseThreshold(tier.threshold_tokens)
      if (threshold === 'invalid') {
        errors[tierErrorKey(i, 'threshold_tokens')] = 'threshold'
      } else {
        if (threshold <= prev) {
          errors[tierErrorKey(i, 'threshold_tokens')] = 'ascending'
        }
        prev = threshold
      }
      for (const f of TIER_RATIO_FIELDS) {
        if (parseRatio(tier[f]) === 'invalid') {
          errors[tierErrorKey(i, f)] = 'positive'
        }
      }
    })
  }
  return errors
}

export function validateAll(edits: Edits): Record<string, EditErrors> {
  const out: Record<string, EditErrors> = {}
  for (const [model, edit] of Object.entries(edits)) {
    const errs = validateEdit(edit)
    if (Object.keys(errs).length > 0) out[model] = errs
  }
  return out
}

/**
 * The request body: only models with at least one real change. A blank scalar
 * is "no change" (never NaN/0 on the wire); a tier list is sent whole, and an
 * explicit [] is the server's "clear this model's tiers" signal. Tier
 * threshold defaults to 0 on blank; blank tier ratios are omitted so the
 * server falls through to the flat ratio instead of zeroing it.
 */
export function buildBatch(edits: Edits): BatchItem[] {
  const items: BatchItem[] = []
  for (const [model, edit] of Object.entries(edits)) {
    const item: BatchItem = { model_name: model }
    for (const f of SCALAR_FIELDS) {
      const v = edit[f]
      if (v === undefined) continue
      const parsed = parseRatio(v)
      if (typeof parsed === 'number') item[f] = parsed
    }
    if (edit.context_tiers !== undefined) {
      item.context_tiers = edit.context_tiers.map((t) => {
        const tier: ContextTier = {
          threshold_tokens: Number.parseInt(t.threshold_tokens, 10) || 0,
        }
        for (const f of TIER_RATIO_FIELDS) {
          const parsed = parseRatio(t[f])
          if (typeof parsed === 'number') tier[f] = parsed
        }
        return tier
      })
    }
    if (Object.keys(item).length > 1) items.push(item)
  }
  return items
}

export function normalizeDiffs(raw: unknown): DiffEntry[] {
  if (!Array.isArray(raw)) return []
  return raw
    .filter(
      (d): d is Record<string, unknown> => typeof d === 'object' && d !== null
    )
    .map((d) => ({
      model_name: String(d.model_name ?? ''),
      field: String(d.field ?? ''),
      old: d.old,
      new: d.new,
      old_explicit:
        typeof d.old_explicit === 'boolean' ? d.old_explicit : undefined,
    }))
}

/** A tier list from a diff `old`/`new` value (nil / [] = no tiers). */
export function tiersFromDiff(value: unknown): ContextTier[] {
  return Array.isArray(value) ? value.map(normalizeTier) : []
}
