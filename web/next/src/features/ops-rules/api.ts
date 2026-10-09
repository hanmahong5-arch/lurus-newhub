import { queryOptions } from '@tanstack/react-query'

import { api, tenantApi } from '@/lib/api'

import {
  mapApplyOutcome,
  mapChannelPage,
  mapRule,
  mapRuleList,
  mapTemplate,
  mapTemplateList,
} from './lib/map'
import type { RuleWriteBody, TemplateWriteBody } from './types'

export const rulesQueryKey = ['ops-rules', 'rules'] as const
export const templatesQueryKey = ['ops-rules', 'templates'] as const

export const CHANNEL_PAGE_SIZE = 100
/** Server cap for one apply call. */
export const MAX_APPLY_CHANNELS = 500

export const rulesQueryOptions = queryOptions({
  queryKey: rulesQueryKey,
  queryFn: async () =>
    mapRuleList(await api.get('/api/v2/admin/content-rules')),
})

export const templatesQueryOptions = queryOptions({
  queryKey: templatesQueryKey,
  queryFn: async () =>
    mapTemplateList(await api.get('/api/v2/admin/channel-templates')),
})

export async function createRule(body: RuleWriteBody) {
  return mapRule(await api.post('/api/v2/admin/content-rules', body))
}

export async function updateRule(id: number, body: RuleWriteBody) {
  return mapRule(await api.put(`/api/v2/admin/content-rules/${id}`, body))
}

export async function deleteRule(id: number) {
  await api.delete(`/api/v2/admin/content-rules/${id}`)
}

export async function createTemplate(body: TemplateWriteBody) {
  return mapTemplate(await api.post('/api/v2/admin/channel-templates', body))
}

export async function updateTemplate(id: number, body: TemplateWriteBody) {
  return mapTemplate(
    await api.put(`/api/v2/admin/channel-templates/${id}`, body)
  )
}

export async function deleteTemplate(id: number) {
  await api.delete(`/api/v2/admin/channel-templates/${id}`)
}

export async function applyTemplate(id: number, channelIds: number[]) {
  return mapApplyOutcome(
    await api.post(`/api/v2/admin/channel-templates/${id}/apply`, {
      channel_ids: channelIds,
    })
  )
}

/** This session's tenant channels (staff only), for the apply picker. */
export function channelsQueryOptions(page: number) {
  return queryOptions({
    queryKey: ['ops-rules', 'channels', page],
    queryFn: async () =>
      mapChannelPage(
        await tenantApi.get('/channels', {
          params: { page, page_size: CHANNEL_PAGE_SIZE },
        })
      ),
  })
}
