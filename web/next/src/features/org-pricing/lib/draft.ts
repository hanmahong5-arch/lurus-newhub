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
import type { Edits, RowEdit, TierDraft } from './pricing'

/**
 * Unsaved price edits survive a refresh (the legacy console kept them in
 * `lurus-hub:draft:<user>:v2-pricing-edits`; the key and envelope are the
 * same, so a draft started there is picked up here). Drafts are namespaced by
 * user id so a second account on the same browser never sees them.
 */
export const DRAFT_PREFIX = 'lurus-hub:draft:'
export const DRAFT_NAME = 'v2-pricing-edits'
export const DRAFT_SCHEMA_VERSION = 1
export const DRAFT_TTL_MS = 7 * 24 * 60 * 60 * 1000

export function draftKey(userId: number | string | undefined): string {
  return `${DRAFT_PREFIX}${userId ?? 'anon'}:${DRAFT_NAME}`
}

const SCALARS = [
  'model_ratio',
  'completion_ratio',
  'model_price',
  'cache_ratio',
] as const
const TIER_KEYS = [
  'threshold_tokens',
  'model_ratio',
  'completion_ratio',
  'cache_ratio',
] as const

/** Stored numbers (legacy drafts) and strings both become the typed text. */
function text(value: unknown): string | undefined {
  if (typeof value === 'string') return value
  if (typeof value === 'number' && Number.isFinite(value)) return String(value)
  return undefined
}

function cleanTier(raw: unknown): TierDraft {
  const r = (raw ?? {}) as Record<string, unknown>
  const tier: TierDraft = {
    threshold_tokens: '',
    model_ratio: '',
    completion_ratio: '',
    cache_ratio: '',
  }
  for (const k of TIER_KEYS) tier[k] = text(r[k]) ?? ''
  return tier
}

/** Rebuild a trustworthy Edits from untrusted stored JSON. */
export function sanitizeEdits(raw: unknown): Edits {
  const out: Edits = {}
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return out
  for (const [model, value] of Object.entries(raw)) {
    if (!value || typeof value !== 'object') continue
    const v = value as Record<string, unknown>
    const edit: RowEdit = {}
    for (const f of SCALARS) {
      const t = text(v[f])
      if (t !== undefined) edit[f] = t
    }
    if (Array.isArray(v.context_tiers)) {
      edit.context_tiers = v.context_tiers.map(cleanTier)
    }
    if (Object.keys(edit).length > 0) out[model] = edit
  }
  return out
}

export function loadDraft(key: string): Edits {
  try {
    const raw = window.localStorage.getItem(key)
    if (!raw) return {}
    const envelope = JSON.parse(raw) as { v?: unknown; d?: unknown; t?: unknown }
    if (envelope.v !== DRAFT_SCHEMA_VERSION) return {}
    if (typeof envelope.t !== 'number' || Date.now() - envelope.t > DRAFT_TTL_MS) {
      window.localStorage.removeItem(key)
      return {}
    }
    return sanitizeEdits(envelope.d)
  } catch {
    return {}
  }
}

/** Persist, or drop the slot when there is nothing left to keep. */
export function saveDraft(key: string, edits: Edits): void {
  try {
    if (Object.keys(edits).length === 0) {
      window.localStorage.removeItem(key)
      return
    }
    window.localStorage.setItem(
      key,
      JSON.stringify({ v: DRAFT_SCHEMA_VERSION, d: edits, t: Date.now() })
    )
  } catch {
    /* private mode / quota: the draft is a convenience, never a requirement */
  }
}
