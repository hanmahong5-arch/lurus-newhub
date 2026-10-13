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
import type { Redemption } from '../types'

import { REDEMPTION_STATUS } from '../constants'

/** Unix seconds; 0 means the code never expires. */
export function isTimestampExpired(
  timestamp: number,
  nowMs: number = Date.now()
): boolean {
  if (timestamp === 0) return false
  return timestamp < nowMs / 1000
}

/** Only an unused code can be expired; a used or disabled one is not. */
export function isRedemptionExpired(
  redemption: Pick<Redemption, 'status' | 'expired_time'>,
  nowMs: number = Date.now()
): boolean {
  return (
    redemption.status === REDEMPTION_STATUS.ENABLED &&
    isTimestampExpired(redemption.expired_time, nowMs)
  )
}

/** The listing masks unused codes with asterisks. */
export function isMaskedKey(key: string): boolean {
  return key.includes('*')
}

/** UTF-8 length, the unit the server limits names in. */
export function byteLength(value: string): number {
  return new TextEncoder().encode(value).length
}
