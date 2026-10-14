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
import { api, tenantApi } from '@/lib/api'

import type {
  CreateRedemptionBody,
  CreatedCodes,
  RedemptionPage,
  RedemptionListParams,
  UpdateRedemptionBody,
} from './types'

export const redemptionsQueryKey = ['redemption-codes'] as const

/**
 * Tenant-scoped list. The server masks the key of every unused code, so a
 * listing never leaks a redeemable secret. Filters on name prefix / id only.
 */
export async function listRedemptions(
  params: RedemptionListParams
): Promise<RedemptionPage> {
  const keyword = params.keyword.trim()
  const page = await tenantApi.get<RedemptionPage>('/redemptions', {
    params: {
      page: params.page,
      page_size: params.pageSize,
      ...(keyword ? { keyword } : {}),
    },
  })
  return { ...page, redemptions: page.redemptions ?? [] }
}

export function createRedemptions(
  body: CreateRedemptionBody
): Promise<CreatedCodes> {
  return tenantApi.post<CreatedCodes>('/redemptions', body)
}

export function deleteRedemption(id: number): Promise<unknown> {
  return tenantApi.delete(`/redemptions/${id}`)
}

/**
 * Edit name / quota / expiry. The tenant routes have no update, so this goes
 * through the admin route, which scopes a non-root caller to their own tenant.
 */
export function updateRedemption(body: UpdateRedemptionBody): Promise<unknown> {
  return api.put('/api/redemption/', body)
}

/** Enable (1) or disable (2); a used code is terminal and is refused. */
export function setRedemptionStatus(
  id: number,
  status: number
): Promise<unknown> {
  return api.put('/api/redemption/?status_only=true', { id, status })
}

/** Deletes used, disabled and expired codes; resolves to the row count. */
export function deleteInvalidRedemptions(): Promise<number> {
  return api.delete<number>('/api/redemption/invalid')
}
