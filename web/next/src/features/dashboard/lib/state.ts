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
import { formatQuota, MONEY_UNAVAILABLE, type MoneyConfig } from '@/lib/money'

/** Where a read stands. A failed read is `error`, never an empty result. */
export type LoadState = 'loading' | 'error' | 'ok'

export function loadState(q: {
  isPending: boolean
  isError: boolean
}): LoadState {
  if (q.isError) return 'error'
  return q.isPending ? 'loading' : 'ok'
}

/** Remaining quota: a negative balance means unlimited. */
export function formatRemaining(
  remaining: number | null | undefined,
  money: MoneyConfig | null
): string {
  if (typeof remaining !== 'number') return MONEY_UNAVAILABLE
  if (remaining < 0) return '∞'
  return formatQuota(remaining, money)
}
