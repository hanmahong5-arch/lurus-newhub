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
import { MONEY_UNAVAILABLE, formatQuota, type MoneyConfig } from '@/lib/money'

import type { ApiToken } from '../types'

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

/** Internal quota -> editable display-currency string (no symbol). */
export function quotaToDisplayInput(
  quota: number,
  config: MoneyConfig | null
): string {
  if (config === null) return ''
  if (config.displayType === 'TOKENS') return String(Math.round(quota))
  const value = (quota / config.quotaPerUnit) * config.rate
  return String(Number.parseFloat(value.toFixed(4)))
}

export type CapParse =
  | { ok: true; quota: number }
  | { ok: false; reason: 'unavailable' | 'invalid' }

/**
 * Read the "total cap" field. A cap must be a positive amount: an empty or
 * zero cap is rejected rather than quietly saved as "unlimited" (or as $0).
 */
export function parseCapInput(
  raw: string,
  config: MoneyConfig | null
): CapParse {
  if (config === null) return { ok: false, reason: 'unavailable' }
  const text = raw.trim()
  if (!/^\d+(\.\d+)?$/.test(text)) return { ok: false, reason: 'invalid' }
  const value = Number.parseFloat(text)
  if (!(value > 0)) return { ok: false, reason: 'invalid' }
  const quota = displayToQuota(value, config)
  if (quota <= 0) return { ok: false, reason: 'invalid' }
  return { ok: true, quota }
}

export interface QuotaView {
  unlimited: boolean
  /** used / total as 0..1; null when unlimited or total is unknown. */
  usedRatio: number | null
  used: string
  total: string
  remaining: string
}

/**
 * Quota columns. An unlimited key is "Unlimited" - never `$0` - and its
 * `remain_quota` (0 on the wire) is not shown as a balance.
 */
export function quotaView(
  token: ApiToken,
  config: MoneyConfig | null
): QuotaView {
  if (token.unlimited_quota) {
    return {
      unlimited: true,
      usedRatio: null,
      used: formatQuota(token.used_quota, config),
      total: MONEY_UNAVAILABLE,
      remaining: MONEY_UNAVAILABLE,
    }
  }
  const total = token.used_quota + token.remain_quota
  return {
    unlimited: false,
    usedRatio: total > 0 ? Math.min(1, token.used_quota / total) : null,
    used: formatQuota(token.used_quota, config),
    total: formatQuota(total, config),
    remaining: formatQuota(token.remain_quota, config),
  }
}
