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
import type { AxiosRequestConfig } from 'axios'

import { ApiError, http, tenantApi, toApiError } from '@/lib/api'

import {
  mapChannelDetail,
  mapChannelHealth,
  mapChannelList,
  mapChannelUsage,
  mapImportOutcome,
  mapKeyProbe,
} from './lib/map'
import type {
  AddChannelRequest,
  BatchDeleteParams,
  BatchSetTagParams,
  Channel,
  ChannelBalanceResponse,
  ChannelTestResponse,
  CopyChannelParams,
  CopyChannelResponse,
  FetchModelsResponse,
  GetChannelResponse,
  GetChannelsParams,
  GetChannelsResponse,
  ChannelListItem,
  ImportRequest,
  MultiKeyManageParams,
  MultiKeyStatusResponse,
  SearchChannelsParams,
  SearchChannelsResponse,
  ServerResponse,
  TagOperationParams,
  UsageWindow,
} from './types'

export const channelsQueryKey = ['ops-channels'] as const

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

// ============================================================================
// v1 channel management (/api/channel/...). These handlers answer HTTP 200
// with {success:false,message} on business failure, so each function returns
// the envelope as-is: callers check `success` (or use requireServerSuccess).
// HTTP-level failures (401/403/5xx) still reject with an ApiError.
// ============================================================================

async function call<T>(config: AxiosRequestConfig): Promise<T> {
  const res = await http.request<T>(config)
  return res.data
}

/** Throw the server's own message when an envelope reports failure. */
export function requireServerSuccess<
  T extends { success: boolean; message?: string; error_code?: string },
>(response: T, fallback = 'Request failed'): T {
  if (!response.success) {
    throw new ApiError(response.message?.trim() || fallback, {
      code: response.error_code,
    })
  }
  return response
}

export function getChannels(params: GetChannelsParams = {}) {
  return call<GetChannelsResponse>({
    method: 'GET',
    url: '/api/channel/',
    params,
  })
}

export function searchChannels(params: SearchChannelsParams) {
  return call<SearchChannelsResponse>({
    method: 'GET',
    url: '/api/channel/search',
    params,
  })
}

export function getChannel(id: number) {
  return call<GetChannelResponse>({ method: 'GET', url: `/api/channel/${id}` })
}

export function createChannel(data: AddChannelRequest) {
  return call<ServerResponse>({ method: 'POST', url: '/api/channel/', data })
}

/** Partial update: the server only writes non-zero fields of the body. */
export function updateChannel(id: number, data: Partial<Channel>) {
  return call<ServerResponse<Channel>>({
    method: 'PUT',
    url: '/api/channel/',
    data: { id, ...data },
  })
}

/**
 * Enable/disable. The server has no status endpoint, so this is a partial
 * update carrying only `status`.
 */
export function updateChannelStatus(id: number, status: number) {
  return updateChannel(id, { status })
}

/**
 * Batch enable/disable. There is no batch endpoint either: one partial update
 * per id; `data` is the number that succeeded.
 */
export async function batchUpdateChannelStatus(
  ids: number[],
  status: number
): Promise<ServerResponse<number>> {
  const results = await Promise.allSettled(
    ids.map((id) => updateChannelStatus(id, status))
  )
  const ok = results.filter(
    (r) => r.status === 'fulfilled' && r.value.success
  ).length
  if (ok === 0 && results.length > 0) {
    const first = results[0]
    const message =
      first.status === 'fulfilled'
        ? first.value.message
        : toApiError(first.reason).message
    return { success: false, message, data: 0 }
  }
  return { success: true, data: ok }
}

export function deleteChannel(id: number) {
  return call<ServerResponse>({ method: 'DELETE', url: `/api/channel/${id}` })
}

export function batchDeleteChannels(data: BatchDeleteParams) {
  return call<ServerResponse<number>>({
    method: 'POST',
    url: '/api/channel/batch',
    data,
  })
}

export function batchSetChannelTag(data: BatchSetTagParams) {
  return call<ServerResponse<number>>({
    method: 'POST',
    url: '/api/channel/batch/tag',
    data,
  })
}

export function testChannel(
  id: number,
  params?: { model?: string; endpoint_type?: string }
) {
  return call<ChannelTestResponse>({
    method: 'GET',
    url: `/api/channel/test/${id}`,
    params,
  })
}

export function updateChannelBalance(id: number) {
  return call<ChannelBalanceResponse>({
    method: 'GET',
    url: `/api/channel/update_balance/${id}`,
  })
}

export function fetchUpstreamModels(id: number) {
  return call<FetchModelsResponse>({
    method: 'GET',
    url: `/api/channel/fetch_models/${id}`,
  })
}

/** Fetch from the unsaved form (base URL, type and key only). */
export function fetchModels(data: {
  base_url: string
  type: number
  key?: string
}) {
  return call<FetchModelsResponse>({
    method: 'POST',
    url: '/api/channel/fetch_models',
    data,
  })
}

export function copyChannel(id: number, params: CopyChannelParams = {}) {
  return call<CopyChannelResponse>({
    method: 'POST',
    url: `/api/channel/copy/${id}`,
    params,
  })
}

