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
import { QUOTA_COLUMN_MAX } from '../constants'
import type { QuotaAdjustMode } from '../types'

export type QuotaResult =
  | { ok: true; quota: number }
  | { ok: false; reason: 'invalid' | 'negative' | 'too-large' }

/**
 * New balance after an adjustment, in quota units. `amount` is already
 * converted from display money; for add/subtract it must be positive, for
 * override it may be zero. A balance below zero is refused (the platform
 * admin API refuses it too), as is one that does not fit the column.
 */
export function applyQuotaAdjust(
  current: number,
  mode: QuotaAdjustMode,
  amount: number
): QuotaResult {
  if (!Number.isFinite(amount) || !Number.isFinite(current)) {
    return { ok: false, reason: 'invalid' }
  }
  if (mode !== 'override' && amount <= 0) return { ok: false, reason: 'invalid' }
  if (mode === 'override' && amount < 0) return { ok: false, reason: 'negative' }
  let next = amount
  if (mode === 'add') next = current + amount
  if (mode === 'subtract') next = current - amount
  if (next < 0) return { ok: false, reason: 'negative' }
  if (next > QUOTA_COLUMN_MAX) return { ok: false, reason: 'too-large' }
  return { ok: true, quota: Math.round(next) }
}

/** Parse an amount typed by a person; empty / non-numeric gives null. */
export function parseAmount(raw: string): number | null {
  const text = raw.trim()
  if (!/^-?\d+(\.\d+)?$/.test(text)) return null
  const value = Number.parseFloat(text)
  return Number.isFinite(value) ? value : null
}
