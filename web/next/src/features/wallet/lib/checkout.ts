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

/**
 * Client-side bounds for the typed amount. The server only requires > 0; these
 * match what the previous console accepted so a typo (an extra zero) is caught
 * before an order is opened.
 */
export const MIN_TOPUP_CNY = 1
export const MAX_TOPUP_CNY = 100000

/** Quick-pick amounts (CNY). Shortcuts only; any amount in range works. */
export const PRESET_AMOUNTS_CNY = [10, 50, 100, 200, 500, 1000] as const

/** Round to fen so 0.1 + 0.2 style input never reaches the platform. */
export function parseAmount(raw: string): number | null {
  const trimmed = raw.trim()
  if (trimmed === '') return null
  const n = Number(trimmed)
  if (!Number.isFinite(n)) return null
  return Math.round(n * 100) / 100
}

export function amountError(
  amount: number | null,
  t: TFunction
): string | null {
  if (amount === null) return t('Enter an amount.')
  if (amount < MIN_TOPUP_CNY || amount > MAX_TOPUP_CNY) {
    return t('Enter an amount between {{min}} and {{max}} CNY.', {
      min: MIN_TOPUP_CNY,
      max: MAX_TOPUP_CNY,
    })
  }
  return null
}

const PAID = new Set(['paid', 'success', 'succeeded', 'completed', 'complete'])
const FAILED = new Set([
  'expired',
  'failed',
  'cancelled',
  'canceled',
  'closed',
  'refunded',
])

export type OrderPhase = 'paid' | 'failed' | 'pending'

/** Unknown statuses stay `pending`: the poll keeps asking until expiry. */
export function orderPhase(status: string | undefined | null): OrderPhase {
  const s = (status ?? '').trim().toLowerCase()
  if (PAID.has(s)) return 'paid'
  if (FAILED.has(s)) return 'failed'
  return 'pending'
}

/** A payment method that shows a code instead of redirecting. */
export function isQrMethod(type: string): boolean {
  return type.trim().toLowerCase() === 'qr'
}

/** Milliseconds until an RFC 3339 expiry, or null when none was given. */
export function msUntil(iso: string | null | undefined, now = Date.now()) {
  if (!iso) return null
  const at = Date.parse(iso)
  return Number.isNaN(at) ? null : at - now
}

/**
 * One idempotency key per pay intent. Reused while the same amount+method is
 * retried (a double click or a retry after a timeout opens ONE order), minted
 * afresh once the order exists or the intent changes.
 */
export class IntentKey {
  private intent = ''
  private key = ''

  keyFor(amount: number, methodId: string): string {
    const intent = `${amount.toFixed(2)}|${methodId}`
    if (intent !== this.intent || this.key === '') {
      this.intent = intent
      this.key = newKey()
    }
    return this.key
  }

  reset(): void {
    this.intent = ''
    this.key = ''
  }
}

function newKey(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return crypto.randomUUID()
  }
  return `k-${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`
}

/** Message for a failed checkout: the server's own words when it gave any. */
export function checkoutErrorMessage(err: unknown, t: TFunction): string {
  if (err instanceof ApiError) {
    if (err.status === 400 && err.message.trim()) return err.message
    if (err.status === 401 || err.status === 503) {
      if (/not linked/i.test(err.message)) {
        return t(
          'Your sign-in is not linked to a platform account, so online top-up is unavailable.'
        )
      }
      return t('The billing service is unavailable. Please try again later.')
    }
    if (err.message.trim()) return err.message
  }
  return t('Failed to create the top-up order.')
}
