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
import { ApiError, http, tenantApi, tenantUrl } from '@/lib/api'

import {
  MISSING_CHANNEL_ID,
  MISSING_KEY,
  type ChannelCreateBody,
  type ChannelTestResult,
  type CreatedChannel,
  type IssuedKey,
  type TokenCreateBody,
  type UpstreamModels,
} from './types'

function record(value: unknown): Record<string, unknown> {
  return typeof value === 'object' && value !== null
    ? (value as Record<string, unknown>)
    : {}
}

function strings(value: unknown): string[] {
  return Array.isArray(value)
    ? value.filter((v): v is string => typeof v === 'string' && v !== '')
    : []
}

/** POST /channels. A create without an id is not a create: fail loudly. */
export async function createChannel(
  body: ChannelCreateBody
): Promise<CreatedChannel> {
  const r = record(await tenantApi.post<unknown>('/channels', body))
  const id = typeof r.id === 'number' ? r.id : Number.NaN
  if (!Number.isFinite(id) || id <= 0) {
    throw new ApiError('The server did not return the new channel id', {
      code: MISSING_CHANNEL_ID,
    })
  }
  return { id, name: typeof r.name === 'string' ? r.name : body.name }
}

/** GET /channels/:id/upstream-models (read-only diff against the channel). */
export async function fetchUpstreamModels(id: number): Promise<UpstreamModels> {
  const r = record(
    await tenantApi.get<unknown>(`/channels/${id}/upstream-models`)
  )
  return {
    upstream: strings(r.upstream),
    new: strings(r.new),
    missing: strings(r.missing),
  }
}

/** PUT /channels/:id with only `models`; the server merges the rest. */
export async function updateChannelModels(
  id: number,
  models: string
): Promise<void> {
  await tenantApi.put(`/channels/${id}`, { models })
}

/**
 * POST /channels/:id/test. The result sits at the top level of the 200 body
 * (`{success, latency_ms, error?}`); the envelope helpers would read it as a
 * failed call with no message, so go through the raw client. A non-2xx answer
 * still rejects with an ApiError.
 */
export async function testChannel(id: number): Promise<ChannelTestResult> {
  const res = await http.post<unknown>(tenantUrl(`/channels/${id}/test`), {})
  const r = record(res.data)
  const latency = r.latency_ms
  return {
    success: r.success === true,
    latencyMs:
      typeof latency === 'number' && Number.isFinite(latency) ? latency : null,
    error: typeof r.error === 'string' ? r.error : '',
  }
}

/**
 * POST /tokens. The plaintext key is in this answer and nowhere else; an
 * answer without one is an error, never a success screen with an empty key.
 */
export async function createToken(body: TokenCreateBody): Promise<IssuedKey> {
  const r = record(await tenantApi.post<unknown>('/tokens', body))
  const key = typeof r.key === 'string' ? r.key : ''
  if (key === '') {
    throw new ApiError('The server did not return the new key', {
      code: MISSING_KEY,
    })
  }
  return {
    id: typeof r.id === 'number' ? r.id : 0,
    key,
    name: typeof r.name === 'string' ? r.name : undefined,
  }
}
