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
  mapChannelDetail,
  mapChannelHealth,
  mapChannelList,
  mapChannelUsage,
  mapImportOutcome,
  mapKeyProbe,
} from './lib/map'
import type { ImportRequest, UsageWindow } from './types'

/** Server caps `page_size` at 100. */
export const PAGE_SIZE = 20

export const channelsQueryKey = ['ops-channels'] as const

export function channelListQueryOptions(page: number, keyword: string) {
  return queryOptions({
    queryKey: [...channelsQueryKey, 'list', page, keyword],
    queryFn: async () =>
      mapChannelList(
        await tenantApi.get('/channels', {
          params: {
            page,
            page_size: PAGE_SIZE,
            ...(keyword !== '' ? { keyword } : {}),
          },
        })
      ),
  })
}

export function channelDetailQueryOptions(id: number) {
  return queryOptions({
    queryKey: [...channelsQueryKey, 'detail', id],
    queryFn: async () =>
      mapChannelDetail(await tenantApi.get(`/channels/${id}`)),
    staleTime: 15_000,
  })
}

export function channelHealthQueryOptions(id: number) {
  return queryOptions({
    queryKey: [...channelsQueryKey, 'health', id],
    queryFn: async () =>
      mapChannelHealth(await tenantApi.get(`/channels/${id}/health`)),
    staleTime: 15_000,
  })
}

export function channelUsageQueryOptions(id: number, window: UsageWindow) {
  return queryOptions({
    queryKey: [...channelsQueryKey, 'usage', id, window],
    queryFn: async () =>
      mapChannelUsage(
        await tenantApi.get(`/channels/${id}/usage`, { params: { window } })
      ),
  })
}

export async function testChannelKey(id: number, idx: number) {
  return mapKeyProbe(await tenantApi.post(`/channels/${id}/keys/${idx}/test`))
}

export async function restoreChannelKey(
  id: number,
  idx: number,
  proof: string
) {
  await tenantApi.post(`/channels/${id}/keys/${idx}/restore`, { proof })
}

export interface KeySettingsBody {
  proxy?: string
  weight?: number
}

export async function updateKeySettings(
  id: number,
  idx: number,
  body: KeySettingsBody
) {
  await tenantApi.put(`/channels/${id}/keys/${idx}/settings`, body)
}

/** Replaces the channel's `setting` document (see buildSettingJson). */
export async function updateChannelSetting(id: number, setting: string) {
  await tenantApi.put(`/channels/${id}`, { setting })
}

export async function importChannelKeys(body: ImportRequest) {
  return mapImportOutcome(await tenantApi.post('/channels/import', body))
}
