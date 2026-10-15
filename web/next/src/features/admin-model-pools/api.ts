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

import type { Pool, PoolChannel, PoolModel, PoolsOverview } from './types'

export const modelPoolsQueryKey = ['admin-model-pools'] as const

function mapModel(m: Partial<PoolModel>): PoolModel {
  return {
    model: m.model ?? '',
    requests_24h: m.requests_24h ?? 0,
    errors_24h: m.errors_24h ?? 0,
    health: m.health ?? null,
  }
}

function mapChannel(c: Partial<PoolChannel>): PoolChannel {
  return {
    id: c.id ?? 0,
    name: c.name ?? '',
    type: c.type ?? 0,
    status: c.status ?? 'unknown',
    priority: c.priority ?? 0,
    weight: c.weight ?? 0,
    tenant_id: c.tenant_id ?? '',
    test_time: c.test_time ?? 0,
    response_time: c.response_time ?? 0,
    balance: c.balance ?? 0,
    keys: {
      total: c.keys?.total ?? 0,
      enabled: c.keys?.enabled ?? 0,
      cooling: c.keys?.cooling ?? 0,
      disabled: c.keys?.disabled ?? 0,
    },
    models: (c.models ?? []).map(mapModel),
  }
}

export function mapOverview(
  data: Partial<PoolsOverview> | null | undefined
): PoolsOverview {
  const pools: Pool[] = (data?.pools ?? []).map((p) => ({
    group: p.group,
    channels: (p.channels ?? []).map(mapChannel),
  }))
  return { generated_at: data?.generated_at ?? 0, pools }
}

/**
 * Root-only. `skipAuthRedirect`: a 401 here is rendered as its own "session
 * expired" panel rather than silently bouncing away from a half-read page.
 */
export const modelPoolsQueryOptions = queryOptions({
  queryKey: modelPoolsQueryKey,
  queryFn: async () =>
    mapOverview(
      await api.get<Partial<PoolsOverview>>('/api/v2/admin/model-pools', {
        skipAuthRedirect: true,
      })
    ),
  retry: false,
})
