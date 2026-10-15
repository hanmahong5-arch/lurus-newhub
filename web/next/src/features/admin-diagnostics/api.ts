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
import { ApiError, api, http, unwrap } from '@/lib/api'

import type {
  AffinitySnapshot,
  AffinityStats,
  PurgeAllResult,
  TotpStats,
} from './types'

export const diagnosticsKeys = {
  all: ['admin-diagnostics'] as const,
  affinity: ['admin-diagnostics', 'affinity'] as const,
  totp: ['admin-diagnostics', 'totp'] as const,
}

const AFFINITY_URL = '/api/v2/admin/routing/affinity'
const TOTP_URL = '/api/v2/admin/security/totp-stats'

/**
 * The affinity read keeps the envelope's top-level `scope`, which the shared
 * `api.get` drops: it is what tells the operator these counters belong to the
 * one replica that answered.
 */
export async function fetchAffinity(): Promise<AffinitySnapshot> {
  const res = await http.request<unknown>({ method: 'GET', url: AFFINITY_URL })
  const stats = unwrap<AffinityStats>(res.data, res.status)
  const scope = (res.data as { scope?: unknown }).scope
  return { stats, scope: typeof scope === 'string' ? scope : null }
}

export function fetchTotpStats(): Promise<TotpStats> {
  return api.get<TotpStats>(TOTP_URL)
}

/**
 * Purge one binding. The server answers 204 (no body) when it removed one,
 * 404 when none exists, 500 when the store itself failed. Anything else that
 * resolves, such as a session-auth refusal that arrives as HTTP 200
 * {success:false}, is neither success nor a rejection, so it is raised here
 * rather than letting the button look like it did nothing.
 */
export async function purgeAffinityKey(key: string): Promise<void> {
  const res = await http.request<{ message?: string } | undefined>({
    method: 'DELETE',
    url: `${AFFINITY_URL}/${encodeURIComponent(key)}`,
  })
  if (res.status === 204) return
  throw new ApiError(res.data?.message?.trim() || 'purge failed', {
    status: res.status,
  })
}

export function purgeAllAffinity(): Promise<PurgeAllResult> {
  return api.delete<PurgeAllResult>(`${AFFINITY_URL}?all=true`)
}
