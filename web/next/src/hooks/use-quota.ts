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
import { useCallback, useMemo } from 'react'

import {
  MONEY_UNAVAILABLE,
  formatQuota as formatQuotaWith,
  quotaToUsd,
  usdToQuota,
} from '@/lib/money'
import { useMoneyConfig } from '@/lib/status'

/**
 * Quota <-> display money bound to the operator's /api/status config.
 *
 * Replaces the donor's module-level `formatQuota` / `formatLogQuota` /
 * `parseQuotaFromDollars` / `quotaUnitsToDollars` / `getCurrencyDisplay`
 * and `useSystemConfigStore().config.currency`. While the config is not
 * loaded (`ready === false`) every formatter returns MONEY_UNAVAILABLE ("--")
 * and the converters return null: an amount is never guessed.
 */
export function useQuota() {
  const config = useMoneyConfig()

  const formatQuota = useCallback(
    (quota: number | null | undefined, digits = 2) =>
      formatQuotaWith(quota, config, digits),
    [config]
  )

  /** Higher precision for per-request costs in usage logs. */
  const formatLogQuota = useCallback(
    (quota: number | null | undefined) => formatQuotaWith(quota, config, 6),
    [config]
  )

  /** Quota units -> number shown in an editable money input (null: unknown). */
  const toDisplayAmount = useCallback(
    (quota: number): number | null => {
      if (config === null || !Number.isFinite(quota)) return null
      if (config.displayType === 'TOKENS') return Math.round(quota)
      return Number((quotaToUsd(quota, config) * config.rate).toFixed(6))
    },
    [config]
  )

  /** Number typed in display money -> quota units (null: unknown). */
  const fromDisplayAmount = useCallback(
    (amount: number): number | null => {
      if (config === null || !Number.isFinite(amount)) return null
      if (config.displayType === 'TOKENS') return Math.round(amount)
      return usdToQuota(amount / config.rate, config)
    },
    [config]
  )

  return useMemo(
    () => ({
      config,
      ready: config !== null,
      symbol: config?.symbol ?? '',
      isTokens: config?.displayType === 'TOKENS',
      formatQuota,
      formatLogQuota,
      toDisplayAmount,
      fromDisplayAmount,
      unavailable: MONEY_UNAVAILABLE,
    }),
    [
      config,
      formatQuota,
      formatLogQuota,
      toDisplayAmount,
      fromDisplayAmount,
    ]
  )
}
