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
import { tenantApi, type ApiError } from '@/lib/api'

import {
  normalizeDiffs,
  normalizeSheet,
  type BatchItem,
  type DiffEntry,
  type PricingSheet,
} from './lib/pricing'

export const pricingQueryKey = ['org-pricing', 'sheet'] as const

/** Header that pins a write to the version the page last read. */
export const VERSION_HEADER = 'If-Match-Pricing-Version'

/** GET /api/v2/~/pricing (handler.GetPricingV2). */
export async function fetchPricing(): Promise<PricingSheet> {
  return normalizeSheet(await tenantApi.get<unknown>('/pricing'))
}

/** POST /api/v2/~/pricing/preview: read-only dry run (root only). */
export async function previewPricing(batch: BatchItem[]): Promise<DiffEntry[]> {
  const data = await tenantApi.post<{ diffs?: unknown }>(
    '/pricing/preview',
    batch
  )
  return normalizeDiffs(data?.diffs)
}

/** POST /api/v2/~/pricing (handler.UpdatePricingV2): CAS-guarded write. */
export async function savePricing(
  batch: BatchItem[],
  version: number
): Promise<{ updated_count: number; new_version?: number }> {
  const data = await tenantApi.post<{
    updated_count?: number
    new_version?: number
  }>('/pricing', batch, { headers: { [VERSION_HEADER]: String(version) } })
  return {
    updated_count: data?.updated_count ?? batch.length,
    new_version: data?.new_version,
  }
}

export const VERSION_CONFLICT_CODE = 'PRICING_VERSION_CONFLICT'

/** The CAS lost: someone else saved prices since this page read them. */
export function isVersionConflict(err: ApiError | { status?: number; code?: string }): boolean {
  return err.status === 409 || err.code === VERSION_CONFLICT_CODE
}
