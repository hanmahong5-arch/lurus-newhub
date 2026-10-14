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
import { api, http } from '@/lib/api'

import { API_ENDPOINTS } from './constants'
import type {
  ChatCompletionRequest,
  ChatCompletionResponse,
  GroupOption,
  ModelOption,
} from './types'

/**
 * Send a chat completion request (non-streaming).
 *
 * The relay answers with a bare chat-completions body (not the `{success,data}`
 * envelope), so this goes through the raw axios instance. Failures reject
 * with the ApiError the shared interceptor builds; the relay error body is
 * reachable through `cause` (see lib/streaming/request-error-utils).
 */
export async function sendChatCompletion(
  payload: ChatCompletionRequest,
  signal?: AbortSignal
): Promise<ChatCompletionResponse> {
  const res = await http.post<ChatCompletionResponse>(
    API_ENDPOINTS.CHAT_COMPLETIONS,
    payload,
    { signal }
  )
  return res.data
}

/** Models the signed-in user may call in the given group. */
export async function getUserModels(group: string): Promise<ModelOption[]> {
  const data = await api.get<string[] | null>(API_ENDPOINTS.USER_MODELS, {
    params: { group },
  })
  if (!Array.isArray(data)) return []
  return data
    .filter((model) => typeof model === 'string' && model !== '')
    .map((model) => ({ label: model, value: model }))
}

interface UserGroupInfo {
  desc?: string
  /** A multiplier, or a label (e.g. the automatic group) instead of a number. */
  ratio?: number | string
}

/** Groups the signed-in user may route through. */
export async function getUserGroups(): Promise<GroupOption[]> {
  const data = await api.get<Record<string, UserGroupInfo> | null>(
    API_ENDPOINTS.USER_GROUPS
  )
  if (!data || typeof data !== 'object') return []

  // label is for the trigger button (name only); desc is for the dropdown.
  return Object.entries(data).map(([group, info]) => ({
    label: group,
    value: group,
    ratio: typeof info?.ratio === 'number' ? info.ratio : undefined,
    desc: info?.desc,
  }))
}
