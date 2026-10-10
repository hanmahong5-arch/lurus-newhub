import { queryOptions, useQuery } from '@tanstack/react-query'

import { api } from '@/lib/api'
import { parseMoneyConfig, type MoneyConfig } from '@/lib/money'

/** The part of GET /api/status (handler.GetStatus) the console reads. */
export interface SystemStatus {
  system_name?: string
  logo?: string
  version?: string
  quota_per_unit?: number
  quota_display_type?: string
  usd_exchange_rate?: number
  custom_currency_symbol?: string
  custom_currency_exchange_rate?: number
  docs_link?: string
  [key: string]: unknown
}

export const statusQueryOptions = queryOptions({
  queryKey: ['status'],
  queryFn: () =>
    api.get<SystemStatus>('/api/status', { skipAuthRedirect: true }),
  staleTime: 5 * 60_000,
})

/** Money display configuration, or null until /api/status has answered. */
export function useMoneyConfig(): MoneyConfig | null {
  const { data } = useQuery({
    ...statusQueryOptions,
    select: (status) => parseMoneyConfig(status),
  })
  return data ?? null
}
