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
import { useQuery } from '@tanstack/react-query'
import { useMemo } from 'react'

import { usdPerRatio } from '@/features/models/lib/catalog'
import { useStatus } from '@/hooks/use-status'
import { useMoneyConfig } from '@/lib/status'

import { asModelHealth, type ModelHealth } from '../api'
import { modelStatusQuery, usePublicPricing } from '../queries'
import { buildPublicModels, type PublicModel } from './lib/model'

/**
 * The public price list joined with the operator's money settings.
 *
 * Prices wait for /api/status (it carries `quota_per_unit`); if that call
 * fails the list still renders, with token prices marked unavailable rather
 * than guessed.
 */
export function usePublicModels() {
  const pricing = usePublicPricing()
  const { loading: statusLoading } = useStatus()
  const money = useMoneyConfig()
  const payload = pricing.data

  const models = useMemo<PublicModel[]>(() => {
    if (!payload || statusLoading) return []
    return buildPublicModels(payload, money ? usdPerRatio(money) : null)
  }, [payload, statusLoading, money])

  return {
    models,
    payload,
    money,
    isLoading: pricing.isLoading || (statusLoading && !pricing.isError),
    isError: pricing.isError,
    error: pricing.error,
    refetch: pricing.refetch,
    /** /api/status answered but gave no usable unit price. */
    moneyMissing: !statusLoading && money === null,
  }
}

/**
 * Health per model from the anonymous status endpoint. Purely decorative:
 * a failure leaves the map empty and the page unchanged.
 */
export function useModelHealth(): {
  health: Map<string, ModelHealth>
  updatedAt: number | undefined
  isLoading: boolean
} {
  const { data, isLoading } = useQuery(modelStatusQuery)
  const health = useMemo(() => {
    const map = new Map<string, ModelHealth>()
    for (const item of data?.models ?? []) {
      const value = asModelHealth(item.status)
      if (value) map.set(item.model, value)
    }
    return map
  }, [data])
  return { health, updatedAt: data?.updated_at, isLoading }
}
