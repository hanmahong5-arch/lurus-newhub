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
import type { TFunction } from 'i18next'

import { ApiError } from '@/lib/api'
import type { MoneyConfig } from '@/lib/money'

import { MISSING_KEY, type TokenCreateBody } from '../types'

/** Backend app.TokenNameMaxLength; Go compares UTF-8 bytes, not characters. */
export const TOKEN_NAME_MAX_BYTES = 50
/** Backend MaxQuotaMultiplier; the cap is this * quota-per-unit. */
export const MAX_QUOTA_MULTIPLIER = 1_000_000_000

/**
 * Wizard draft. Safe to persist across a refresh: it never holds the issued
 * secret, only what the person typed into the form.
 */
export interface TokenDraft {
  name: string
  /** Blank = the server's "default" group. */
  group: string
  unlimited: boolean
  /** Total quota in the operator's display currency. */
  cap: string
  /** `yyyy-mm-dd`, blank = never expires. */
  expires: string
}

export const TOKEN_DRAFT_INIT: TokenDraft = {
  name: '',
  group: '',
  unlimited: true,
  cap: '',
  expires: '',
}

/** Same key the legacy console used, so a half-filled draft survives the move. */
export const TOKEN_DRAFT_KEY = 'flow-newtoken-draft'

function utf8Bytes(value: string): number {
  return new TextEncoder().encode(value).length
}

export function loadTokenDraft(): TokenDraft {
  try {
    const raw = window.localStorage.getItem(TOKEN_DRAFT_KEY)
    if (raw === null) return TOKEN_DRAFT_INIT
    const parsed: unknown = JSON.parse(raw)
    if (typeof parsed !== 'object' || parsed === null) return TOKEN_DRAFT_INIT
    const r = parsed as Record<string, unknown>
    return {
      name: typeof r.name === 'string' ? r.name : '',
      group: typeof r.group === 'string' ? r.group : '',
      unlimited: typeof r.unlimited === 'boolean' ? r.unlimited : true,
      cap: typeof r.cap === 'string' ? r.cap : '',
      expires: typeof r.expires === 'string' ? r.expires : '',
    }
  } catch {
    return TOKEN_DRAFT_INIT
  }
}

export function saveTokenDraft(draft: TokenDraft): void {
  try {
    window.localStorage.setItem(TOKEN_DRAFT_KEY, JSON.stringify(draft))
  } catch {
    // Storage full or blocked: the draft just stays in memory.
  }
}

export function clearTokenDraft(): void {
  try {
    window.localStorage.removeItem(TOKEN_DRAFT_KEY)
  } catch {
    // Nothing to clear.
  }
}

export type ScopeIssue = 'name_required' | 'name_too_long'

/** Step 1 (name & scope). */
export function validateScope(draft: TokenDraft): ScopeIssue | null {
  const name = draft.name.trim()
  if (name === '') return 'name_required'
  if (utf8Bytes(name) > TOKEN_NAME_MAX_BYTES) return 'name_too_long'
  return null
}

export type LimitsIssue =
  | 'cap_unavailable'
  | 'cap_invalid'
  | 'cap_too_large'
  | 'expiry_invalid'
  | 'expiry_past'

export type LimitsResult =
  | { ok: true; quota: number; expiredTime: number }
  | { ok: false; issue: LimitsIssue }

/** Unit the cap input is typed in: the operator's display currency. */
export function capUnitLabel(config: MoneyConfig | null): string {
  if (config === null) return ''
  return config.displayType === 'TOKENS' ? 'tokens' : config.symbol
}

/** Display-currency amount typed by a person -> internal quota units. */
export function displayToQuota(value: number, config: MoneyConfig): number {
  if (config.displayType === 'TOKENS') return Math.round(value)
  return Math.round((value / config.rate) * config.quotaPerUnit)
}

/**
 * A bare date means the END of that local day, so "expires today" still works
 * all day. NaN = not a real date.
 */
export function expiryToUnix(value: string): number {
  if (value === '') return -1
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return Number.NaN
  const ms = new Date(`${value}T23:59:59`).getTime()
  return Number.isFinite(ms) ? Math.floor(ms / 1000) : Number.NaN
}

/**
 * Step 2 (limits). The quota must be a positive amount: an empty or zero cap
 * is rejected rather than quietly saved as "unlimited" or as $0.
 */
export function validateLimits(
  draft: TokenDraft,
  config: MoneyConfig | null,
  nowSec: number
): LimitsResult {
  let quota = 0
  if (!draft.unlimited) {
    if (config === null) return { ok: false, issue: 'cap_unavailable' }
    const text = draft.cap.trim()
    if (!/^\d+(\.\d+)?$/.test(text)) return { ok: false, issue: 'cap_invalid' }
    const value = Number.parseFloat(text)
    if (!(value > 0)) return { ok: false, issue: 'cap_invalid' }
    quota = displayToQuota(value, config)
    if (quota <= 0) return { ok: false, issue: 'cap_invalid' }
    if (quota > MAX_QUOTA_MULTIPLIER * config.quotaPerUnit) {
      return { ok: false, issue: 'cap_too_large' }
    }
  }
  const expiredTime = expiryToUnix(draft.expires)
  if (Number.isNaN(expiredTime)) return { ok: false, issue: 'expiry_invalid' }
  if (expiredTime !== -1 && expiredTime <= nowSec) {
    return { ok: false, issue: 'expiry_past' }
  }
  return { ok: true, quota, expiredTime }
}

/** Exactly the body CreateTokenV2 parses. */
export function buildTokenBody(
  draft: TokenDraft,
  limits: { quota: number; expiredTime: number }
): TokenCreateBody {
  return {
    name: draft.name.trim(),
    group: draft.group.trim() || 'default',
    unlimited_quota: draft.unlimited,
    remain_quota: draft.unlimited ? 0 : limits.quota,
    expired_time: limits.expiredTime,
  }
}

/** Message for a failed create: the server's own wording, else a fallback. */
export function describeTokenError(error: unknown, t: TFunction): string {
  if (error instanceof ApiError && error.code === MISSING_KEY) {
    return t('The server did not return the new key')
  }
  const message =
    error instanceof Error && error.message.trim() !== '' ? error.message : ''
  return message || t('Failed to create key')
}

function pad(n: number): string {
  return String(n).padStart(2, '0')
}

/** `yyyy-mm-dd` (local) for "today + days", the expiry quick presets. */
export function dateAfterDays(from: Date, days: number): string {
  const d = new Date(from)
  d.setDate(d.getDate() + days)
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}
