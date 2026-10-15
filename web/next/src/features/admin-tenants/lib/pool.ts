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
import { POOL_LOW_RATIO, POOL_UNLIMITED } from '../constants'
import type { CreditPool } from '../types'

/** What the pool's balance means for the traffic that draws on it. */
export type PoolHealth =
  | 'unconfigured'
  | 'unlimited'
  | 'exhausted'
  | 'warning'
  | 'ok'

/** True when max_balance signals "no ceiling". */
export function isUnlimited(pool: CreditPool | null | undefined): boolean {
  return !!pool && pool.max_balance === POOL_UNLIMITED
}

/**
 * Health from the current/max ratio:
 *   unlimited - no ceiling
 *   exhausted - balance <= 0 (the relay gate is refusing traffic)
 *   warning   - under 20% of the ceiling
 *   ok        - anything else
 */
export function poolHealth(pool: CreditPool | null | undefined): PoolHealth {
  if (!pool) return 'unconfigured'
  if (isUnlimited(pool)) return 'unlimited'
  const balance = pool.current_balance ?? 0
  if (balance <= 0) return 'exhausted'
  if (pool.max_balance > 0 && balance / pool.max_balance < POOL_LOW_RATIO) {
    return 'warning'
  }
  return 'ok'
}

/** A top-up needs a pool to land in. */
export function isTopupDisabled(pool: CreditPool | null | undefined): boolean {
  return !pool
}

/** Share of the ceiling still in the pool (0..1); null without a finite ceiling. */
export function poolFillRatio(
  pool: CreditPool | null | undefined
): number | null {
  if (!pool || isUnlimited(pool) || pool.max_balance <= 0) return null
  return Math.min(1, Math.max(0, (pool.current_balance ?? 0) / pool.max_balance))
}

export type CeilingError = 'not-a-number' | 'negative'

/** max_balance in quota units: -1 (unlimited) or >= 0. */
export function validateMaxBalance(value: number): CeilingError | null {
  if (!Number.isFinite(value)) return 'not-a-number'
  if (value < 0 && value !== POOL_UNLIMITED) return 'negative'
  return null
}

export type TopupAmountError = 'not-a-number' | 'not-integer' | 'not-positive'

/** A top-up amount in quota units: a positive integer. */
export function validateTopupAmount(value: number): TopupAmountError | null {
  if (!Number.isFinite(value)) return 'not-a-number'
  if (!Number.isInteger(value)) return 'not-integer'
  if (value <= 0) return 'not-positive'
  return null
}

const MONEY_PATTERN = /^\d+(\.\d+)?$/

/** A typed money amount ("5", "12.50"); null for anything else. */
export function parseMoneyText(raw: string): number | null {
  const text = raw.trim()
  if (!MONEY_PATTERN.test(text)) return null
  const value = Number.parseFloat(text)
  return Number.isFinite(value) ? value : null
}

export type FromDisplay = (amount: number) => number | null

export type TopupParse =
  | { ok: true; amount: number }
  | { ok: false; error: 'currency' | TopupAmountError }

/**
 * Typed top-up (display money) -> quota units. The conversion only runs
 * through the operator's currency settings; without them nothing is guessed.
 */
export function parseTopup(raw: string, fromDisplay: FromDisplay): TopupParse {
  const typed = parseMoneyText(raw)
  if (typed === null) return { ok: false, error: 'not-a-number' }
  const quota = fromDisplay(typed)
  if (quota === null) return { ok: false, error: 'currency' }
  const error = validateTopupAmount(quota)
  return error ? { ok: false, error } : { ok: true, amount: quota }
}

export type CeilingParse =
  | { ok: true; value: number }
  | { ok: false; error: 'currency' | CeilingError }

/** The pool ceiling form -> max_balance in quota units (-1 = unlimited). */
export function parseCeiling(
  input: { unlimited: boolean; amount: string },
  fromDisplay: FromDisplay
): CeilingParse {
  if (input.unlimited) return { ok: true, value: POOL_UNLIMITED }
  const typed = parseMoneyText(input.amount)
  if (typed === null) return { ok: false, error: 'not-a-number' }
  const quota = fromDisplay(typed)
  if (quota === null) return { ok: false, error: 'currency' }
  const error = validateMaxBalance(quota)
  return error ? { ok: false, error } : { ok: true, value: quota }
}

/** alert_threshold_pct: a whole percent from 1 to 100. */
export function parseAlertPct(raw: string): number | null {
  const text = raw.trim()
  if (!/^\d+$/.test(text)) return null
  const value = Number.parseInt(text, 10)
  return value >= 1 && value <= 100 ? value : null
}
