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

import { tenantApi } from '@/lib/api'

import {
  mapIssuedKey,
  mapProjects,
  mapRoutableModels,
  mapTokenList,
} from './lib/map'
import type { TokenWriteBody } from './types'

/** Server caps `size` at 100. There is no keyword search on this endpoint. */
export const PAGE_SIZE = 20

export const keysQueryKey = ['keys'] as const

export function tokenListQueryOptions(page: number, size: number) {
  return queryOptions({
    queryKey: [...keysQueryKey, 'list', page, size],
    queryFn: async () =>
      mapTokenList(
        await tenantApi.get('/tokens', { params: { p: page, size } })
      ),
  })
}

export const projectsQueryOptions = queryOptions({
  queryKey: [...keysQueryKey, 'projects'],
  queryFn: async () => mapProjects(await tenantApi.get('/projects')),
  staleTime: 60_000,
})

export const routableModelsQueryOptions = queryOptions({
  queryKey: [...keysQueryKey, 'routable-models'],
  queryFn: async () =>
    mapRoutableModels(await tenantApi.get('/models/routable')),
  staleTime: 60_000,
})

export async function createToken(body: TokenWriteBody) {
  return mapIssuedKey(await tenantApi.post('/tokens', body))
}

export async function updateToken(
  id: number,
  body: Partial<TokenWriteBody> & { status?: number }
) {
  await tenantApi.put(`/tokens/${id}`, body)
}

export async function deleteToken(id: number) {
  await tenantApi.delete(`/tokens/${id}`)
}

export async function deleteTokens(ids: number[]) {
  await tenantApi.post('/tokens/batch-delete', { ids })
}

/** New secret for the key; the old one stops working immediately. */
export async function rotateToken(id: number) {
  return mapIssuedKey(await tenantApi.post(`/tokens/${id}/rotate`))
}
