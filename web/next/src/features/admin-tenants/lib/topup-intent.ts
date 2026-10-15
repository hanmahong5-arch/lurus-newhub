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
import { nanoid } from 'nanoid'

import { isApiError } from '@/lib/api'

/**
 * One top-up intent: the Idempotency-Key the server dedupes the wallet debit
 * and the pool credit on, with the figures it was minted for. A key is only
 * ever reused for the SAME amount and reason; the server answers a key reused
 * for a different amount with a conflict and credits nothing.
 */
export interface TopupIntent {
  key: string
  amount: number
  reason: string
}

export function newTopupKey(): string {
  return `console-topup-${nanoid(24)}`
}

/**
 * The intent to submit now. A retry of an attempt whose outcome is unknown
 * (timeout, 5xx, connection lost) keeps its key so the server settles it once;
 * a different amount or reason is a different intent and gets a fresh key.
 */
export function intentFor(
  previous: TopupIntent | null,
  amount: number,
  reason: string,
  makeKey: () => string = newTopupKey
): TopupIntent {
  if (previous && previous.amount === amount && previous.reason === reason) {
    return previous
  }
  return { key: makeKey(), amount, reason }
}

/**
 * True when a failed top-up is a definitive refusal: the server answered, so
 * the key is spent and the next attempt must mint a new one. An unanswered
 * request (no status) or a 5xx is ambiguous and keeps its key.
 */
export function failureIsDefinitive(error: unknown): boolean {
  if (!isApiError(error) || error.status === undefined) return false
  return error.status >= 400 && error.status < 500 && error.status !== 408
}