export function fixChannelAbilities() {
  return call<ServerResponse<{ success: number; fails: number }>>({
    method: 'POST',
    url: '/api/channel/fix',
  })
}

export function deleteDisabledChannels() {
  return call<ServerResponse<number>>({
    method: 'DELETE',
    url: '/api/channel/disabled',
  })
}

/** Root-only and behind step-up verification (see useStepUp). */
export function getChannelKey(id: number) {
  return call<ServerResponse<{ key: string }>>({
    method: 'POST',
    url: `/api/channel/${id}/key`,
  })
}

export function testAllChannels() {
  return call<ServerResponse>({ method: 'GET', url: '/api/channel/test' })
}

export function updateAllChannelsBalance() {
  return call<ServerResponse>({
    method: 'GET',
    url: '/api/channel/update_balance',
  })
}

// ---- multi-key --------------------------------------------------------------

export function manageMultiKeys(params: MultiKeyManageParams) {
  return call<ServerResponse<unknown>>({
    method: 'POST',
    url: '/api/channel/multi_key/manage',
    data: params,
  })
}

export async function getMultiKeyStatus(
  channelId: number,
  page = 1,
  pageSize = 50,
  status?: number
) {
  return (await manageMultiKeys({
    channel_id: channelId,
    action: 'get_key_status',
    page,
    page_size: pageSize,
    status,
  })) as MultiKeyStatusResponse
}

const multiKeyAction =
  (action: MultiKeyManageParams['action']) =>
  (channelId: number, keyIndex?: number) =>
    manageMultiKeys({ channel_id: channelId, action, key_index: keyIndex })

export const enableMultiKey = multiKeyAction('enable_key')
export const disableMultiKey = multiKeyAction('disable_key')
export const deleteMultiKey = multiKeyAction('delete_key')
export const enableAllMultiKeys = multiKeyAction('enable_all_keys')
export const disableAllMultiKeys = multiKeyAction('disable_all_keys')
export const deleteDisabledMultiKeys = multiKeyAction('delete_disabled_keys')

// ---- tags -------------------------------------------------------------------

export function enableTagChannels(tag: string) {
  return call<ServerResponse>({
    method: 'POST',
    url: '/api/channel/tag/enabled',
    data: { tag },
  })
}

export function disableTagChannels(tag: string) {
  return call<ServerResponse>({
    method: 'POST',
    url: '/api/channel/tag/disabled',
    data: { tag },
  })
}

export function editTagChannels(params: TagOperationParams) {
  return call<ServerResponse>({
    method: 'PUT',
    url: '/api/channel/tag',
    data: params,
  })
}

export function getTagModels(tag: string) {
  return call<ServerResponse<string>>({
    method: 'GET',
    url: '/api/channel/tag/models',
    params: { tag },
  })
}

// ---- ollama -----------------------------------------------------------------

export function deleteOllamaModel(params: {
  channel_id: number
  model_name: string
}) {
  return call<ServerResponse>({
    method: 'DELETE',
    url: '/api/channel/ollama/delete',
    data: params,
  })
}

// ---- pickers ----------------------------------------------------------------

export function getGroups() {
  return call<ServerResponse<string[]>>({ method: 'GET', url: '/api/group/' })
}

export function getEnabledModels() {
  return call<ServerResponse<string[]>>({
    method: 'GET',
    url: '/api/channel/models_enabled',
  })
}

export function getPrefillGroups(type: 'model' | 'group' = 'model') {
  return call<
    ServerResponse<
      Array<{ id: number; name: string; items: string | string[] }>
    >
  >({ method: 'GET', url: '/api/prefill_group/', params: { type } })
}

/** Every model id the relay knows (admin catalogue picker). */
export function getAllModels() {
  return call<ServerResponse<Array<{ id: string }>>>({
    method: 'GET',
    url: '/api/channel/models',
  })
}

// ---- ops overview (tenant-scoped v2 list) ---------------------------------

const OVERVIEW_PAGE_SIZE = 100
const OVERVIEW_MAX_PAGES = 10

/**
 * Ops fields (routability, plan, key pool, modality) for the channels the v2
 * list can see, by channel id. The v1 table is the source of the rows; this is
 * joined on id and a failure just leaves those cells empty. `revision` ties a
 * refetch to the v1 list so both stay in step after a mutation.
 */
export function channelOpsOverviewQueryOptions(revision: number) {
  return queryOptions({
    queryKey: [...channelsQueryKey, 'overview', revision],
    queryFn: async () => {
      const byId = new Map<number, ChannelListItem>()
      for (let page = 1; page <= OVERVIEW_MAX_PAGES; page++) {
        const res = mapChannelList(
          await tenantApi.get('/channels', {
            params: { page, page_size: OVERVIEW_PAGE_SIZE },
          })
        )
        for (const item of res.items) byId.set(item.id, item)
        if (res.items.length === 0 || page * OVERVIEW_PAGE_SIZE >= res.total) {
          break
        }
      }
      return byId
    },
    staleTime: 15_000,
    retry: false,
  })
}
