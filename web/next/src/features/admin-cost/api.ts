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
import { queryOptions } from '@tanstack/react-query'

import { api } from '@/lib/api'

import type { SavingsReport } from './types'

/** The analyzer's natural window: the last 7 days. */
export const SAVINGS_WINDOW_HOURS = 168

/**
 * Read the savings report. `null` is a real answer (the endpoint replied with
 * an empty payload); a failure of any kind rejects and is presented as a
 * failure, never as "nothing to save". A 401 is surfaced to the page instead of
 * bouncing the browser, so the person sees why the page is empty.
 */
export async function fetchSavings(): Promise<SavingsReport | null> {
  const body = await api.get<Partial<SavingsReport> | null>(
    `/api/v2/admin/governance/savings?hours=${SAVINGS_WINDOW_HOURS}`,
    { skipAuthRedirect: true }
  )
  if (body === null || body === undefined) return null
  return {
    ...body,
    total_spend_quota: body.total_spend_quota ?? 0,
    spend_by_model: Array.isArray(body.spend_by_model)
      ? body.spend_by_model
      : [],
    spend_by_product: Array.isArray(body.spend_by_product)
      ? body.spend_by_product
      : [],
    scenarios: body.scenarios ?? {},
    excluded: Array.isArray(body.excluded) ? body.excluded : [],
  }
}

export const savingsQuery = queryOptions({
  queryKey: ['admin-cost', 'savings', SAVINGS_WINDOW_HOURS],
  queryFn: fetchSavings,
  retry: false,
  meta: { errorToast: false },
})
